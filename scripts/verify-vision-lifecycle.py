#!/usr/bin/env python3
"""Exercise capture failure, backpressure, and one leased demo-phone reboot."""

import argparse
from collections import deque
from datetime import datetime
import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import threading
import time

ROOT = Path(__file__).resolve().parent.parent
CLI = str(ROOT / "build/agent-phone")
HELPER = str(ROOT / "build/AgentPhoneVision")


def call(method, params):
    reply = json.loads(subprocess.run([CLI, "call", method, json.dumps(params)],
        capture_output=True, text=True, timeout=180).stdout)
    if not reply.get("ok"):
        raise RuntimeError(str(reply))
    return reply["result"]


def wait_for(predicate, seconds, description):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        if predicate():
            return
        time.sleep(.1)
    raise RuntimeError("Timed out: " + description)


def helpers():
    result = {}
    for line in subprocess.check_output(["ps", "-axo", "pid=,command="], text=True).splitlines():
        parts = line.strip().split()
        if len(parts) == 3 and parts[1] == HELPER:
            if parts[2] in result:
                raise RuntimeError("Duplicate producer: " + parts[2])
            result[parts[2]] = int(parts[0])
    return result


class Watch:
    def __init__(self, device):
        self.device = device
        self.latest = None
        self.events = deque(maxlen=128)
        self.error = None
        self.proc = subprocess.Popen([CLI, "watch", "--device", device],
            stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        self.thread = threading.Thread(target=self.read, daemon=True)
        self.thread.start()

    def read(self):
        try:
            for line in iter(lambda: self.proc.stdout.readline(3 * 1024 * 1024 + 1), b""):
                record = json.loads(line)
                frame = record.get("frame")
                if frame:
                    assert frame["device"] == self.device
                    frame.pop("jpeg", None)
                self.latest = record
                self.events.append({"time": time.monotonic(), "state": record["state"],
                    "cursor": frame.get("cursor") if frame else None})
        except Exception as error:
            self.error = str(error)

    def cursor(self):
        if self.error:
            raise RuntimeError(self.error)
        return (self.latest or {}).get("frame", {}).get("cursor")

    def live(self):
        return self.cursor() and self.latest["state"] == "live"

    def close(self):
        if self.proc.poll() is None:
            self.proc.terminate()
        try:
            self.proc.wait(timeout=3)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            self.proc.wait(timeout=3)
        self.thread.join(timeout=3)
        self.proc.stdout.close()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--device", required=True, help="Managed Wikipedia demo UDID to reboot")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    report = {"status": "failed", "startedAt": datetime.now().astimezone().isoformat(), "checks": {}}
    watches, lease, slow, stopped_pid = [], None, None, None
    try:
        fleet = call("fleet", {})
        phones = [p for p in fleet["phones"] if p.get("managed") and p["state"] == "Booted"]
        assert len(phones) == 4 and any(p["udid"] == args.device for p in phones)
        assert not any(p.get("owner") or p.get("busy") for p in phones)
        watches = [Watch(p["udid"]) for p in phones]
        wait_for(lambda: all(w.live() for w in watches), 15, "four live streams")
        target = next(w for w in watches if w.device == args.device)
        others = [w for w in watches if w is not target]
        assert len(helpers()) == 4
        report["checks"]["sharedProducerCount"] = 4

        for label, sig in [("crash", signal.SIGKILL), ("stall", signal.SIGSTOP)]:
            before = {w.device: w.cursor() for w in watches}
            pid = helpers()[args.device]
            started = time.monotonic()
            os.kill(pid, sig)
            stopped_pid = pid if sig == signal.SIGSTOP else None
            wait_for(lambda: target.live() and target.cursor()["epoch"] != before[args.device]["epoch"], 15, label + " recovery")
            assert pid not in helpers().values()
            stopped_pid = None
            for w in others:
                assert w.live() and w.cursor()["epoch"] == before[w.device]["epoch"]
                assert w.cursor()["sequence"] > before[w.device]["sequence"]
            report["checks"][label] = {"recoveredMs": round((time.monotonic()-started)*1000),
                "newEpoch": target.cursor()["epoch"], "states": sorted({e["state"] for e in target.events if e["time"] >= started}),
                "otherThreeEpochsUnchanged": True}

        before = {w.device: w.cursor() for w in watches}
        with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as conn:
            conn.setsockopt(socket.SOL_SOCKET, socket.SO_RCVBUF, 4096)
            conn.connect(str(Path(fleet["dataDirectory"]) / "control.sock"))
            conn.sendall(("GET /vision/stream?device=" + args.device + " HTTP/1.1\r\nHost: localhost\r\n\r\n").encode())
            time.sleep(7)
            conn.settimeout(3)
            total = 0
            while True:
                data = conn.recv(65536)
                if not data:
                    break
                total += len(data)
                assert total < 4 * 1024 * 1024
        for w in watches:
            assert w.live() and w.cursor()["sequence"] > before[w.device]["sequence"]
        report["checks"]["slowHTTPReader"] = {"serverClosedConnection": True, "bufferedBytes": total, "healthyReadersContinued": True}
        slow = subprocess.Popen([CLI, "watch", "--device", args.device], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        time.sleep(4)
        started = time.monotonic()
        slow.terminate()
        slow.wait(timeout=3)
        slow.stdout.close()
        slow = None
        report["checks"]["blockedStdoutCancellationMs"] = round((time.monotonic()-started)*1000)
        assert len(helpers()) == 4

        lease = call("acquire", {"device": args.device, "owner": "vision-lifecycle-validation"})["token"]
        control = {"device": args.device, "lease": lease}
        old = target.cursor()
        call("shutdown", control)
        wait_for(lambda: target.proc.poll() is not None, 5, "old shutdown stream closes")
        unavailable = call("wait_frame", {"device": args.device, "afterEpoch": old["epoch"], "afterSequence": old["sequence"], "timeoutMs": 100})
        assert unavailable.get("timedOut") and not unavailable.get("frame")
        target.close()
        watches.remove(target)
        call("boot", control)
        call("launch", dict(control, bundleId="org.wikimedia.wikipedia"))
        target = Watch(args.device)
        watches.append(target)
        wait_for(lambda: target.live() and target.cursor()["epoch"] != old["epoch"], 30, "rebooted live screen")
        assert all(w.live() for w in watches)
        report["checks"]["shutdownAndBoot"] = {"oldStreamClosed": True, "offlineWaitHasNoImage": True, "newEpoch": target.cursor()["epoch"]}
        report["status"] = "passed"
    except Exception as error:
        report["error"] = str(error) or type(error).__name__
    finally:
        if stopped_pid and stopped_pid in helpers().values():
            os.kill(stopped_pid, signal.SIGCONT)
        if slow:
            slow.kill(); slow.wait(timeout=3); slow.stdout.close()
        if lease:
            try:
                call("release", {"device": args.device, "lease": lease})
            except Exception as error:
                report["status"], report["cleanupError"] = "failed", str(error)
        for watch in watches:
            watch.close()
        report["completedAt"] = datetime.now().astimezone().isoformat()
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(report, indent=2) + "\n")
        print(json.dumps(report, indent=2))
    return 0 if report["status"] == "passed" else 1


if __name__ == "__main__":
    raise SystemExit(main())
