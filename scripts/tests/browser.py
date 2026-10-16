#!/usr/bin/env python3
"""Real persistent CUA plugin acceptance against a disposable page; no model API."""

import argparse
import hashlib
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import threading
import uuid
from urllib.request import Request, urlopen

ROOT = Path(__file__).resolve().parents[min(2, len(Path(__file__).resolve().parents) - 1)]
HTML = b"""<!doctype html><title>Pot browser fixture</title>
<label>Name <input aria-label="Name"></label><button onclick="document.querySelector('output').textContent='Saved '+document.querySelector('input').value">Save</button><output></output>
<canvas width="160" height="80"></canvas><script>let c=document.querySelector('canvas').getContext('2d');c.fillStyle='red';c.fillRect(0,0,160,80)</script>"""


class Page(BaseHTTPRequestHandler):
    def do_GET(self):
        self.send_response(200)
        self.send_header("Content-Type", "text/html")
        self.end_headers()
        self.wfile.write(HTML)

    def log_message(self, *args):
        pass


def package(root):
    manifest = json.loads((root / "plugin.json").read_text())
    files = {name: (root / name).read_text() for name in manifest["files"]}
    encoded = json.dumps(
        [manifest, files], sort_keys=True, separators=(",", ":"), ensure_ascii=False
    )
    return {
        "manifest": manifest,
        "files": files,
        "digest": hashlib.sha256(encoded.encode()).hexdigest(),
    }


def state(reply):
    for item in reversed(reply["content"]):
        if item["type"] == "text":
            try:
                value = json.loads(item["text"])
                if "elements" in value:
                    return value
            except json.JSONDecodeError:
                pass
    raise AssertionError(reply)


