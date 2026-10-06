#!/usr/bin/env python3
"""Sandbox-only executor. No product credentials, Agent loop or Host API."""

import hmac
import json
import os
from pathlib import Path
import pwd
import signal
import sqlite3
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = Path(os.environ.get("POT_EXECUTOR_ROOT", "/data")).resolve()
WORK = ROOT / "workspace"
WORK.mkdir(parents=True, exist_ok=True)
# Never put control credentials/ledgers on virtiofs: Host ownership is mapped
# to the guest's ordinary user. Root's private native filesystem is the boundary.
TOKEN = os.environ.pop("POT_EXECUTOR_TOKEN")
PRIVATE = Path("/run/pot-executor")
PRIVATE.mkdir(mode=0o700, parents=True, exist_ok=True)
os.chmod(PRIVATE, 0o700)
LOCK = threading.RLock()
PROCESSES = {}
CANCELLED = set()
DESKTOP = None
DB = sqlite3.connect(PRIVATE / "operations.sqlite", check_same_thread=False)
DB.execute("CREATE TABLE IF NOT EXISTS operations (id TEXT PRIMARY KEY, input TEXT, result TEXT)")
DB.execute(
    "UPDATE operations SET result=? WHERE result IS NULL",
    (json.dumps({"error": "Executor restarted; prior execution outcome unknown"}),),
)
DB.commit()
USER = pwd.getpwnam("potdesktop") if os.getuid() == 0 else None


def workspace(cwd):
    # Host and Sandbox paths have different roots; only the declared guest namespace is accepted.
    prefix = "/data/workspace"
    if cwd != prefix and not cwd.startswith(prefix + "/"):
        raise ValueError("Invalid execution workspace")
    path = (WORK / cwd.removeprefix(prefix).lstrip("/")).resolve()
    if not path.is_relative_to(WORK):
        raise ValueError("Workspace escape")
    path.mkdir(parents=True, exist_ok=True)
    if USER:
        # Ancestors created by Runtime mounts may have host ownership.
        for parent in [WORK, *path.parents, path]:
            if parent.is_relative_to(WORK):
                os.chown(parent, USER.pw_uid, USER.pw_gid)
    return path


def target(cwd, name):
    raw = Path(name or ".")
    if raw.is_absolute():
        prefix = "/data/workspace"
        text = str(raw)
        if text != prefix and not text.startswith(prefix + "/"):
            raise ValueError("File path outside Sandbox workspace")
        raw = WORK / text.removeprefix(prefix).lstrip("/")
    path = (cwd / raw).resolve()
    if not path.is_relative_to(WORK):
        raise ValueError("File path outside Sandbox workspace")
    return path


def terminate(process):
    cgroup = getattr(process, "sandbox_cgroup", None)
    if cgroup is not None:
        (cgroup / "cgroup.kill").write_text("1")
    # Reap all groups in this session, including shell background groups.
    groups = {process.pid}
    proc = Path("/proc")
    if proc.is_dir():
        for entry in proc.iterdir():
            try:
                stat = (entry / "stat").read_text().rsplit(")", 1)[1].split()
                if int(stat[3]) == process.pid:
                    groups.add(int(stat[2]))
            except (OSError, ValueError, IndexError):
                pass
    for group in groups:
        try:
            os.killpg(group, signal.SIGKILL)
        except ProcessLookupError:
            pass
    process.wait(timeout=5)
    if cgroup is not None:
        deadline = time.monotonic() + 5
        while "populated 1" in (cgroup / "cgroup.events").read_text():
            if time.monotonic() > deadline:
                raise RuntimeError("Process containment cleanup unconfirmed")
            time.sleep(0.025)


def demote():
    if USER:
        os.setgroups([])
        os.setgid(USER.pw_gid)
        os.setuid(USER.pw_uid)


def command(op_id, argv, cwd, timeout, payload=None):
    output = PRIVATE / ("output-" + op_id)
    env = {
        "PATH": "/usr/local/bin:/usr/bin:/bin",
        "LANG": "C.UTF-8",
        "HOME": str(WORK),
        "TMPDIR": "/tmp",
    }
    with output.open("wb") as out:
        cgroup = None
        if os.uname().sysname == "Linux":
            if not USER or not Path("/sys/fs/cgroup/cgroup.controllers").is_file():
                raise RuntimeError("Root-owned cgroup v2 containment required")
            cgroup = Path("/sys/fs/cgroup") / ("pot-" + op_id)
            cgroup.mkdir(mode=0o700)

        def contain():
            if cgroup is not None:
                (cgroup / "cgroup.procs").write_text(str(os.getpid()))
            demote()

        process = None
        timed_out = False
        try:
            with LOCK:
                if op_id in CANCELLED:
                    return {"cancelled": True}
                process = subprocess.Popen(
                    argv,
                    cwd=cwd,
                    env=env,
                    stdout=out,
                    stderr=subprocess.STDOUT,
                    start_new_session=True,
                    preexec_fn=contain,
                    stdin=subprocess.PIPE if payload is not None else subprocess.DEVNULL,
                )
                process.sandbox_cgroup = cgroup
                PROCESSES[op_id] = process
            if payload is not None:
                process.stdin.write(payload)
                process.stdin.close()
            deadline = time.monotonic() + timeout
            while process.poll() is None:
                if time.monotonic() >= deadline or output.stat().st_size > 8 * 1024 * 1024:
                    timed_out = True
                    break
                time.sleep(0.05)
        finally:
            if process is not None:
                terminate(process)
            with LOCK:
                PROCESSES.pop(op_id, None)
            if cgroup is not None:
                cgroup.rmdir()
    with output.open("rb") as stream:
        size = output.stat().st_size
        stream.seek(max(0, size - 65536))
        text = stream.read(65536).decode("utf-8", errors="replace")
    output.unlink(missing_ok=True)
    return {
        "output": text,
        "exit_code": process.returncode,
        "truncated": size > 65536,
        "timed_out": timed_out,
        "cancelled": op_id in CANCELLED,
    }


