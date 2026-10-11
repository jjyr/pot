#!/usr/bin/env python3
"""Isolated CLI qualification; SSH credentials and machine paths are caller supplied."""

import argparse
import http.server
import json
import os
import shlex
from pathlib import Path
import socket
import subprocess
import sys
import threading
import time
import uuid


class Worker:
    def __init__(self, binary, root):
        self.binary = binary
        self.root = Path(root)
        if self.root.exists() and any(self.root.iterdir()):
            raise RuntimeError("qualification requires a fresh isolated root")
        self.root.mkdir(parents=True, exist_ok=True, mode=0o700)
        self.process = None
        self.model = None
        self.requests = []
        self.lock = threading.Lock()
        self.run(["init"])

    def run(self, args):
        result = subprocess.run(
            [self.binary, "--root", str(self.root), *args],
            capture_output=True,
            text=True,
            timeout=30,
        )
        if result.returncode:
            raise RuntimeError(result.stderr[-3000:])
        return (
            json.loads(result.stdout)
            if result.stdout.strip().startswith(("{", "["))
            else result.stdout.strip()
        )

    def start(self):
        env = dict(
            os.environ,
            POT_RUNTIME_BIND="127.0.0.1:0",
            POT_AGENT_HOME=str(self.root / "agent-home"),
            NO_PROXY="*",
            no_proxy="*",
        )
        self.log = open(self.root / "process.log", "ab")
        self.process = subprocess.Popen(
            [self.binary, "--root", str(self.root), "serve"],
            env=env,
            stdout=self.log,
            stderr=self.log,
        )
        for _ in range(100):
            try:
                return self.api("/api/v1/runtime")
            except Exception:
                if self.process.poll() is not None:
                    raise RuntimeError((self.root / "process.log").read_text()[-3000:])
                time.sleep(0.1)
        raise RuntimeError("startup timeout")

    def api(self, path, body=None):
        args = ["api", path]
        file = self.root / "request.json"
        if body is not None:
            file.write_text(json.dumps(body))
            file.chmod(0o600)
            args.append(str(file))
        try:
            return self.run(args)
        finally:
            file.unlink(missing_ok=True)

    def stop(self, crash=False):
        if self.process and self.process.poll() is None:
            self.process.kill() if crash else self.process.terminate()
            self.process.wait(timeout=15)
        if hasattr(self, "log"):
            self.log.close()

    def listen(self, address):
        reservation = socket.socket()
        reservation.bind((address, 0))
        port = reservation.getsockname()[1]
        reservation.close()
        cert, key = self.root / "cert.pem", self.root / "key.pem"
        subprocess.run(
            [
                "openssl",
                "req",
                "-x509",
                "-newkey",
                "rsa:2048",
                "-nodes",
                "-keyout",
                str(key),
                "-out",
                str(cert),
                "-days",
                "1",
                "-subj",
                f"/CN={address}",
                "-addext",
                f"subjectAltName=IP:{address}",
                "-addext",
                "basicConstraints=critical,CA:FALSE",
                "-addext",
                "extendedKeyUsage=serverAuth",
            ],
            check=True,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
        )
        key.chmod(0o600)
        self.api(
            "/api/v1/runtime/control",
            {
                "action": "enable_control",
                "config": {
                    "address": f"{address}:{port}",
                    "certificate": str(cert),
                    "private_key": str(key),
                },
            },
        )
        return {"url": f"https://{address}:{port}", "ca": cert.read_text()}

    def mock(self):
        worker = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                remote = "CROSS_MACHINE_PROJECT" in json.dumps(body)
                with worker.lock:
                    number = len(worker.requests) if remote else -1
                    if remote:
                        worker.requests.append(body)
                report = "REPORT_ONLY" in json.dumps(body)
                tool = remote and number in (0, 2) and not report
                delta = (
                    {
                        "tool_calls": [
                            {
                                "index": 0,
                                "id": f"effect-{number}",
                                "type": "function",
                                "function": {
                                    "name": "exec_command",
                                    "arguments": json.dumps(
                                        {"cmd": "printf X >> once.txt; sleep 3; cat once.txt"}
                                    ),
                                },
                            }
                        ]
                    }
                    if tool
                    else {"content": "Cross-machine result settled."}
                )
                events = [
                    {"choices": [{"delta": delta, "finish_reason": None}]},
                    {"choices": [{"delta": {}, "finish_reason": "tool_calls" if tool else "stop"}]},
                    {"choices": [], "usage": {"prompt_tokens": 10, "completion_tokens": 5}},
                ]
                data = "".join("data: " + json.dumps(e) + "\n\n" for e in events).encode()
                self.send_response(200)
                self.send_header("Content-Type", "text/event-stream")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                try:
                    self.wfile.write(data)
                except (BrokenPipeError, ConnectionResetError):
                    # Control shutdown can abandon an unrelated Main summary request.
                    pass

        self.model = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        threading.Thread(target=self.model.serve_forever, daemon=True).start()
        return f"http://127.0.0.1:{self.model.server_port}/model"

    def project(self):
        project = self.root / "product/execution-workspaces"
        (project / ".git").mkdir(parents=True, exist_ok=True)
        (project / "AGENTS.md").write_text("CROSS_MACHINE_PROJECT\n")

    def evidence(self):
        files = list((self.root / "product/execution-workspaces").glob("*/once.txt"))
        runs = [
            json.loads(p.read_text())
            for p in (self.root / "product/task-agent-runs").glob("*.json")
        ]
        return {
            "effects": [p.read_text() for p in files],
            "runs": runs,
            "assistant_db": (self.root / "product/pot-v2.sqlite").exists(),
        }

    def dispatch(self, command):
        method = command.pop("method")
        if method == "stats":
            with self.lock:
                return {"count": len(self.requests), "requests": self.requests.copy()}
        if method == "close":
            self.stop()
            if self.model:
                self.model.shutdown()
            return True
        return getattr(self, method)(**command)


