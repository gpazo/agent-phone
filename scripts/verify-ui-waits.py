#!/usr/bin/env python3
"""Verify UI wait evidence and deadlines on one explicitly managed phone."""

import argparse
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import select
import subprocess
import sys
import time
import uuid

ROOT = Path(__file__).resolve().parent.parent
CLI = ROOT / "build/agent-phone"


def require(value, message):
    if not value:
        raise RuntimeError(message)


def raw(method, args):
    proc = subprocess.run([str(CLI), "call", method, json.dumps(args)], capture_output=True,
                          text=True, timeout=180)
    require(proc.stdout, proc.stderr.strip() or f"{method} produced no response")
    reply = json.loads(proc.stdout)
    require((proc.returncode == 0) == reply.get("ok", False), f"{method} exit/envelope mismatch")
    return reply


def call(method, args):
    reply = raw(method, args)
    require(reply.get("ok"), f"{method}: {reply.get('error')}")
    return reply["result"]


def condition(evidence="tree", **where):
    return {"where": where, "evidence": evidence}


def wait(args, conditions, **options):
    return call("wait", dict(args, conditions=conditions, **options))


def element(obs, **where):
    matches = [e for e in obs["elements"] if all(e.get(k) == v for k, v in where.items())]
    require(len(matches) == 1, f"Expected one element {where}, found {len(matches)}")
    return matches[0]


def act(args, obs, label=None, text=None):
    target = element(obs, role="textfield") if text is not None else element(obs, role="button", label=label)
    action = "type" if text is not None else "press"
    require(target.get("actionable") and action in target.get("actions", []), "Target is not actionable")
    params = dict(args, observation=obs["id"], ref=target["ref"], action=action)
    if text is not None:
        params["text"] = text
    reply = raw("act", params)
    if not reply.get("ok"):
        require(text is not None and "Text replacement not acknowledged" in reply.get("error", {}).get("message", ""),
                f"Input failed: {reply.get('error')}")
    return reply


