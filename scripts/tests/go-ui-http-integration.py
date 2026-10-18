#!/usr/bin/env python3
"""Opt-in real browser/Runtime integration; loopback only, no model credentials.

Needs pinned Go/Node/Python, cached @playwright/cli and its installed browser.
No packages, browsers, user services or external accounts are installed.
"""

import argparse
import base64
import hashlib
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import secrets
import shutil
import signal
import socket
import subprocess
import tempfile
import threading
import time
from urllib.error import HTTPError
from urllib.parse import urlencode
from urllib.request import build_opener, ProxyHandler, Request
import uuid

ROOT = Path(__file__).resolve().parents[2]


def wait_for(work, label):
    for _ in range(150):
        try:
            result = work()
            if result:
                return result
        except (OSError, subprocess.CalledProcessError):
            pass
        time.sleep(0.1)
    raise RuntimeError(f"Timed out waiting for {label}")


def stop(process):
    if process.poll() is None:
        process.send_signal(signal.SIGTERM)
        try:
            process.wait(timeout=10)
        except subprocess.TimeoutExpired:
            process.kill()
            process.wait(timeout=5)


def run(binary, base, env, cli_command):
    processes = []
    servers = []
    session = "pot-http-" + uuid.uuid4().hex[:10]
    browser_open = False
    web = None
    task_id = ""
    model_requests = []
    coordinator_key = secrets.token_urlsafe(24)
    opener = build_opener(ProxyHandler({}))
    with (base / "process.log").open("w") as log:

        def cli(*args):
            return subprocess.check_output([str(binary), *args], env=env, text=True, stderr=log)

        def browser_cli(*args):
            result = subprocess.run(
                [*cli_command, "--session", session, *args],
                env=env,
                text=True,
                capture_output=True,
                timeout=180,
            )
            # run-code echoes its input, including this disposable access token.
            # Never print the code section or put credentials in diagnostics.
            output = result.stdout.split("### Ran Playwright code", 1)[0]
            if result.returncode or "### Error" in output:
                raise RuntimeError(
                    "Playwright failed; cached CLI and installed browser are required.\n"
                    + output[:2500]
                )
            return output

        def api(method, path, body=None):
            endpoint = json.loads((base / "runtime/runtime.json").read_text())

            class Local(http.client.HTTPConnection):
                def connect(self):
                    self.sock = socket.socket(socket.AF_UNIX)
                    self.sock.settimeout(10)
                    self.sock.connect(endpoint["socket"])

            connection = Local("runtime", timeout=10)
            connection.request(
                method,
                path,
                None if body is None else json.dumps(body),
                {
                    "Authorization": "Bearer " + endpoint["token"],
                    "Content-Type": "application/json",
                },
            )
            response = connection.getresponse()
            data = response.read()
            connection.close()
            if response.status != 200:
                raise RuntimeError(f"Private {method} {path}: HTTP {response.status}")
            return json.loads(data)

        def start_web():
            nonlocal web
            web = subprocess.Popen(
                [
                    str(binary),
                    "web-ui-server",
                    "-root",
                    str(base / "browser"),
                    "-control",
                    "local",
                    "-control-root",
                    str(base / "runtime"),
                    "-origin",
                    origin,
                    "-listen",
                    f"127.0.0.1:{web_port}",
                ],
                env=env,
                stdout=log,
                stderr=log,
            )
            processes.append(web)
            wait_for(
                lambda: opener.open(origin + "/access/status", timeout=1).status == 200,
                "browser service",
            )

        class Model(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                if self.headers.get("Authorization") != "Bearer isolated-key":
                    self.send_error(401)
                    return
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                model_requests.append(body)
                if any(
                    m.get("content") == "以后叫你小白" for m in body.get("messages", [])
                ) and not any(m.get("role") == "tool" for m in body.get("messages", [])):
                    self.send_response(200)
                    self.send_header("Content-Type", "text/event-stream")
                    self.end_headers()
                    event = {
                        "choices": [
                            {
                                "index": 0,
                                "delta": {
                                    "tool_calls": [
                                        {
                                            "index": 0,
                                            "id": "rename-onboarding",
                                            "type": "function",
                                            "function": {
                                                "name": "rename_assistant",
                                                "arguments": json.dumps({"name": "小白"}),
                                            },
                                        }
                                    ]
                                },
                                "finish_reason": "tool_calls",
                            }
                        ]
                    }
                    self.wfile.write(
                        ("data: " + json.dumps(event) + "\n\ndata: [DONE]\n\n").encode()
                    )
                    return
                text = "Server stream integration completed. "
                text += (
                    f"[Download binary proof](pot-file://{task_id}/proof.bin)"
                    if task_id
                    else "Task ready."
                )
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.end_headers()
                for start in range(0, len(text), 10):
                    event = {
                        "choices": [
                            {
                                "index": 0,
                                "delta": {"content": text[start : start + 10]},
                                "finish_reason": None,
                            }
                        ]
                    }
                    self.wfile.write(("data: " + json.dumps(event) + "\n\n").encode())
                    self.wfile.flush()
                    time.sleep(0.04)
                event = {
                    "choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}],
                    "usage": {"prompt_tokens": 20, "completion_tokens": 10},
                }
                self.wfile.write(("data: " + json.dumps(event) + "\n\ndata: [DONE]\n\n").encode())
                self.wfile.flush()

        class Coordinator(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                if self.headers.get("Authorization") != "Bearer " + coordinator_key:
                    self.send_error(401)
                    return
                if self.path == "/restart-web":
                    stop(web)
                    start_web()
                elif self.path == "/rotate":
                    cli("access", "-browser-root", str(base / "browser"), "regenerate-token")
                else:
                    self.send_error(404)
                    return
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b"{}")

        try:
            model = ThreadingHTTPServer(("127.0.0.1", 0), Model)
            coordinator = ThreadingHTTPServer(("127.0.0.1", 0), Coordinator)
            servers.extend([model, coordinator])
            for server in servers:
                threading.Thread(target=server.serve_forever, daemon=True).start()
            with socket.socket() as reserve:
                reserve.bind(("127.0.0.1", 0))
                web_port = reserve.getsockname()[1]
            origin = f"http://127.0.0.1:{web_port}"
            processes.append(
                subprocess.Popen(
                    [str(binary), "serve", "-root", str(base / "runtime")],
                    env=env,
                    stdout=log,
                    stderr=log,
                )
            )
            wait_for(lambda: cli("status", "-root", str(base / "runtime")), "Runtime")
            runtime = api("GET", "/api/v1/runtime")
            api(
                "POST",
                "/api/v1/permissions",
                {
                    "action": "configure",
                    "machine_id": runtime["runtime_id"],
                    "machine_policy": {
                        "execution_access": "allow",
                        "revision": api("GET", "/api/v1/permissions")["policy"]["machines"][
                            runtime["runtime_id"]
                        ]["revision"],
                    },
                },
            )
            start_web()
            browser_open = True
            browser_cli("open", origin)
            browser_cli("snapshot")
            onboarding = {
                "origin": origin,
                "token": cli("access", "-browser-root", str(base / "browser"), "token").strip(),
                "endpoint": f"http://127.0.0.1:{model.server_port}",
                "screenshot": str(ROOT / "output/pr-images/onboarding-after.png"),
            }
            (ROOT / "output/pr-images").mkdir(parents=True, exist_ok=True)
            code = (ROOT / "scripts/tests/go-ui-onboarding-browser.js").read_text()
            browser_cli("run-code", code.replace("STATE", json.dumps(onboarding)))
            welcome = "你好，我是 Pot。\n\n你可以给我起个名字，或者告诉我你希望我如何帮你。"
            assert any(
                any(
                    m.get("role") == "assistant" and m.get("content") == welcome
                    for m in body.get("messages", [])
                )
                for body in model_requests
            ), "Welcome missing from actual HTTP model request"
            api(
                "POST",
                "/api/v1/providers",
                {
                    "request_id": "local-provider",
                    "profile_id": "local-only",
                    "name": "Local deterministic integration",
                    "model": "isolated-model",
                    "endpoint": f"http://127.0.0.1:{model.server_port}",
                    "protocol": "openai_chat",
                    "api_key": "isolated-key",
                    "max_output_tokens": 1024,
                },
            )
            assistant = api(
                "POST",
                "/api/v1/assistants",
                {"request_id": "assistant", "name": "Live browser integration"},
            )
            runtime = api("GET", "/api/v1/runtime")
            api(
                "POST",
                "/api/v1/permissions",
                {
                    "action": "configure",
                    "machine_id": runtime["runtime_id"],
                    "machine_policy": {
                        "execution_access": "allow",
                        "revision": api("GET", "/api/v1/permissions")["policy"]["machines"][
                            runtime["runtime_id"]
                        ]["revision"],
                    },
                },
            )
            task = api(
                "POST",
                "/api/v1/tasks?assistant_id=" + assistant["id"],
                {
                    "request_id": "binary-artifact",
                    "text": "Prepare isolated artifact task",
                    "machine_id": runtime["runtime_id"],
                },
            )

            def completed():
                record = api("GET", f"/api/v1/tasks/{task['id']}?assistant_id={assistant['id']}")
                if record["info"]["phase"] == "failed":
                    raise RuntimeError("Isolated artifact Task failed")
                return record["info"]["phase"] == "complete"

            wait_for(completed, "artifact Task")
            task_id = task["id"]
            artifact = base / "runtime/agent-workspace/tasks" / task_id / "proof.bin"
            artifact.parent.mkdir(parents=True, exist_ok=True)
            content = bytes(range(256)) * 4097 + b"\x00GO\xffEND"
            artifact.write_bytes(content)
            state = {
                "origin": origin,
                "coord": f"http://127.0.0.1:{coordinator.server_port}",
                "control_key": coordinator_key,
                "token": cli("access", "-browser-root", str(base / "browser"), "token").strip(),
                "assistant_id": assistant["id"],
                "download_path": str(base / "download.bin"),
            }
            browser_open = True
            browser_cli("open", origin)
            provider_state = {
                "origin": origin,
                "base": f"http://127.0.0.1:{model.server_port}",
                "connectedScreenshot": str(
                    ROOT / "output/pr-images/provider-connected-desktop-after.png"
                ),
                "mobileScreenshot": str(ROOT / "output/pr-images/provider-setup-mobile-after.png"),
                "desktopScreenshot": str(
                    ROOT / "output/pr-images/provider-setup-desktop-after.png"
                ),
            }
            # Authenticate the isolated browser before editor API probes.
            browser_cli(
                "run-code",
                "async page => { await page.request.post("
                + json.dumps(origin + "/access/token")
                + ", {headers:{Authorization:"
                + json.dumps("Bearer " + state["token"])
                + "}}); }",
            )
            code = (ROOT / "scripts/tests/go-ui-providers-browser.js").read_text()
            providers = browser_cli("run-code", code.replace("STATE", json.dumps(provider_state)))
            if '"passed":true' not in providers:
                raise RuntimeError("Provider editor browser contracts did not pass")
            (ROOT / "output/pr-images/task-progress").mkdir(parents=True, exist_ok=True)
            for script in [
                "go-ui-tasks-browser.js",
                "go-ui-task-refresh-browser.js",
                "go-ui-machines-browser.js",
                "go-ui-membership-browser.js",
            ]:
                code = (
                    (ROOT / "scripts/tests" / script)
                    .read_text()
                    .replace("http://127.0.0.1:17702", origin)
                )
                output = browser_cli(
                    "run-code", code.replace("STATE", json.dumps({"origin": origin}))
                )
                if '"passed":true' not in output:
                    raise RuntimeError(script + " did not pass")
            code = (ROOT / "scripts/tests/go-ui-refresh-browser.js").read_text()
            refresh = browser_cli("run-code", code.replace("STATE", json.dumps({"origin": origin})))
            if '"passed":true' not in refresh:
                raise RuntimeError("Background refresh browser contracts did not pass")
            code = (ROOT / "scripts/tests/go-ui-production-browser.js").read_text()
            code = code.replace("http://127.0.0.1:17702", origin).replace(
                "docs/pr-images/", str(ROOT / "output/pr-images/regression") + "/"
            )
            production = browser_cli(
                "run-code",
                "async page => { const context = await page.context().browser().newContext({serviceWorkers:'block'}); "
                "try { return await ("
                + code
                + ")(await context.newPage()); } finally { await context.close(); } }",
            )
            if '"errors":[]' not in production:
                raise RuntimeError("Production UI browser contracts did not pass")
            code = (ROOT / "scripts/tests/go-ui-http-browser.js").read_text()
            output = browser_cli("run-code", code.replace("STATE", json.dumps(state)))
            if '"errors":[]' not in output:
                raise RuntimeError("Browser did not return successful integration evidence")
            replies = browser_cli(
                "run-code", (ROOT / "scripts/tests/go-ui-replies-browser.js").read_text()
            )
            if '"passed":true' not in replies:
                raise RuntimeError("Reply bubble browser contracts did not pass")
            approvals = browser_cli(
                "run-code", (ROOT / "scripts/tests/go-ui-task-approvals-browser.js").read_text()
            )
            if '"passed":true' not in approvals:
                raise RuntimeError("Task approval browser contracts did not pass")
            task_time = browser_cli(
                "run-code",
                (ROOT / "scripts/tests/go-ui-task-time-browser.js")
                .read_text()
                .replace("STATE", json.dumps({"origin": origin})),
            )
            if '"passed":true' not in task_time:
                raise RuntimeError("Task elapsed-time browser contracts did not pass")
            actual = (base / "download.bin").read_bytes()
            assert len(actual) == len(content)
            assert hashlib.sha256(actual).digest() == hashlib.sha256(content).digest()
            token = cli("access", "-browser-root", str(base / "browser"), "token").strip()
            response = opener.open(
                Request(
                    origin + "/access/token",
                    method="POST",
                    headers={"Authorization": "Bearer " + token},
                )
            )
            cookie = response.headers["Set-Cookie"].split(";", 1)[0]
            response.close()

            def request(path):
                try:
                    return opener.open(
                        Request(origin + path, headers={"Cookie": cookie}), timeout=8
                    )
                except HTTPError as error:
                    return error

            with request("/api/v1/providers") as response:
                assert b"isolated-key" not in response.read()
            path = f"/api/v1/tasks/{task_id}/file-download?"
            with request(
                path + urlencode({"assistant_id": "wrong-owner", "path": "proof.bin"})
            ) as response:
                assert response.status == 409
            with request(
                path + urlencode({"assistant_id": assistant["id"], "path": "../runtime.json"})
            ) as response:
                assert response.status == 400
            stream = request("/api/v1/changes")
            assert stream.status == 200
            ended = threading.Event()

            def drain():
                try:
                    stream.read()
                finally:
                    ended.set()

            threading.Thread(target=drain, daemon=True).start()
            cli("access", "-browser-root", str(base / "browser"), "regenerate-token")
            assert ended.wait(5), "Active SSE stream survived credential rotation"
            stream.close()
            with request("/api/v1/runtime") as response:
                assert response.status == 401
            print(
                "Real browser HTTP/SSE integration passed: two clients, reconnect, sessions, boundaries and binary download"
            )
        finally:
            if browser_open:
                try:
                    browser_cli("close")
                except (RuntimeError, subprocess.TimeoutExpired):
                    pass
            for server in reversed(servers):
                server.shutdown()
                server.server_close()
            for process in reversed(processes):
                stop(process)
            for server in servers:
                with socket.socket() as check:
                    assert check.connect_ex(server.server_address) != 0, (
                        "Fixture listener survived cleanup"
                    )
            if web is not None:
                with socket.socket() as check:
                    assert check.connect_ex(("127.0.0.1", web_port)) != 0, (
                        "Browser listener survived cleanup"
                    )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--binary", type=Path, help="Use an already built current Go CLI")
    args = parser.parse_args()
    if not shutil.which("node") or not shutil.which("npx"):
        parser.error("Pinned Node/npx and cached @playwright/cli are required; run through mise")
    env = dict(os.environ)
    env.pop("POT_ACCESS_VAPID_KEY", None)
    env.pop("POT_ACCESS_VAPID_CONTACT", None)
    # Dependencies must be cached. Runtime metadata refresh cannot reach outside
    # loopback. The browser script likewise rejects non-loopback resource requests.
    env.update(
        {
            "npm_config_offline": "true",
            "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD": "1",
            "GOPROXY": "off",
            "GOTOOLCHAIN": "local",
            "HTTP_PROXY": "http://127.0.0.1:9",
            "HTTPS_PROXY": "http://127.0.0.1:9",
            "ALL_PROXY": "http://127.0.0.1:9",
            "NO_PROXY": "127.0.0.1,localhost",
        }
    )
    cli_command = ["npx", "--offline", "--yes", "--package", "@playwright/cli", "playwright-cli"]
    with tempfile.TemporaryDirectory(prefix="pot-web-http-", dir="/tmp") as temporary:
        base = Path(temporary).resolve()
        base.chmod(0o700)
        key = base / "key"
        key.write_bytes(secrets.token_bytes(32))
        key.chmod(0o600)
        env["POT_SECRET_KEY_FILE"] = str(key)
        binary = args.binary.resolve() if args.binary else base / "pot"
        if not args.binary:
            subprocess.run(
                ["go", "build", "-mod=readonly", "-trimpath", "-o", str(binary), "./cmd/pot"],
                cwd=ROOT,
                env=env,
                check=True,
            )
        run(binary, base, env, cli_command)


if __name__ == "__main__":
    main()
