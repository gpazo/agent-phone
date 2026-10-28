#!/usr/bin/env python3
"""Validate four live vision feeds alongside independent Wikipedia controls."""

import argparse
import base64
from collections import Counter, deque
from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import select
import statistics
import subprocess
import sys
import threading
import time
import uuid

ROOT = Path(__file__).resolve().parent.parent
SPEC = importlib.util.spec_from_file_location("smoke_wikipedia", ROOT / "scripts" / "smoke-wikipedia.py")
SMOKE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(SMOKE)
CLI = SMOKE.CLI
MANIFEST = ROOT / "examples" / "wikipedia-four.json"
QUERIES = ["Saturn", "Volcano", "Jupiter", "Forest"]
MAX_JPEG, MAX_LINE, WINDOW = 2 * 1024 * 1024, 3 * 1024 * 1024 + 65536, 256
STATUSES = {"starting", "live", "stale", "reconnecting", "offline", "error", "timeout"}


class DeviceRun:
    def __init__(self, phone, query):
        self.phone, self.query = phone, query
        self.lock = threading.Condition()
        self.process = self.reader = self.stderr_reader = None
        self.stderr_tail = deque(maxlen=20)
        self.latest, self.error, self.lease, self.release_cursor = None, None, None, None
        self.status, self.message, self.expected_stop = "starting", "", False
        self.previous, self.last_receive = None, None
        self.frames = self.duplicates = self.epoch_changes = 0
        self.status_counts = Counter()
        self.ages, self.cadence, self.jpeg_sizes = (deque(maxlen=WINDOW) for _ in range(3))
        self.first_captured_at = self.last_captured_at = None
        self.checks = {"observations": 0, "observationErrors": 0}
        self.search_start = self.search_finish = self.final_observation = self.jpeg_path = None


require = SMOKE.require


def jpeg_dimensions(data):
    require(4 <= len(data) <= MAX_JPEG and data[:2] == b"\xff\xd8" and data[-2:] == b"\xff\xd9",
            "Frame is not a complete JPEG under 2 MiB")
    sof = {0xC0, 0xC1, 0xC2, 0xC3, 0xC5, 0xC6, 0xC7, 0xC9, 0xCA, 0xCB, 0xCD, 0xCE, 0xCF}
    i = 2
    while i < len(data) - 2:
        if data[i] != 0xFF:
            i += 1
            continue
        while i < len(data) and data[i] == 0xFF: i += 1
        marker, i = data[i], i + 1
        if marker in (0xD8, 0x01) or 0xD0 <= marker <= 0xD7:
            continue
        if i + 2 > len(data): break
        size = int.from_bytes(data[i:i + 2], "big")
        if size < 2 or i + size > len(data): break
        if marker in sof:
            body = data[i + 2:i + size]
            require(len(body) >= 5, "JPEG frame header is truncated")
            h, w = int.from_bytes(body[1:3], "big"), int.from_bytes(body[3:5], "big")
            require(w > 0 and h > 0, "JPEG dimensions are invalid")
            return w, h
        if marker == 0xDA: break
        i += size
    raise RuntimeError("JPEG has no frame dimensions")


def parse_frame(frame, cursor, device, allow_missing=False):
    require(isinstance(frame, dict) and frame.get("device") == device,
            f"Frame device {frame.get('device') if isinstance(frame, dict) else None!r} != {device}")
    cursor = frame.get("cursor") if isinstance(frame.get("cursor"), dict) else cursor
    require(isinstance(cursor, dict) and isinstance(cursor.get("epoch"), str) and cursor["epoch"], "Missing frame epoch")
    seq = cursor.get("sequence")
    require(isinstance(seq, int) and not isinstance(seq, bool) and seq >= 0, "Invalid frame sequence")
    stamp = frame.get("capturedAt")
    require(isinstance(stamp, str) and stamp, "Missing capturedAt")
    try: captured = datetime.fromisoformat(stamp.replace("Z", "+00:00"))
    except ValueError as error: raise RuntimeError(f"Invalid capturedAt {stamp!r}") from error
    captured = captured.replace(tzinfo=captured.tzinfo or timezone.utc).astimezone(timezone.utc)
    w, h = frame.get("width"), frame.get("height")
    require(isinstance(w, int) and w > 0 and isinstance(h, int) and h > 0, "Invalid frame dimensions")
    encoded = frame.get("jpeg")
    data = base64.b64decode(encoded, validate=True) if isinstance(encoded, str) else None
    require(data is not None or allow_missing, "Frame omitted JPEG")
    if data is not None:
        require(jpeg_dimensions(data) == (w, h), "JPEG dimensions differ from metadata")
    return {"device": device, "epoch": cursor["epoch"], "sequence": seq, "capturedAt": stamp,
            "capturedTime": captured, "width": w, "height": h, "jpeg": data or b""}


