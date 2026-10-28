import json
import os
from pathlib import Path
import subprocess


root = Path(__file__).resolve().parents[3]
data = Path(os.environ.get("AGENT_PHONE_HOME", Path.home() / "Library/Application Support/AgentPhone"))
if not (data / "control.sock").is_socket():
    raise SystemExit("This check requires an already-running agent-phone service.")

process = subprocess.Popen(
    [str(root / "build/agent-phone"), "mcp"],
    stdin=subprocess.PIPE,
    stdout=subprocess.PIPE,
    stderr=subprocess.PIPE,
    text=True,
)


def call(identifier, method, params):
    process.stdin.write(json.dumps({"jsonrpc": "2.0", "id": identifier, "method": method, "params": params}) + "\n")
    process.stdin.flush()
    reply = json.loads(process.stdout.readline())
    assert reply["id"] == identifier, reply
    return reply


def sockets():
    result = subprocess.run(
        ["/usr/sbin/lsof", "-nP", "-a", "-p", str(process.pid), "-U", "-Ffn"],
        capture_output=True, text=True, check=False,
    )
    if result.returncode not in (0, 1):
        raise RuntimeError(result.stderr)
    return [line for line in result.stdout.splitlines() if line.startswith("f")]


try:
    call(1, "initialize", {"protocolVersion": "2025-06-18"})
    before = sockets()
    for identifier in range(2, 14):
        reply = call(identifier, "tools/call", {
            "name": "phone_observe",
            "arguments": {"device": "fresh-eyes-nonexistent-device", "lease": "invalid"},
        })
        body = json.loads(reply["result"]["content"][0]["text"])
        assert body["error"]["code"] == "unmanaged_device", body
    after = sockets()
    print(json.dumps({"calls": 12, "unix_fds_before": before, "unix_fds_after": after}, indent=2))
finally:
    process.stdin.close()
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired:
        process.kill()
        process.wait()
