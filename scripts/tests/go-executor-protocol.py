#!/usr/bin/env python3
"""Actual Go subprocess through retained Linux/macOS Python Runner.execute methods.

Run with a newly built Go pot-tool-executor path. This exercises the preserved
JSON-lines/event/session contract without entering root/cgroup/VM initialization;
real guest containment remains a separate platform acceptance requirement.
"""

import ast
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import threading
import time
import uuid

ROOT = Path(__file__).resolve().parents[2]
BINARY = str(Path(sys.argv[1]).resolve(strict=True))
capabilities = json.loads(subprocess.check_output([BINARY, "--capabilities"]))
assert capabilities["protocol_version"] == 1
assert capabilities["plugins"]["runtime"] == "node"
assert capabilities["observation_modes"] == ["text", "mixed"]
POLICY = dict(
    executable="/bin/bash",
    allow_login_shell=False,
    shell_snapshot=False,
    session_idle_timeout_ms=1800000,
    default_timeout_ms=2000,
    max_timeout_ms=5000,
    default_output_bytes=16384,
    max_output_bytes=65536,
    max_sessions=8,
    default_yield_ms=30,
    max_yield_ms=1000,
)

for source in [
    ROOT / "providers/podman/image/executor.py",
    ROOT / "providers/apple-vm/bootstrap/executor.py",
]:
    parsed = ast.parse(source.read_text())
    selected = [
        node
        for node in parsed.body
        if isinstance(node, (ast.ClassDef, ast.FunctionDef))
        and node.name in ("Runner", "terminate")
    ]
    namespace = dict(
        json=json,
        subprocess=subprocess,
        threading=threading,
        os=os,
        signal=signal,
        time=time,
        Path=Path,
    )
    exec(compile(ast.Module(body=selected, type_ignores=[]), str(source), "exec"), namespace)
    runner_class = namespace["Runner"]
    with tempfile.TemporaryDirectory(prefix="pot-go-executor-") as directory:
        workspace = Path(directory).resolve()
        (workspace / "dir").mkdir()
        process = subprocess.Popen(
            [BINARY],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            cwd=workspace,
            start_new_session=True,
            env={"HOME": str(workspace), "PATH": os.environ["PATH"], "LANG": "C.UTF-8"},
        )
        runner = runner_class.__new__(runner_class)
        runner.process, runner.lock = process, threading.Lock()

        def call(operation, args):
            reply = runner.execute(
                dict(
                    id=uuid.uuid4().hex,
                    operation=operation,
                    cwd=str(workspace),
                    policy=POLICY,
                    args=args,
                )
            )
            assert isinstance(reply["events"], list)
            result = reply["result"]
            if result.get("isError"):
                raise AssertionError(result)
            return result, reply["events"]

        try:
            result, events = call(
                "exec_command", {"cmd": "printf '中文 stdout'; printf stderr >&2"}
            )
            assert (
                result["exit_code"] == 0
                and result["stdout"] == "中文 stdout"
                and result["stderr"] == "stderr"
            )
            assert any(event["ToolOutput"]["stream"] == "stdout" for event in events)
            result, _ = call(
                "exec_command",
                {"cmd": "read -r value; printf 'input=%s' \"$value\"", "yield_time_ms": 10},
            )
            session = result["session_id"]
            result, _ = call(
                "write_stdin", {"session_id": session, "chars": "hello\n", "yield_time_ms": 1000}
            )
            assert result["stdout"] == "input=hello" and result["session_id"] is None
            call(
                "apply_patch",
                {"patch": "*** Begin Patch\n*** Add File: file.txt\n+one\n*** End Patch"},
            )
            assert (workspace / "file.txt").read_text() == "one\n"
            result, _ = call(
                "exec_command",
                {
                    "cmd": "test -t 0 && printf terminal; read -r line; printf ':%s' \"$line\"",
                    "tty": True,
                    "yield_time_ms": 30,
                },
            )
            session, combined = result["session_id"], result["stdout"]
            result, _ = call(
                "write_stdin",
                {
                    "session_id": session,
                    "chars": "pty-input\n",
                    "rows": 40,
                    "cols": 100,
                    "yield_time_ms": 1000,
                },
            )
            assert "terminal" in combined + result["stdout"] and ":pty-input" in result["stdout"]
            result, _ = call("inspect", {})
            assert result["active"] is False
            result, _ = call("exec_command", {"cmd": "sleep 60", "yield_time_ms": 10})
            assert result["session_id"]
            process.stdin.close()
            process.wait(timeout=5)
            assert process.returncode == 0, process.stderr.read().decode()
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait(timeout=5)
    print(f"Retained {source.relative_to(ROOT)} JSON-lines interoperability passed")
