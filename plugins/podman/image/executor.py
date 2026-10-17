#!/usr/bin/env python3
"""Sandbox-only executor. No product credentials, Agent loop or Host API."""

import ctypes
import hmac
import json
import os
from pathlib import Path
import pwd
import signal
import secrets
import sqlite3
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from skill_mounts import SKILL_SOURCES, skill_namespace
from operation_prepare import prepare

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
RUNNERS = {}
CANCELLED = set()
DESKTOP = None
DESKTOP_TOKEN = secrets.token_urlsafe(32)
DB = sqlite3.connect(PRIVATE / "operations.sqlite", check_same_thread=False)
DB.execute("CREATE TABLE IF NOT EXISTS operations (id TEXT PRIMARY KEY, input TEXT, result TEXT)")
DB.execute(
    "UPDATE operations SET result=? WHERE result IS NULL",
    (json.dumps({"error": "Executor restarted; prior execution outcome unknown"}),),
)
DB.commit()
USER = pwd.getpwnam("potdesktop") if os.getuid() == 0 else None
if USER:
    # Hide raw sources even when main and task scopes share a VM.
    os.chmod(SKILL_SOURCES, 0o700)


def workspace(cwd, role):
    # Host and Sandbox paths have different roots; only the declared guest namespace is accepted.
    prefix = "/data/workspace"
    if cwd != prefix and not cwd.startswith(prefix + "/"):
        raise ValueError("Invalid execution workspace")
    if role == "task" and not cwd.startswith(prefix + "/tasks/"):
        raise ValueError("Invalid task workspace")
    path = (WORK / cwd.removeprefix(prefix).lstrip("/")).resolve()
    if not path.is_relative_to(WORK):
        raise ValueError("Workspace escape")
    path.mkdir(parents=True, exist_ok=True)
    if USER:
        # Ancestors created by Runtime mounts may have host ownership.
        user = USER
        for parent in [WORK, *path.parents, path]:
            if parent.is_relative_to(WORK):
                os.chown(parent, USER.pw_uid, USER.pw_gid)
                os.chmod(parent, 0o755)
        os.chown(path, user.pw_uid, USER.pw_gid)
        os.chmod(path, 0o775 if role == "task" else 0o755)
    return path


def development_environment():
    home = ROOT / "development"
    for path in [home, home / ".cargo", home / ".rustup"]:
        if path.is_symlink():
            raise ValueError("Development environment must not be a symlink")
        path.mkdir(parents=True, exist_ok=True)
        if USER:
            os.chown(path, USER.pw_uid, USER.pw_gid)
            os.chmod(path, 0o2775)
    return home


def runner_environment(cwd):
    development = development_environment()
    return {
        "PATH": f"{development}/.cargo/bin:/usr/local/bin:/usr/bin:/bin",
        "CARGO_HOME": str(development / ".cargo"),
        "RUSTUP_HOME": str(development / ".rustup"),
        "LANG": "C.UTF-8",
        "HOME": str(cwd),
        "TMPDIR": "/tmp",
    }


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


def demote(role):
    if USER:
        os.setgroups([])
        user = USER
        os.setgid(USER.pw_gid)
        os.setuid(user.pw_uid)
        os.umask(0o002)
        if ctypes.CDLL(None, use_errno=True).prctl(38, 1, 0, 0, 0) != 0:
            raise OSError(ctypes.get_errno(), "Unable to set no_new_privs")


class Runner:
    def __init__(self, scope, cwd, policy, role):
        self.lock = threading.Lock()
        self.cwd, self.policy, self.role = cwd, policy, role
        self.last_used = time.monotonic()
        self.busy = False
        group = Path("/sys/fs/cgroup") / ("pot-" + scope)
        if not USER or not Path("/sys/fs/cgroup/cgroup.controllers").is_file():
            raise RuntimeError("Root-owned cgroup v2 containment required")
        group.mkdir(mode=0o700)

        def contain():
            (group / "cgroup.procs").write_text(str(os.getpid()))
            try:
                skill_namespace(role, cwd, [])
                demote(role)
                os.write(1, b'{"ready":true}\n')
            except Exception as error:
                os.write(1, (json.dumps({"error": str(error)[:1024]}) + "\n").encode())
                os._exit(125)

        self.process = subprocess.Popen(
            ["/opt/pot/pot-tool-executor"],
            cwd=cwd,
            env=runner_environment(cwd),
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
            preexec_fn=contain,
        )
        self.process.sandbox_cgroup = group
        prepared = json.loads(self.process.stdout.readline(2048))
        if prepared.get("ready") is not True:
            terminate(self.process)
            group.rmdir()
            raise RuntimeError(prepared.get("error", "Scope preparation failed"))

    def execute(self, request):
        with self.lock:
            if self.process.poll() is not None:
                raise RuntimeError("Execution scope ended; sessions cannot be resumed")
            if request["operation"] != "inspect":
                self.last_used = time.monotonic()
            self.process.stdin.write((json.dumps(request) + "\n").encode())
            self.process.stdin.flush()
            events = []
            while True:
                line = self.process.stdout.readline(2 * 1024 * 1024)
                if not line:
                    raise RuntimeError(
                        "Execution scope interrupted; external effects may have occurred"
                    )
                value = json.loads(line)
                if "result" in value:
                    return {"result": value["result"], "events": events}
                if "event" in value:
                    events.append(value["event"])


def valid_id(value):
    return (
        isinstance(value, str)
        and 0 < len(value) <= 64
        and all(c.isalnum() or c == "-" for c in value)
    )


