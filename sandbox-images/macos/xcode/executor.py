#!/usr/bin/env python3
"""Root macOS guest broker for persistent VMs; closure ends managed sessions only."""

import hmac
import json
import os
from pathlib import Path
import pwd
import signal
import socket
import sqlite3
import ssl
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PRIVATE = Path("/var/db/pot-executor")
WORK = Path("/Volumes/PotWorkspace/workspace")
PREFIX = str(WORK)
LOCK = threading.RLock()
RUNNERS = {}
CANCELLED = set()
TOKEN = None
USER = None
DB = None


def valid_id(value):
    return (
        isinstance(value, str)
        and 0 < len(value) <= 64
        and all(
            c in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-" for c in value
        )
    )


def workspace(cwd, role):
    if not isinstance(cwd, str) or (cwd != PREFIX and not cwd.startswith(PREFIX + "/")):
        raise ValueError("Invalid workspace")
    if role not in ("main", "task") or (role == "task" and not cwd.startswith(PREFIX + "/tasks/")):
        raise ValueError("Invalid task workspace")
    path = Path(cwd).resolve()
    if not path.is_relative_to(WORK.resolve()):
        raise ValueError("Workspace escape")
    path.mkdir(parents=True, exist_ok=True)
    return path


def terminate(process):
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait(timeout=5)
    # Detached children/launchd jobs may survive. Never claim process containment.


class Runner:
    def __init__(self, scope, cwd, policy, role):
        self.lock = threading.Lock()
        self.busy = False
        self.last_used = time.monotonic()
        self.cwd, self.policy, self.role = cwd, policy, role
        temporary = Path("/var/tmp/pot-scopes") / scope
        temporary.mkdir(mode=0o700, parents=True, exist_ok=True)
        os.chown(temporary, USER.pw_uid, USER.pw_gid)
        # Each guest gets only readonly skill content, never Host credentials.
        skills = Path("/Volumes/PotSkills")
        if skills.is_dir():
            destination = cwd / ".pot/agent"
            destination.mkdir(parents=True, exist_ok=True)
            link = destination / "skills"
            if not link.exists() and not link.is_symlink():
                link.symlink_to(skills)
        self.process = subprocess.Popen(
            ["/usr/local/libexec/pot-tool-executor"],
            cwd=cwd,
            env={
                "PATH": "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
                "LANG": "en_US.UTF-8",
                "HOME": str(cwd),
                "TMPDIR": str(temporary),
            },
            user=USER.pw_uid,
            group=USER.pw_gid,
            extra_groups=[],
            umask=0o022,
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )

    def execute(self, request):
        with self.lock:
            if self.process.poll() is not None:
                raise RuntimeError("Execution scope ended")
            self.process.stdin.write((json.dumps(request) + "\n").encode())
            self.process.stdin.flush()
            events, total = [], 0
            while True:
                line = self.process.stdout.readline(2 * 1024 * 1024 + 1)
                total += len(line)
                if not line or len(line) > 2 * 1024 * 1024 or total > 8 * 1024 * 1024:
                    terminate(self.process)
                    raise RuntimeError(
                        "Runner interrupted or output exceeded bound; outcome unknown"
                    )
                value = json.loads(line)
                if "result" in value:
                    return {"result": value["result"], "events": events}
                if "event" in value:
                    events.append(value["event"])


def execute(request):
    op, scope = request["id"], request["scope"]
    if not valid_id(op) or not valid_id(scope):
        raise ValueError("Invalid operation/scope")
    encoded = json.dumps(request, sort_keys=True)
    with LOCK:
        row = DB.execute("SELECT input,result FROM operations WHERE id=?", (op,)).fetchone()
        if row:
            if row[0] != encoded or row[1] is None:
                raise ValueError("Conflicting or unknown prior operation; never replay")
            return json.loads(row[1])
        if scope in CANCELLED:
            raise ValueError("Expired scope")
        if any(existing != scope for existing in RUNNERS):
            raise ValueError("Exclusive execution owner required")
        cwd = workspace(request["cwd"], request["role"])
        runner = RUNNERS.get(scope)
        if runner is None:
            runner = Runner(scope, cwd, request["policy"], request["role"])
            RUNNERS[scope] = runner
        if (runner.cwd, runner.policy, runner.role) != (cwd, request["policy"], request["role"]):
            raise ValueError("Scope configuration changed")
        DB.execute("INSERT INTO operations VALUES (?,?,NULL)", (op, encoded))
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
        DB.execute("UPDATE operations SET result=? WHERE id=?", (json.dumps(result), op))
        DB.commit()
    return result


def inspect(scope):
    if not valid_id(scope):
        raise ValueError("Invalid scope")
    with LOCK:
        runner = RUNNERS.get(scope)
    if runner is None or runner.process.poll() is not None:
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
    return reply["result"]