def run(executable, broker=False, runner=None, plugin_root=None):
    bundle = package(plugin_root or ROOT / "plugins/computer-use")
    server = ThreadingHTTPServer(("127.0.0.1", 0), Page)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    url = f"http://127.0.0.1:{server.server_port}/"
    results = {}
    policy = {
        "executable": "/bin/bash",
        "allow_login_shell": True,
        "shell_snapshot": True,
        "session_idle_timeout_ms": 1800000,
        "default_timeout_ms": 120000,
        "max_timeout_ms": 600000,
        "default_output_bytes": 16384,
        "max_output_bytes": 1000000,
        "max_sessions": 8,
        "default_yield_ms": 1000,
        "max_yield_ms": 10000,
    }

    def post(path, body):
        request = Request(
            "http://127.0.0.1:17701/" + path,
            data=json.dumps(body).encode(),
            headers={
                "Content-Type": "application/json",
                "Authorization": "Bearer " + os.environ["POT_BROWSER_TEST_TOKEN"],
            },
        )
        with urlopen(request, timeout=40) as response:
            return json.load(response)

    try:
        for vision in (False, True):
            scope = str(uuid.uuid4())
            process = (
                None
                if broker
                else subprocess.Popen(
                    [str(runner or ROOT / ".local/go/bin/pot-tool-executor")],
                    env={**os.environ, "POT_BROWSER_EXECUTABLE": executable},
                    stdin=subprocess.PIPE,
                    stdout=subprocess.PIPE,
                    text=True,
                )
            )
            session = None

            def exchange(input_args):
                envelope = {
                    "plugin": bundle,
                    "tool": "cua_repl",
                    "input": input_args,
                    "mode": "mixed" if vision else "text",
                }
                if "session_id" in input_args:
                    envelope["session_id"] = input_args["session_id"]
                request = {
                    "id": str(uuid.uuid4()),
                    "scope": scope,
                    "operation": "plugin_call",
                    "args": envelope,
                    "cwd": "/data/workspace" if broker else "/tmp",
                    "role": "main",
                    "policy": policy,
                }
                if broker:
                    response = post("execute", request)
                    assert post("execute", request) == response, "receipt replay changed output"
                else:
                    process.stdin.write(json.dumps(request) + "\n")
                    process.stdin.flush()
                    response = json.loads(process.stdout.readline())
                return response["result"]

            def call(code="", reset=False):
                nonlocal session
                input_args = {"code": code}
                if session:
                    input_args["session_id"] = session
                if reset:
                    input_args["reset"] = True
                reply = exchange(input_args)
                assert not reply.get("isError"), reply
                session = reply.get("session_id")
                return reply

            try:
                if broker:
                    caps = post("capabilities", {"protocol_version": 1})
                    assert caps["plugins"]["runtime"] == "node", caps
                docs = call()
                assert "Computer-use API" in docs["content"][0]["text"]
                original_session = session
                rejected_reset = exchange({"reset": True})
                assert (
                    rejected_reset["isError"] and "existing session_id" in rejected_reset["error"]
                )
                reply = call(
                    f"let tab = await cua.open({json.dumps(url)}); let state = await tab.observe(); let counter = 1;"
                )
                assert state(reply)["title"] == "Pot browser fixture"
                duplicate_open = exchange(
                    {"session_id": session, "code": f"await cua.open({json.dumps(url)});"}
                )
                assert duplicate_open["isError"] and "already open" in duplicate_open["error"]
                assert duplicate_open["session_id"] == session

                assert any(c["type"] == "image" for c in reply["content"]) == vision
                if vision:
                    image = next(c for c in reply["content"] if c["type"] == "image")
                    assert (image["width"], image["height"]) == (1280, 800)
                else:
                    assert "requires visual capability" in state(reply)["limitations"]
                field = next(e for e in state(reply)["elements"] if e["role"] == "textbox")
                reply = call(
                    "state = await tab.fill("
                    + json.dumps(
                        {
                            "element": field["element"],
                            "sequence": state(reply)["sequence"],
                            "text": "Pot",
                        }
                    )
                    + ");"
                )
                button = next(
                    e
                    for e in state(reply)["elements"]
                    if e["role"] == "button" and e["name"] == "Save"
                )
                reply = call(
                    "state = await tab.click("
                    + json.dumps(
                        {"element": button["element"], "sequence": state(reply)["sequence"]}
                    )
                    + ");"
                )
                assert any("Saved Pot" in e["name"] for e in state(reply)["elements"]), reply
                assert session == original_session
                reply = call("counter++; nodeRepl.write(counter); state = await tab.observe();")
                assert any(c.get("text") == "2" for c in reply["content"])
                # A stale-sequence JS error retains the REPL, allowing safe re-observation.
                error = exchange(
                    {
                        "session_id": session,
                        "code": f"await tab.click({{element: {json.dumps(button['element'])}, sequence: 1}});",
                    }
                )
                assert error["isError"] and "latest observation" in error["error"]
                assert error["session_id"] == session
                call("state = await tab.observe();")
                coords = exchange(
                    {
                        "session_id": session,
                        "code": "state = await tab.click({x:100,y:150,sequence:state.sequence});",
                    }
                )
                assert bool(coords.get("isError")) != vision
                if broker:
                    assert post("inspect", {"id": scope})["active"]
                call("await tab.close();")
                # Closing a tab leaves JS variables alive; reset ends the whole plugin session.
                call("nodeRepl.write(counter);")
                call(reset=True)
                assert session is None
                if broker:
                    assert not post("inspect", {"id": scope})["active"]
                call(f"let tab = await cua.open({json.dumps(url)});")
                assert session != original_session
                if broker:
                    assert post("cancel", {"id": scope})["settled"]
                    assert post("inspect", {"id": scope})["ended"]
                else:
                    call(reset=True)
                results["mixed" if vision else "text"] = True
            finally:
                if process:
                    process.stdin.close()
                    process.wait(timeout=10)
                elif broker:
                    post("cancel", {"id": scope})
    finally:
        server.shutdown()
        server.server_close()
    print(json.dumps(results))


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--chromium", default="/usr/bin/chromium")
    parser.add_argument("--broker", action="store_true")
    parser.add_argument("--runner", type=Path)
    parser.add_argument("--package", type=Path)
    args = parser.parse_args()
    run(args.chromium, args.broker, args.runner, args.package)