class MCP:
    def __init__(self):
        self.proc = subprocess.Popen([str(CLI), "mcp"], stdin=subprocess.PIPE, stdout=subprocess.PIPE,
                                     stderr=subprocess.DEVNULL)
        self.buffer = b""
        self.send(1, "initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                  "clientInfo": {"name": "ui-wait-check", "version": "1"}})
        require("result" in self.read(1), "MCP initialization failed")
        self.send(None, "notifications/initialized", {})

    def send(self, ident, method, params):
        request = {"jsonrpc": "2.0", "method": method, "params": params}
        if ident is not None:
            request["id"] = ident
        self.proc.stdin.write(json.dumps(request).encode() + b"\n")
        self.proc.stdin.flush()

    def read(self, ident, seconds=15):
        deadline = time.monotonic() + seconds
        while time.monotonic() < deadline:
            if b"\n" in self.buffer:
                line, self.buffer = self.buffer.split(b"\n", 1)
                response = json.loads(line)
                require(response.get("id") == ident, f"Unexpected MCP response {response.get('id')}")
                return response
            require(select.select([self.proc.stdout], [], [], max(0, deadline-time.monotonic()))[0],
                    f"MCP {ident} timed out")
            chunk = os.read(self.proc.stdout.fileno(), 65536)
            require(chunk, "MCP closed before its response")
            self.buffer += chunk
        raise RuntimeError(f"MCP {ident} timed out")

    def tool(self, ident, args):
        self.send(ident, "tools/call", {"name": "phone_wait", "arguments": args})
        return self.read(ident)["result"]

    def close(self):
        self.proc.stdin.close()
        try:
            self.proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            self.proc.kill()
            self.proc.wait(timeout=5)
        self.proc.stdout.close()


def metadata(reply):
    texts = [c["text"] for c in reply.get("content", []) if c.get("type") == "text"]
    require(len(texts) == 1, "MCP response needs one JSON text block")
    return json.loads(texts[0])


def check_mcp(args):
    mcp = MCP()
    try:
        mcp.send(2, "tools/list", {})
        tools = mcp.read(2)["result"]["tools"]
        require(any(t["name"] == "phone_wait" for t in tools), "phone_wait missing from tools/list")
        ready = mcp.tool(3, dict(args, conditions=[condition(label="Ready", role="text")], screenshot=True))
        require(not ready.get("isError"), f"MCP match failed: {metadata(ready)}")
        require(any(c.get("type") == "image" for c in ready["content"]), "MCP wait omitted screenshot image")
        absent = dict(args, conditions=[condition(label="Never present")], timeoutMs=1200)
        timeout = mcp.tool(4, absent)
        require(timeout.get("isError") and metadata(timeout).get("error", {}).get("code") == "wait_timeout",
                "MCP timeout did not fail with wait_timeout")
        mcp.send("cancel-me", "tools/call", {"name": "phone_wait", "arguments": dict(absent, timeoutMs=30000)})
        time.sleep(.3)
        mcp.send(None, "notifications/cancelled", {"requestId": "cancel-me", "reason": "live verification"})
        cancelled = mcp.read("cancel-me")["result"]
        require(cancelled.get("isError") and metadata(cancelled).get("error", {}).get("code") == "cancelled",
                "MCP cancellation did not stop its wait")
        return {"toolCount": len(tools), "matchWithImage": True, "timeoutFailed": True, "cancelled": True}
    finally:
        mcp.close()


def fixture_checks(args, report):
    covered = condition("native-hit", identifier="covered-evidence", label="Covered evidence")
    tree = wait(args, [condition(identifier="covered-evidence")], screenshot=True)
    native = wait(args, [covered], screenshot=True)
    observed = native["observation"]
    hidden = element(observed, identifier="covered-evidence")
    require(hidden.get("nativeHitAt") and not hidden["actionable"] and not hidden.get("actions"),
            "Passive native hit gained input authority or omitted evidence")
    reject = raw("act", dict(args, observation=observed["id"], ref=hidden["ref"], action="press"))
    require(not reject.get("ok"), "Passive label accepted input")
    for invalid in [dict(conditions=[condition("visible", label="Covered evidence")]),
                    dict(conditions=[]), dict(conditions=[covered], timeoutMs=0),
                    dict(conditions=[{"where": {"unknown": "x"}, "evidence": "tree"}])]:
        require(not raw("wait", dict(args, **invalid)).get("ok"), f"Invalid wait accepted: {invalid}")
    report["coveredEvidence"] = {"tree": tree, "nativeHit": native, "passiveInputRejected": True,
                                 "visualVisibility": "unverified; inspect screenshot for opaque cover"}
    initial = wait(args, [condition("native-hit", label="Start delayed result", action="press")])["observation"]
    before_tick = element(initial, identifier="unrelated-timer")["label"]
    act(args, initial, label="Start delayed result")
    early = raw("wait", dict(args, conditions=[condition(label="Ready", identifier="result-status")], timeoutMs=100))
    require(not early.get("ok") and early.get("error", {}).get("code") == "wait_timeout", "Short wait accepted late readiness")
    ready = wait(args, [condition(label="Ready", identifier="result-status"),
                        condition(label="Starts 1", identifier="start-count")], timeoutMs=8000)
    require(element(ready["observation"], identifier="unrelated-timer")["label"] != before_tick,
            "Unrelated timer did not change during readiness check")
    missing = raw("wait", dict(args, conditions=[condition(label="Never present")], timeoutMs=2000))
    require(not missing.get("ok") and missing.get("error", {}).get("code") == "wait_timeout", "Absent label did not time out")
    diagnostic = missing.get("result", {}).get("lastObservation")
    require(diagnostic and not any(e["actionable"] for e in diagnostic["elements"]), "Timeout lacks passive last observation")
    report["delayedReadiness"] = ready
    report["shortDeadline"] = early
    report["timeoutDiagnostics"] = missing
    report["mcp"] = check_mcp(args)


def wikipedia_checks(args):
    call("launch", dict(args, bundleId="org.wikimedia.wikipedia"))
    hit = lambda **where: condition("native-hit", **where)
    obs = wait(args, [hit(label="Skip", action="press"), hit(label="Search", action="press"),
                      hit(role="textfield", action="type")], mode="any")["observation"]
    if any(e["label"] == "Skip" and e.get("actionable") for e in obs["elements"]):
        act(args, obs, label="Skip")
        obs = wait(args, [hit(label="Search", action="press"), hit(role="textfield", action="type")], mode="any")["observation"]
    if not any(e["role"] == "textfield" and e.get("actionable") for e in obs["elements"]):
        act(args, obs, label="Search")
        obs = wait(args, [hit(role="textfield", action="type")])["observation"]
    act(args, obs, text="Saturn")
    result = wait(args, [condition(role="textfield", value="Saturn"), condition(role="text", label="Saturn")], screenshot=True)
    require(result["observation"].get("screenshotAt"), "Wikipedia screenshot has no separate timestamp")
    return result


def matrix_check(phone, catalog, output, report):
    prefix, separator, variant = phone["name"].rpartition(" / ")
    require(separator, "Matrix verification requires a managed name of the form Matrix / Variant")
    live = [(runtime, device) for runtime, devices in catalog["devices"].items()
            for device in devices if device["udid"] == phone["udid"]]
    require(len(live) == 1, "Selected managed UDID is absent or duplicated in live catalog")
    runtime, device = live[0]
    manifest = {"name": prefix, "runtime": runtime, "deviceType": device["deviceTypeIdentifier"],
                "concurrency": 1, "keepRunning": True, "build": {
                    "project": str(ROOT / "tests/fixtures/WaitFixture/WaitFixture.xcodeproj"),
                    "scheme": "WaitFixture", "derivedData": str(ROOT / ".cache/fixture-builds/wait-fixture-debug")},
                "variants": [{"name": variant, "steps": [
                    {"action": "assert", "expect": "Covered evidence"},
                    {"action": "press", "label": "Start delayed result"},
                    {"action": "wait", "conditions": [condition(label="Ready", identifier="result-status")], "timeoutMs": 8000}]}]}
    path = output.with_name(output.stem + "-matrix.json")
    path.write_text(json.dumps(manifest, indent=2) + "\n")
    reply = raw("matrix", {"matrix": str(path)})
    report["matrix"] = reply.get("result")
    require(reply.get("ok"), f"matrix: {reply.get('error')}")
    result = reply["result"]
    require(result["status"] == "passed" and len(result["results"]) == 1 and
            result["results"][0]["device"] == phone["udid"], "Matrix did not pass on the selected exact managed phone")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--device", required=True, help="Exact existing managed simulator UDID")
    parser.add_argument("--shutdown", action="store_true", help="Shut down the selected phone at the end even if initially booted")
    parser.add_argument("--output", type=Path, default=ROOT / ".cache/ui-waits-validation.json")
    options = parser.parse_args()
    output = options.output.expanduser().resolve()
    output.parent.mkdir(parents=True, exist_ok=True)
    report = {"status": "failed", "device": options.device, "startedAt": datetime.now(timezone.utc).isoformat()}
    args, phone = None, None
    try:
        fleet = call("fleet", {})
        matches = [p for p in fleet["phones"] if p["udid"] == options.device and p.get("managed")]
        require(len(matches) == 1 and not matches[0].get("owner") and not matches[0].get("busy"), "Select an unowned managed phone")
        phone = matches[0]
        require(sum(p.get("managed") and p["name"] == phone["name"] for p in fleet["phones"]) == 1,
                "Managed target name is ambiguous")
        report["initialState"] = phone["state"]
        catalog = json.loads(subprocess.check_output(["xcrun", "simctl", "list", "devices", "--json"]))
        matrix_check(phone, catalog, output, report)
        token = call("acquire", {"device": options.device, "owner": "ui-waits:" + uuid.uuid4().hex[:12]})["token"]
        args = {"device": options.device, "lease": token}
        call("launch", dict(args, bundleId="local.agentphone.waitfixture"))
        fixture_checks(args, report)
        report["wikipedia"] = wikipedia_checks(args)
        report["status"] = "passed"
    except Exception as error:
        report["error"] = str(error)
    finally:
        if phone and (options.shutdown or phone["state"] == "Shutdown"):
            try:
                if args is None:
                    token = call("acquire", {"device": options.device, "owner": "ui-waits-cleanup"})["token"]
                    args = {"device": options.device, "lease": token}
                call("shutdown", args)
                report["shutdown"] = True
            except Exception as error:
                report["status"] = "failed"
                report.setdefault("cleanupErrors", []).append(str(error))
        if args:
            try:
                call("release", args)
                report["leaseReleased"] = True
            except Exception as error:
                report["status"] = "failed"
                report.setdefault("cleanupErrors", []).append(str(error))
        report["completedAt"] = datetime.now(timezone.utc).isoformat()
        output.write_text(json.dumps(report, indent=2) + "\n")
    print(f"UI wait validation {report['status']}. Report: {output}")
    if report["status"] != "passed":
        print(report.get("error", report.get("cleanupErrors")), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