def execute(request):
    op_id = request["id"]
    if (
        not isinstance(op_id, str)
        or len(op_id) > 64
        or not all(c.isalnum() or c == "-" for c in op_id)
    ):
        raise ValueError("Invalid operation ID")
    encoded = json.dumps(request, sort_keys=True)
    with LOCK:
        row = DB.execute("SELECT input,result FROM operations WHERE id=?", (op_id,)).fetchone()
        if row:
            if row[0] != encoded and row[0] != "cancelled":
                raise ValueError("Operation ID conflict")
            if row[1] is None:
                raise ValueError("Operation in progress; do not replay")
            return json.loads(row[1])
        DB.execute("INSERT INTO operations VALUES (?,?,NULL)", (op_id, encoded))
        DB.commit()
    try:
        cwd = workspace(request["cwd"])
        args = request.get("args", {})
        op = request["operation"]
        path = target(cwd, args.get("path", "."))
        if op == "bash":
            result = command(
                op_id,
                ["/bin/bash", "-lc", args["command"]],
                cwd,
                min(600, max(1, args.get("timeout", 120))),
            )
        elif op in ("read", "write", "edit", "ls"):
            response = command(
                op_id,
                ["python3", str(Path(__file__).with_name("file-tools.py"))],
                cwd,
                30,
                json.dumps({"operation": op, "args": args, "path": str(path)}).encode(),
            )
            result = response if response.get("cancelled") else json.loads(response["output"])
        elif op in ("grep", "find"):
            argv = (
                ["rg", "--no-heading", "--", args["pattern"], str(path)]
                if op == "grep"
                else ["find", str(path), "-name", args["pattern"]]
            )
            result = command(op_id, argv, cwd, 30)
        else:
            raise ValueError("Unknown executor operation")
    except Exception as error:
        result = {"error": str(error)[:1024]}
    with LOCK:
        DB.execute("UPDATE operations SET result=? WHERE id=?", (json.dumps(result), op_id))
        DB.commit()
    return result


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_):
        pass

    def respond(self, status, value):
        body = json.dumps(value).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        try:
            self.wfile.write(body)
        except (BrokenPipeError, ConnectionResetError):
            pass

    def authorized(self):
        return hmac.compare_digest(self.headers.get("Authorization", ""), "Bearer " + TOKEN)

    def do_GET(self):
        if not self.authorized():
            self.respond(403, {"error": "Unauthorized"})
        elif self.path == "/health":
            self.respond(200, {"ready": True})
        else:
            self.respond(404, {})

    def do_POST(self):
        global DESKTOP
        if not self.authorized():
            self.respond(403, {"error": "Unauthorized"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= 3 * 1024 * 1024:
                raise ValueError("Invalid request size")
            request = json.loads(self.rfile.read(length))
            if self.path == "/execute":
                self.respond(200, execute(request))
            elif self.path == "/desktop":
                with LOCK:
                    if DESKTOP is None or DESKTOP.poll() is not None:
                        DESKTOP = subprocess.Popen(
                            ["/bin/sh", "/opt/pot/desktop.sh"], start_new_session=True
                        )
                self.respond(200, {"starting": True})
            elif self.path == "/cancel":
                op_id = request["id"]
                with LOCK:
                    CANCELLED.add(op_id)
                    process = PROCESSES.get(op_id)
                    if process:
                        terminate(process)
                    DB.execute(
                        "INSERT OR IGNORE INTO operations VALUES (?,?,?)",
                        (op_id, "cancelled", json.dumps({"cancelled": True})),
                    )
                    DB.commit()
                deadline = time.monotonic() + 10
                while time.monotonic() < deadline:
                    with LOCK:
                        row = DB.execute(
                            "SELECT result FROM operations WHERE id=?", (op_id,)
                        ).fetchone()
                    if row and row[0] is not None:
                        break
                    time.sleep(0.05)
                self.respond(200, {"settled": row is not None and row[0] is not None})
            else:
                self.respond(404, {})
        except Exception as error:
            self.respond(409, {"error": str(error)[:1024]})


def shutdown(*_):
    with LOCK:
        for process in list(PROCESSES.values()):
            terminate(process)
    if DESKTOP and DESKTOP.poll() is None:
        DESKTOP.terminate()
        try:
            DESKTOP.wait(timeout=10)
        except subprocess.TimeoutExpired:
            terminate(DESKTOP)
    (ROOT / "browser-stopped").touch()
    raise SystemExit(0)


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, shutdown)
    signal.signal(signal.SIGINT, shutdown)
    ThreadingHTTPServer(
        (
            os.environ.get("POT_EXECUTOR_BIND", "0.0.0.0"),
            int(os.environ.get("POT_EXECUTOR_PORT", "17701")),
        ),
        Handler,
    ).serve_forever()