def parse_snapshot(value, device, allow_missing=False):
    require(isinstance(value, dict) and isinstance(value.get("state"), str), "Snapshot has no state")
    frame = value.get("frame")
    if frame is None:
        return value["state"], value.get("error", ""), None
    cursor = value.get("cursor") if isinstance(value.get("cursor"), dict) else None
    return value["state"], value.get("error", ""), parse_frame(frame, cursor, device, allow_missing)


def ingest(run, status, message, frame):
    now = time.monotonic()
    with run.lock:
        run.status, run.message = status, str(message or "")
        run.status_counts[status if status in STATUSES else "other"] += 1
        if frame:
            key = (frame["epoch"], frame["sequence"])
            require(not run.previous or key[0] != run.previous[0] or key[1] >= run.previous[1], "Frame cursor regressed")
            if key == run.previous:
                run.duplicates += 1
            else:
                require(status == "live", f"New frame arrived with status {status!r}")
                run.epoch_changes += int(run.previous is not None and key[0] != run.previous[0])
                run.ages.append((datetime.now(timezone.utc) - frame["capturedTime"]).total_seconds() * 1000)
                if run.last_receive is not None:
                    run.cadence.append((now - run.last_receive) * 1000)
                run.jpeg_sizes.append(len(frame["jpeg"]))
                run.last_receive, run.previous, run.latest = now, key, frame
                run.frames += 1
                run.first_captured_at = run.first_captured_at or frame["capturedAt"]
                run.last_captured_at = frame["capturedAt"]
        run.lock.notify_all()