def cancel(scope):
    if not valid_id(scope):
        raise ValueError("Invalid execution scope")
    with LOCK:
        CANCELLED.add(scope)
        runner = RUNNERS.get(scope)
        if runner:
            terminate(runner.process)
            runner.process.sandbox_cgroup.rmdir()
            RUNNERS.pop(scope, None)
    return {"settled": True}


def execute(request):
    op_id, scope = request["id"], request["scope"]
    if not valid_id(op_id) or not valid_id(scope):
        raise ValueError("Invalid operation/scope ID")
    encoded = json.dumps(
        {key: value for key, value in request.items() if key != "environment"},
        sort_keys=True,
    )
    with LOCK:
        row = DB.execute("SELECT input,result FROM operations WHERE id=?", (op_id,)).fetchone()
        if row:
            if row[0] != encoded:
                raise ValueError("Operation ID conflict")
            if row[1] is None:
                raise ValueError("Operation in progress; do not replay")
            return json.loads(row[1])
        if scope in CANCELLED:
            raise ValueError("Execution scope ended")
        role = request["role"]
        if role not in ("main", "task"):
            raise ValueError("Invalid execution role")
        cwd = workspace(request["cwd"], role)
        runner = RUNNERS.get(scope)
        if runner is None:
            runner = Runner(scope, cwd, request["policy"], role)
            RUNNERS[scope] = runner
        if runner.cwd != cwd or runner.policy != request["policy"] or runner.role != role:
            raise ValueError("Scope configuration changed")
        DB.execute("INSERT INTO operations VALUES (?,?,NULL)", (op_id, encoded))
        DB.commit()
        runner.busy = True
    try:
        result = runner.execute({**request, "cwd": str(cwd)})
    except Exception as error:
        result = {"result": {"isError": True, "error": str(error)[:1024]}, "events": []}
    finally:
        runner.busy = False
        runner.last_used = time.monotonic()
    with LOCK:
        DB.execute("UPDATE operations SET result=? WHERE id=?", (json.dumps(result), op_id))
        DB.commit()
    return result


def inspect(scope):
    if not valid_id(scope):
        raise ValueError("Invalid execution scope")
    with LOCK:
        runner = RUNNERS.get(scope)
    if runner is None:
        return {"sessions": [], "active": False, "ended": True}
    reply = runner.execute(
        {
            "id": scope,
            "operation": "inspect",
            "args": {},
            "cwd": str(runner.cwd),
            "policy": runner.policy,
        }
    )
    if reply["result"].get("isError"):
        raise RuntimeError(reply["result"]["error"])
    if reply["result"].get("active") is False:
        # No managed process remains. Drain orphaned/setsid descendants before
        # allowing Host to release the execution lease, retaining only the runner.
        group = runner.process.sandbox_cgroup
        deadline = time.monotonic() + 5
        while True:
            pids = {int(pid) for pid in (group / "cgroup.procs").read_text().split()} - {
                runner.process.pid
            }
            if not pids:
                break
            for pid in pids:
                try:
                    os.kill(pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
            if time.monotonic() > deadline:
                raise RuntimeError("Idle scope descendant cleanup stop unconfirmed")
            time.sleep(0.025)
    return reply["result"]


def expire_idle():
    # Expire inactive runners too, bounding memory used by initialization snapshots.
    while True:
        time.sleep(1)
        with LOCK:
            for scope, runner in list(RUNNERS.items()):
                if (
                    not runner.busy
                    and time.monotonic() - runner.last_used
                    > runner.policy.get("session_idle_timeout_ms", 1800000) / 1000
                ):
                    try:
                        cancel(scope)
                    except Exception:
                        # Retain failed cleanup for subsequent retry and Host reconciliation.
                        pass


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
            if self.path == "/prepare":
                home = workspace(request["cwd"], request["role"])
                self.respond(200, prepare(request, home))
            elif self.path == "/execute":
                self.respond(200, execute(request))
            elif self.path == "/capabilities":
                probe = subprocess.run(
                    ["/opt/pot/pot-tool-executor", "--capabilities"],
                    stdin=subprocess.DEVNULL,
                    capture_output=True,
                    timeout=5,
                    check=True,
                    env={"PATH": "/usr/local/bin:/usr/bin:/bin"},
                )
                capabilities = json.loads(probe.stdout)
                plugins = capabilities.get("plugins", {})
                plugins["requirements"] = [
                    name
                    for name in plugins.get("requirements", [])
                    if name in ("node", "chromium", "git", "gh")
                    and Path("/usr/bin/" + name).is_file()
                ]
                if not Path("/usr/bin/node").is_file():
                    plugins["runtime"] = None
                capabilities["plugins"] = plugins
                self.respond(200, capabilities)
            elif self.path == "/inspect":
                self.respond(200, inspect(request["id"]))
            elif self.path == "/desktop":
                with LOCK:
                    if DESKTOP is None or DESKTOP.poll() is not None:
                        DESKTOP = subprocess.Popen(
                            ["/bin/sh", "/opt/pot/desktop.sh"],
                            start_new_session=True,
                            env={**os.environ, "POT_BROKER_TOKEN": DESKTOP_TOKEN},
                        )
                self.respond(200, {"starting": True, "token": DESKTOP_TOKEN})
            elif self.path in ("/cancel", "/finish"):
                self.respond(200, cancel(request["id"]))
            else:
                self.respond(404, {})
        except Exception as error:
            self.respond(409, {"error": str(error)[:1024]})


def shutdown(*_):
    with LOCK:
        for scope in list(RUNNERS):
            cancel(scope)
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
    threading.Thread(target=expire_idle, daemon=True).start()
    ThreadingHTTPServer(
        (
            os.environ.get("POT_EXECUTOR_BIND", "0.0.0.0"),
            int(os.environ.get("POT_EXECUTOR_PORT", "17701")),
        ),
        Handler,
    ).serve_forever()