class Remote:
    def __init__(self, host, script, binary, root):
        command = shlex.join(["python3", script, "--worker", "--binary", binary, "--root", root])
        self.process = subprocess.Popen(
            ["ssh", "-o", "BatchMode=yes", "-o", "ConnectTimeout=8", host, command],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            text=True,
        )

    def call(self, method, **kwargs):
        self.process.stdin.write(json.dumps(dict(method=method, **kwargs)) + "\n")
        self.process.stdin.flush()
        line = self.process.stdout.readline()
        if not line:
            raise RuntimeError("SSH helper ended")
        response = json.loads(line)
        if "error" in response:
            raise RuntimeError(response["error"])
        return response["result"]


def wait_for(fn, seconds=90):
    end = time.monotonic() + seconds
    while time.monotonic() < end:
        value = fn()
        if value:
            return value
        time.sleep(0.2)
    raise RuntimeError("qualification timeout")


def qualify(args):
    control = Remote(args.host, args.remote_script, args.remote_binary, args.remote_root)
    target = Worker(args.binary, args.root)

    def api(path, body=None):
        return control.call("api", path=path, body=body)

    def status(task_id):
        return next(t for t in api("/api/v1/tasks") if t["id"] == task_id)

    def approve():
        cards = api("/api/v1/permissions")["requests"]
        for card in cards:
            api("/api/v1/permissions", {"id": card["id"], "action": "allow_once"})
        return cards

    try:
        control.call("start")
        target.start()
        api(
            "/api/v1/runtime/resources",
            {"request_id": "assistant", "resource": {"kind": "assistant", "name": "Qualification"}},
        )
        target.api(
            "/api/v1/runtime/resources", {"request_id": "host", "resource": {"kind": "host"}}
        )
        target.project()
        listener = control.call("listen", address=args.address)
        invitation = api("/api/v1/runtime/control", {"action": "create_invitation"})
        target.api(
            "/api/v1/runtime/control",
            {
                "action": "join",
                "request": {
                    "request_id": str(uuid.uuid4()),
                    "url": listener["url"],
                    "invitation": invitation["invitation"],
                    "expected_runtime_id": invitation["identity"]["runtime_id"],
                    "expected_fingerprint": invitation["identity"]["fingerprint"],
                    "ca_pem": listener["ca"],
                },
            },
        )
        endpoint = control.call("mock")
        api(
            "/api/v1/providers",
            {
                "request_id": str(uuid.uuid4()),
                "expected_revision": 0,
                "endpoint": endpoint,
                "model": "fixture",
                "protocol": "openai_chat",
                "api_key": "CROSS_MACHINE_SECRET",
                "max_output_tokens": 1024,
            },
        )
        target_id = target.api("/api/v1/runtime")["runtime_id"]
        wait_for(
            lambda: any(
                m["runtime_id"] == target_id and m.get("connection") == "online"
                for m in api("/api/v1/machines")["machines"]
            )
        )
        binding = {"runtime_id": target_id, "target": {"kind": "host"}}
        task = api(
            "/api/v1/tasks",
            {
                "request_id": "control-crash-task",
                "text": "Execute once",
                "environment_ref": binding,
            },
        )

        def first_approval():
            if status(task["id"])["phase"] == "failed":
                raise RuntimeError(api(f"/api/v1/tasks/{task['id']}")["messages"][-1]["text"])
            return approve()

        wait_for(first_approval)
        wait_for(lambda: target.evidence()["effects"] == ["X"])
        original = target.evidence()["runs"][0]["launch"]["run_id"]
        control.call("stop", crash=True)
        time.sleep(33)
        assert control.call("stats")["count"] == 1, "sampled during Control crash"
        assert target.evidence()["effects"] == ["X"], "replayed disconnected effect"
        control.call("start")
        finished = wait_for(
            lambda: (
                s
                if (s := status(task["id"]))["phase"] in ("complete", "failed", "interrupted")
                else None
            )
        )
        assert finished["phase"] == "complete", finished
        assert target.evidence()["runs"][0]["launch"]["run_id"] == original
        assert target.evidence()["effects"] == ["X"]
        assert control.call("stats")["count"] == 2
        print("PASS Control SIGKILL > lease expiry: original run, exactly one effect", flush=True)
        task2 = api(
            "/api/v1/tasks",
            {
                "request_id": "execution-crash-task",
                "text": "Execute once on second task",
                "environment_ref": binding,
            },
        )
        cards = wait_for(lambda: api("/api/v1/permissions")["requests"])
        target.stop(crash=True)
        for card in cards:
            api("/api/v1/permissions", {"id": card["id"], "action": "allow_once"})
        target.start()
        interrupted = wait_for(
            lambda: (
                s
                if (s := status(task2["id"]))["phase"] in ("interrupted", "failed", "complete")
                else None
            )
        )
        assert interrupted["phase"] == "interrupted", interrupted
        assert control.call("stats")["count"] == 3
        assert target.evidence()["effects"] == ["X"]
        api(
            f"/api/v1/tasks/{task2['id']}/messages",
            {
                "request_id": "explicit-guide",
                "conversation_id": task2["id"],
                "text": "REPORT_ONLY. Report known outcome; do not run the old command.",
            },
        )
        finished = wait_for(
            lambda: s if (s := status(task2["id"]))["phase"] == "complete" else None
        )
        assert control.call("stats")["count"] == 4
        evidence = target.evidence()
        assert evidence["effects"] == ["X"]
        assert not evidence["assistant_db"]
        stats = control.call("stats")
        assert all("CROSS_MACHINE_PROJECT" in json.dumps(r) for r in stats["requests"])
        assert all(r["model"] == "fixture" for r in stats["requests"])
        for path in (target.root / "product").rglob("*"):
            if path.is_file() and path.suffix in (".json", ".sqlite"):
                assert b"CROSS_MACHINE_SECRET" not in path.read_bytes(), path
        print(
            "PASS execution SIGKILL: interrupted until explicit guidance; no replay or destination credentials",
            flush=True,
        )
        Path(args.evidence).write_text(
            json.dumps(
                {
                    "control_crash_run": original,
                    "effects": evidence["effects"],
                    "runs": [
                        {
                            "run_id": r["launch"]["run_id"],
                            "phase": r["phase"],
                            "boot_id": r["boot_id"],
                        }
                        for r in evidence["runs"]
                    ],
                    "worker_samples": stats["count"],
                },
                indent=2,
            )
            + "\n"
        )
    finally:
        target.stop()
        control.call("close")
        control.process.stdin.close()
        control.process.wait(timeout=10)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--worker", action="store_true")
    for key in (
        "binary",
        "root",
        "host",
        "remote-script",
        "remote-binary",
        "remote-root",
        "address",
        "evidence",
    ):
        parser.add_argument("--" + key)
    args = parser.parse_args()
    required = (
        ("binary", "root")
        if args.worker
        else (
            "binary",
            "root",
            "host",
            "remote_script",
            "remote_binary",
            "remote_root",
            "address",
            "evidence",
        )
    )
    if any(getattr(args, field) is None for field in required):
        parser.error("missing qualification arguments")
    if args.worker:
        worker = Worker(args.binary, args.root)
        try:
            for line in sys.stdin:
                try:
                    response = {"result": worker.dispatch(json.loads(line))}
                except Exception as error:
                    response = {"error": str(error)}
                print(json.dumps(response), flush=True)
        finally:
            worker.dispatch({"method": "close"})
    else:
        qualify(args)


if __name__ == "__main__":
    main()