def start_watch(run):
    proc = subprocess.Popen([CLI, "watch", "--device", run.phone["udid"]], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    run.process = proc

    def drain():
        for chunk in iter(lambda: proc.stderr.readline(8192), b""):
            run.stderr_tail.append(chunk.decode(errors="replace").rstrip())

    def read():
        for line in iter(lambda: proc.stdout.readline(MAX_LINE + 1), b""):
            try:
                require(len(line) <= MAX_LINE, "watch line exceeded 3 MiB")
                snapshot = json.loads(line)
                status, message, frame = parse_snapshot(snapshot, run.phone["udid"])
                ingest(run, status, message, frame)
                del line, snapshot, frame
            except Exception as error:
                with run.lock:
                    run.error, run.expected_stop = str(error), True
                    run.lock.notify_all()
                try:
                    proc.terminate()
                except OSError:
                    pass
                break
        with run.lock:
            if not run.expected_stop:
                run.error = f"watch exited ({proc.poll()}): {' | '.join(run.stderr_tail)}"
            run.lock.notify_all()

    run.stderr_reader = threading.Thread(target=drain, daemon=True)
    run.reader = threading.Thread(target=read, daemon=True)
    run.stderr_reader.start(); run.reader.start()


def wait_for(predicate, runs, seconds, label):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        for run in runs:
            with run.lock:
                require(not run.error, f"{run.phone['name']}: {run.error}")
        if predicate():
            return
        time.sleep(.1)
    raise RuntimeError(f"Timed out waiting for {label}: " + ", ".join(f"{r.phone['name']}={r.status}/{r.frames}" for r in runs))


def frame_call(method, device, after=None):
    args = {"device": device}
    if method == "wait_frame":
        args.update(afterEpoch=after["epoch"], afterSequence=after["sequence"], timeoutMs=10000)
    status, message, frame = parse_snapshot(SMOKE.call(method, args), device)
    require(status == "live" and frame, f"{method} returned {status!r}: {message}")
    if after:
        require(frame["epoch"] != after["epoch"] or frame["sequence"] > after["sequence"], "wait_frame cursor did not advance")
    return frame


def reject(method, args, code):
    reply = SMOKE.raw(method, args)
    error = reply.get("error", {})
    require(reply.get("ok") is False and isinstance(error, dict) and error.get("code") == code,
            f"Expected {method} rejection {code}, received {reply}")


def type_and_observe(args, observation, text, checks):
    try:
        return SMOKE.act(args, observation, SMOKE.target(observation, role="textfield", action="type"), "type", text=text)
    except Exception as error:
        if "Text replacement not acknowledged; inspect before retrying" not in str(error):
            raise
        checks.setdefault("unacknowledgedInputs", []).append(str(error))
        observed = SMOKE.wait_for(args, lambda o: SMOKE.has_query(o, text), checks, 30)
        checks.setdefault("inputsConfirmedByFreshObservation", []).append(text)
        return observed


def search_worker(run, barrier):
    phone, query = run.phone, run.query
    result = {"name": phone["name"], "device": phone["udid"], "query": query, "status": "failed"}
    try:
        lease = SMOKE.call("acquire", {"device": phone["udid"], "owner": "live-vision:" + uuid.uuid4().hex[:16]})
        run.lease = lease["token"]
        reject("acquire", {"device": phone["udid"], "owner": "live-vision-conflict-check"}, "device_busy")
        run.checks["leaseConflictRejected"] = True
        args = {"device": phone["udid"], "lease": run.lease}
        SMOKE.call("launch", dict(args, bundleId="org.wikimedia.wikipedia"))
        ready = SMOKE.wait_for(args, lambda o: SMOKE.target(o, "Skip", action="press")
            or SMOKE.target(o, "Search", action="press") or SMOKE.target(o, role="textfield", action="type"), run.checks, 45)
        skip = SMOKE.target(ready, "Skip", action="press")
        if skip:
            ready = SMOKE.act(args, ready, skip, "press")
            ready = SMOKE.wait_for(args, lambda o: SMOKE.target(o, "Search", action="press")
                or SMOKE.target(o, role="textfield", action="type"), run.checks, 30)
        if not SMOKE.target(ready, role="textfield", action="type"):
            ready = SMOKE.act(args, ready, SMOKE.target(ready, "Search", action="press"), "press")
            ready = SMOKE.wait_for(args, lambda o: SMOKE.target(o, role="textfield", action="type"), run.checks, 30)
        preflight = "Vision " + uuid.uuid4().hex[:4] + " " + query
        ready = type_and_observe(args, ready, preflight, run.checks)
        ready = SMOKE.wait_for(args, lambda o: SMOKE.has_query(o, preflight), run.checks, 30)
        old, _ = SMOKE.observe(args, run.checks)
        current, _ = SMOKE.observe(args, run.checks)
        old_field = SMOKE.target(old, role="textfield", action="type")
        reject("act", dict(args, observation=old["id"], ref=old_field["ref"], action="type", text="MUST NOT APPEAR"), "stale_observation")
        run.checks["staleObservationRejected"] = True
        first = frame_call("frame", phone["udid"])
        newer = frame_call("wait_frame", phone["udid"], first)
        run.checks["axReferenceHeldThroughFrameReads"] = current["id"]
        barrier.wait(timeout=70)
        run.search_start = time.monotonic()
        post = type_and_observe(args, current, query, run.checks)
        final = SMOKE.wait_for(args, lambda o: SMOKE.has_query(o, query) and SMOKE.has_result(o, query), run.checks, 60)
        run.search_finish, run.final_observation = time.monotonic(), final
        run.checks["heldReadCursors"] = [[first["epoch"], first["sequence"]], [newer["epoch"], newer["sequence"]]]
        result.update(status="passed", queryVisible=True, resultVisible=True, postActionObservationId=post.get("id"))
    except Exception as error:
        result["error"] = str(error) or type(error).__name__
        try:
            barrier.abort()
        except Exception:
            pass
    finally:
        if run.lease:
            with run.lock:
                run.release_cursor = run.previous
            try:
                SMOKE.call("release", {"device": phone["udid"], "lease": run.lease})
                run.lease = None
                result["leaseReleased"] = True
            except Exception as error:
                result["cleanupError"] = str(error)
    return result


def mcp_read(proc, ident, timeout=30):
    end = time.monotonic() + timeout
    while time.monotonic() < end:
        if not select.select([proc.stdout], [], [], end - time.monotonic())[0]: break
        response = json.loads(proc.stdout.readline())
        if response.get("id") == ident:
            return response
    raise RuntimeError(f"MCP response {ident} timed out")


def check_mcp(device):
    proc = subprocess.Popen([CLI, "mcp"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True, bufsize=1)
    try:
        init = {"jsonrpc":"2.0", "id":1, "method":"initialize", "params":{"protocolVersion":"2025-06-18", "capabilities":{}, "clientInfo":{"name":"vision-check", "version":"1"}}}
        proc.stdin.write(json.dumps(init) + "\n"); proc.stdin.flush(); mcp_read(proc, 1)
        proc.stdin.write('{"jsonrpc":"2.0","method":"notifications/initialized"}\n'); proc.stdin.flush()
        tools = []
        for ident, method in ((2, "tools/list"), (3, "tools/call")):
            params = {} if ident == 2 else {"name":"phone_frame", "arguments":{"device":device}}
            req = {"jsonrpc":"2.0", "id":ident, "method":method, "params":params}
            proc.stdin.write(json.dumps(req) + "\n"); proc.stdin.flush()
            result = mcp_read(proc, ident).get("result", {})
            if ident == 2:
                tools = result.get("tools", [])
                require(any(t.get("name") == "phone_frame" for t in tools), "MCP tools/list lacks phone_frame")
        require(not result.get("isError"), f"MCP phone_frame failed: {result}")
        texts = [c for c in result.get("content", []) if c.get("type") == "text"]
        images = [c for c in result.get("content", []) if c.get("type") == "image"]
        require(len(texts) == len(images) == 1, "MCP phone_frame needs metadata text and one image")
        text = texts[0].get("text", ""); metadata = json.loads(text)
        if metadata.get("ok") is True:
            metadata = metadata.get("result")
        status, message, frame = parse_snapshot(metadata, device, allow_missing=True)
        require(status == "live" and frame, f"MCP frame status={status!r}: {message}")
        encoded = images[0].get("data", "")
        require(encoded not in text and not re.search(r"[A-Za-z0-9+/]{256,}={0,2}", text), "MCP text contains image base64")
        require(images[0].get("mimeType") == "image/jpeg", "MCP image is not JPEG")
        jpeg = base64.b64decode(encoded, validate=True)
        require(jpeg_dimensions(jpeg) == (frame["width"], frame["height"]), "MCP JPEG dimensions differ")
        return {"status":"passed", "device":device, "toolCount":len(tools), "jpegBytes":len(jpeg),
                "cursor":{"epoch":frame["epoch"], "sequence":frame["sequence"]}, "metadataContainsBase64":False}
    finally:
        try:
            proc.stdin.close(); proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill(); proc.wait(timeout=5)
        except Exception:
            pass
        proc.stdout.close()


def stats(values):
    if not values:
        return {"sampleCount": 0}
    ordered = sorted(values)
    return {"sampleCount":len(values), "min":round(ordered[0], 2), "median":round(statistics.median(values), 2),
            "p95":round(ordered[int((len(ordered)-1)*.95)], 2), "max":round(ordered[-1], 2)}


def ps_sample(pids):
    stamp = datetime.now().astimezone().isoformat()
    try:
        result = subprocess.run(["/bin/ps", "-axo", "pid=,ppid=,rss=,%cpu=,comm="], capture_output=True, text=True, timeout=5)
        processes = []
        for line in result.stdout.splitlines():
            cols = line.strip().split(None, 4)
            if len(cols) != 5: continue
            try: row = {"pid":int(cols[0]), "ppid":int(cols[1]), "rssKiB":int(cols[2]), "cpuPercent":float(cols[3]), "command":cols[4]}
            except ValueError: continue
            if row["pid"] in pids or "agent-phone" in cols[4].lower() or "agentphone" in cols[4].lower(): processes.append(row)
        return {"sampledAt":stamp, "processes":processes}
    except Exception as error:
        return {"sampledAt":stamp, "error":str(error)}


def sustained_sample(runs, seconds, report):
    pids = [r.process.pid for r in runs if r.process and r.process.poll() is None]
    report["resourceSamples"].append(ps_sample(pids))
    start, end, next_ps = time.monotonic(), time.monotonic() + seconds, time.monotonic() + 2
    while time.monotonic() < end:
        for run in runs:
            with run.lock:
                require(not run.error, f"{run.phone['name']}: {run.error}")
                frame = run.latest
            require(frame is not None, f"{run.phone['name']} has no frame")
            age = (datetime.now(timezone.utc) - frame["capturedTime"]).total_seconds()
            require(age <= 5, f"{run.phone['name']} capture age {age:.2f}s exceeds 5s")
        now = time.monotonic()
        if now >= next_ps:
            report["resourceSamples"].append(ps_sample(pids)); next_ps = now + 2
        time.sleep(min(.25, end - now))
    report["sustainedSampling"] = {"requestedSeconds":seconds, "actualSeconds":round(time.monotonic() - start, 2)}
    report["resourceSamples"].append(ps_sample(pids))


def stop_watch(run):
    proc = run.process
    if proc is None:
        return
    with run.lock:
        run.expected_stop = True
    if proc.poll() is None:
        try:
            proc.terminate(); proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            proc.kill(); proc.wait(timeout=3)
        except OSError:
            pass
    if run.reader: run.reader.join(timeout=3)
    if run.stderr_reader: run.stderr_reader.join(timeout=1)
    proc.stdout.close(); proc.stderr.close()


def save_frames(runs, output):
    folder = output.with_name(output.stem + "-frames"); folder.mkdir(parents=True, exist_ok=True)
    for run in runs:
        with run.lock: frame = run.latest
        if frame:
            path = folder / (re.sub(r"[^A-Za-z0-9_-]", "_", run.phone["udid"]) + ".jpg")
            path.write_bytes(frame["jpeg"]); run.jpeg_path = str(path.resolve())
            run.checks["finalJpegBytes"] = len(frame["jpeg"])
            run.checks["finalJpegSha256"] = hashlib.sha256(frame["jpeg"]).hexdigest()


def device_report(run):
    with run.lock:
        frame = run.latest
        return {"name":run.phone["name"], "device":run.phone["udid"], "query":run.query, "status":run.status,
                "frameCount":run.frames, "duplicateCursorFrames":run.duplicates, "epochChanges":run.epoch_changes,
                "statusCounts":dict(run.status_counts), "cadenceMs":stats(list(run.cadence)), "captureAgeMs":stats(list(run.ages)),
                "jpegBytes":stats(list(run.jpeg_sizes)), "latestCursor":{"epoch":frame["epoch"], "sequence":frame["sequence"]} if frame else None,
                "latestCapturedAt":frame["capturedAt"] if frame else None,
                "nativeAX":{"queryVisible":SMOKE.has_query(run.final_observation, run.query), "resultVisible":SMOKE.has_result(run.final_observation, run.query)} if run.final_observation else None,
                "checks":run.checks, "finalJpeg":run.jpeg_path}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--seconds", type=int, default=30, help="sustained sample duration after lease release")
    parser.add_argument("--output", type=Path)
    options = parser.parse_args()
    output = (options.output or ROOT / ".cache" / "live-vision-validation" /
              ("live-vision-" + datetime.now().strftime("%Y%m%d-%H%M%S") + ".json")).expanduser().resolve()
    report = {"schemaVersion":1, "status":"failed", "manifest":str(MANIFEST.resolve()), "queries":QUERIES,
        "startedAt":datetime.now().astimezone().isoformat(), "apiAssumptions":{
            "watch":"agent-phone watch --device <managed UDID> emits NDJSON snapshots",
            "snapshot":"state plus frame {device,capturedAt,width,height,jpeg,cursor:{epoch,sequence}}",
            "rpc":"frame and wait_frame accept device without lease", "mcp":"phone_frame returns metadata text and JPEG image",
            "cleanup":"release lease while persistent watch remains active"}, "checks":{}, "devices":[], "resourceSamples":[]}
    runs, start = [], time.monotonic()
    try:
        require(1 <= options.seconds <= 3600, "--seconds must be 1..3600")
        manifest = json.loads(MANIFEST.read_text())
        fleet = SMOKE.call("fleet", {})
        pairs = SMOKE.select_phones(manifest, fleet, QUERIES)
        require(len(manifest.get("variants", [])) == len(pairs) == 4, "Manifest must select exactly four managed booted phones")
        runs = [DeviceRun(p, q) for p, q in pairs]
        report["checks"]["exactFourManifestManagedBootedPhones"] = True
        report["initialFleet"] = [{"name":p["name"], "device":p["udid"], "state":p["state"],
            "owner":p.get("owner"), "leaseExpires":p.get("leaseExpires"), "busy":p.get("busy", False)}
            for p in fleet["phones"] if p.get("managed")]
        reject("frame", {"device":"LIVE-VISION-UNKNOWN-" + uuid.uuid4().hex}, "unmanaged_device")
        report["checks"]["unknownManagedIdRejected"] = True
        for run in runs: start_watch(run)
        wait_for(lambda: all(r.latest is not None and r.status == "live" for r in runs), runs, 35, "four live frames")
        report["checks"]["watchPids"] = [r.process.pid for r in runs]

        for run in runs:
            first = frame_call("frame", run.phone["udid"])
            newer = frame_call("wait_frame", run.phone["udid"], first)
            run.checks["unleasedFrame"] = {"cursor":[first["epoch"], first["sequence"]], "dimensions":[first["width"], first["height"]], "jpegBytes":len(first["jpeg"])}
            run.checks["unleasedWaitFrameAdvanced"] = [newer["epoch"], newer["sequence"]]
        report["checks"]["unleasedFrameAndWaitFrame"] = True
        report["mcp"] = check_mcp(runs[0].phone["udid"])
        barrier = threading.Barrier(4)
        with ThreadPoolExecutor(max_workers=4) as pool:
            report["searches"] = [f.result() for f in [pool.submit(search_worker, r, barrier) for r in runs]]
        require(all(x.get("status") == "passed" and x.get("leaseReleased") for x in report["searches"]), "A search or lease release failed")
        overlap = (min(r.search_finish for r in runs) - max(r.search_start for r in runs)) * 1000
        require(overlap > 0, "Wikipedia search intervals did not overlap")
        report["checks"]["searchOverlapMs"] = round(overlap, 2)
        report["checks"]["fourDistinctNativeAXQueriesAndResults"] = True
        for run in runs:
            epoch, seq = run.release_cursor
            wait_for(lambda r=run, e=epoch, s=seq: r.latest and (r.latest["epoch"] != e or r.latest["sequence"] > s), [run], 20, "frame after lease release")
            run.checks["streamAdvancedAfterLeaseRelease"] = True
        report["checks"]["streamsAdvanceAfterLeaseRelease"] = True
        sustained_sample(runs, options.seconds, report)
        wait_for(lambda: all(r.status == "live" for r in runs), runs, 8, "live feeds after sample")
        report["checks"]["fourFeedsStayedFresh"] = True
        report["status"] = "passed"
    except Exception as error:
        report["error"] = str(error) or type(error).__name__
    finally:
        for run in runs:
            if run.lease:
                try: SMOKE.call("release", {"device":run.phone["udid"], "lease":run.lease}); run.lease = None
                except Exception as error: report.setdefault("cleanupErrors", []).append(f"{run.phone['udid']} lease: {error}"); report["status"] = "failed"
        for run in runs:
            try: stop_watch(run)
            except Exception as error: report.setdefault("cleanupErrors", []).append(f"{run.phone['udid']} watch: {error}"); report["status"] = "failed"
        try: save_frames(runs, output)
        except Exception as error: report.setdefault("cleanupErrors", []).append(f"final JPEG save: {error}"); report["status"] = "failed"
        report["devices"] = [device_report(r) for r in runs]
        report.update(completedAt=datetime.now().astimezone().isoformat(), elapsedSeconds=round(time.monotonic()-start, 2), output=str(output))
        try:
            output.parent.mkdir(parents=True, exist_ok=True)
            output.write_text(json.dumps(report, indent=2, default=str) + "\n")
        except Exception as error:
            print(f"Could not write partial report {output}: {error}", file=sys.stderr)
    print(f"Live vision status: {report['status']}\nReport: {output}")
    if report["status"] != "passed":
        print(report.get("error", "Live vision validation failed"), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