def cancel(scope):
    if not valid_id(scope):
        raise ValueError("Invalid scope")
    with LOCK:
        CANCELLED.add(scope)
        runner = RUNNERS.get(scope)
        if runner:
            terminate(runner.process)
            RUNNERS.pop(scope, None)
    return {
        "scope_closed": True,
        "settled": False,
        "cleanup": "session-closure",
        "process_containment": False,
    }


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
    protocol_version = "HTTP/1.1"

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

    def desktop_stream(self):
        # Trusted owner opt-in. Never accept a destination or credentials from callers.
        config = json.loads((PRIVATE / "config.json").read_text())
        if (
            config.get("desktop_rfb") != "loopback-none-3.8"
            or self.headers.get("Upgrade") != "pot-rfb"
        ):
            self.respond(503, {"error": "Desktop not configured"})
            return
        try:
            upstream = socket.create_connection(("127.0.0.1", 5900), timeout=5)
        except OSError:
            self.respond(503, {"error": "Desktop service unavailable"})
            return
        with upstream:
            upstream.settimeout(3600)
            self.connection.settimeout(3600)
            self.send_response(101)
            self.send_header("Connection", "Upgrade")
            self.send_header("Upgrade", "pot-rfb")
            self.end_headers()
            self.wfile.flush()

            def downstream():
                try:
                    while True:
                        data = upstream.recv(65536)
                        if not data:
                            break
                        self.connection.sendall(data)
                except OSError:
                    pass
                finally:
                    try:
                        self.connection.shutdown(socket.SHUT_RDWR)
                    except OSError:
                        pass

            thread = threading.Thread(target=downstream, daemon=True)
            thread.start()
            try:
                while True:
                    data = self.rfile.read1(65536)
                    if not data:
                        break
                    upstream.sendall(data)
            except OSError:
                pass
            finally:
                try:
                    upstream.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
                thread.join(timeout=2)
                self.close_connection = True

    def do_GET(self):
        if not self.authorized():
            self.respond(403, {})
        elif self.path == "/desktop-ready":
            config = json.loads((PRIVATE / "config.json").read_text())
            if config.get("desktop_rfb") != "loopback-none-3.8":
                self.respond(503, {"error": "Desktop not configured"})
            else:
                try:
                    with socket.create_connection(("127.0.0.1", 5900), timeout=2):
                        self.respond(200, {"ready": True})
                except OSError:
                    self.respond(503, {"error": "Desktop service unavailable"})
        elif self.path == "/desktop-stream":
            self.desktop_stream()
        elif self.path == "/health":
            self.respond(200, {"ready": True, "protocol": "pot-macos-executor-v2"})
        else:
            self.respond(404, {})

    def do_POST(self):
        if not self.authorized():
            self.respond(403, {})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            if not 0 < length <= 3 * 1024 * 1024:
                raise ValueError("Invalid request size")
            request = json.loads(self.rfile.read(length))
            if self.path == "/execute":
                self.respond(200, execute(request))
            elif self.path == "/inspect":
                self.respond(200, inspect(request["id"]))
            elif self.path in ("/finish", "/cancel"):
                self.respond(200, cancel(request["id"]))
            else:
                self.respond(404, {})
        except Exception as error:
            self.respond(409, {"error": str(error)[:1024]})


def main():
    global TOKEN, USER, DB
    if os.getuid() != 0:
        raise RuntimeError("Broker must be root; runner is always a standard user")
    config = json.loads((PRIVATE / "config.json").read_text())
    USER = pwd.getpwnam(config["user"])
    if USER.pw_uid < 501:
        raise RuntimeError("Standard execution account required")
    membership = subprocess.run(
        ["/usr/sbin/dseditgroup", "-o", "checkmember", "-m", USER.pw_name, "admin"],
        capture_output=True,
        text=True,
    ).stdout
    if not membership.startswith("no "):
        raise RuntimeError("Execution account must not be an administrator")
    sudo = subprocess.run(
        ["/usr/bin/sudo", "-l", "-U", USER.pw_name], capture_output=True, text=True
    )
    if sudo.returncode != 1:
        raise RuntimeError("Execution account must have no sudo grants")
    TOKEN = (PRIVATE / "executor-token").read_text().strip()
    if not 32 <= len(TOKEN) <= 256:
        raise RuntimeError("Invalid enrollment credential")
    Path("/Volumes/PotWorkspace").mkdir(parents=True, exist_ok=True)
    if not os.path.ismount("/Volumes/PotWorkspace"):
        subprocess.run(
            [
                "/sbin/mount_virtiofs",
                "-u",
                str(USER.pw_uid),
                "-g",
                str(USER.pw_gid),
                "PotWorkspace",
                "/Volumes/PotWorkspace",
            ],
            check=True,
        )
    Path("/Volumes/PotSkills").mkdir(parents=True, exist_ok=True)
    if not os.path.ismount("/Volumes/PotSkills"):
        subprocess.run(
            ["/sbin/mount_virtiofs", "-r", "PotSkills", "/Volumes/PotSkills"], check=True
        )
    WORK.mkdir(parents=True, exist_ok=True)
    DB = sqlite3.connect(PRIVATE / "operations.sqlite", check_same_thread=False)
    DB.execute(
        "CREATE TABLE IF NOT EXISTS operations (id TEXT PRIMARY KEY, input TEXT, result TEXT)"
    )
    DB.execute(
        "UPDATE operations SET result=? WHERE result IS NULL",
        (
            json.dumps(
                {
                    "result": {"isError": True, "error": "Broker restarted; outcome unknown"},
                    "events": [],
                }
            ),
        ),
    )
    DB.commit()
    server = ThreadingHTTPServer(("0.0.0.0", 17701), Handler)
    server.daemon_threads = True
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.minimum_version = ssl.TLSVersion.TLSv1_2
    tls.load_cert_chain(PRIVATE / "guest-cert.pem", PRIVATE / "guest-key.pem")
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    threading.Thread(target=expire_idle, daemon=True).start()
    server.serve_forever()


if __name__ == "__main__":
    main()
