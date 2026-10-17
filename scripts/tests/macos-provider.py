#!/usr/bin/env python3
"""Guest protocol regression using local TLS and a fake runner; no VM or model calls."""

import http.client
import socket
import importlib.util
import sys
import json
from pathlib import Path
import sqlite3
import ssl
import subprocess
import tempfile
import threading
from types import SimpleNamespace
import urllib.error

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "plugins/podman/image"))
spec = importlib.util.spec_from_file_location(
    "macos_executor", ROOT / "plugins/apple-vm/bootstrap/executor.py"
)
broker = importlib.util.module_from_spec(spec)
spec.loader.exec_module(broker)


class Runner:
    dispatches = 0

    def __init__(self, _, cwd, policy, role):
        self.cwd, self.policy, self.role = cwd, policy, role
        self.process = SimpleNamespace(poll=lambda: None)

    def execute(self, request):
        if request["operation"] == "inspect":
            return {"result": {"sessions": ["live"], "active": True, "ended": False}, "events": []}
        Runner.dispatches += 1
        return {"result": {"stdout": request["args"]["cmd"]}, "events": []}


with tempfile.TemporaryDirectory(prefix="pot-macos-test-") as temporary:
    directory = Path(temporary)
    assets = directory / "enrollment"
    subprocess.run(
        [
            str(ROOT / "scripts/macos-enrollment"),
            "--guest-ip",
            "192.168.64.3",
            "--output",
            str(assets),
        ],
        check=True,
    )
    broker.PRIVATE = directory / "private"
    broker.PRIVATE.mkdir()
    (broker.PRIVATE / "config.json").write_text(json.dumps({"user": "fixture"}))
    broker.WORK = directory / "workspace"
    broker.WORK.mkdir()
    broker.PREFIX = str(broker.WORK)
    broker.TOKEN = (assets / "executor-token").read_text().strip()
    broker.DB = sqlite3.connect(directory / "operations.sqlite", check_same_thread=False)
    broker.DB.execute("CREATE TABLE operations (id TEXT PRIMARY KEY, input TEXT, result TEXT)")
    broker.Runner = Runner
    broker.terminate = lambda _: None
    server = broker.ThreadingHTTPServer(("127.0.0.1", 0), broker.Handler)
    tls = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls.load_cert_chain(assets / "guest-cert.pem", assets / "guest-key.pem")
    server.socket = tls.wrap_socket(server.socket, server_side=True)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    client = ssl.create_default_context(cafile=str(assets / "guest-ca.pem"))
    base = f"https://192.168.64.3:{server.server_port}"

    class LocalGuestConnection(http.client.HTTPSConnection):
        def connect(self):
            self.sock = client.wrap_socket(
                socket.create_connection(server.server_address, timeout=3),
                server_hostname="192.168.64.3",
            )

    # Validate the enrolled IP identity; a different identity must fail TLS.
    try:
        with client.wrap_socket(
            socket.create_connection(server.server_address, timeout=3),
            server_hostname="192.168.64.4",
        ):
            raise AssertionError("Unexpected guest certificate accepted")
    except ssl.SSLCertVerificationError:
        pass

    def request(path, body=None, token=None):
        headers = {} if token is None else {"Authorization": "Bearer " + token}
        connection = LocalGuestConnection(
            "192.168.64.3", server.server_port, context=client, timeout=3
        )
        try:
            connection.request(
                "GET" if body is None else "POST",
                path,
                body=None if body is None else json.dumps(body).encode(),
                headers=headers,
            )
            response = connection.getresponse()
            if response.status >= 400:
                response.read()
                raise urllib.error.HTTPError(
                    base + path, response.status, "rejected", response.headers, None
                )
            return json.loads(response.read())
        finally:
            connection.close()

    def rejected(path, body=None, token=None):
        try:
            request(path, body, token)
            raise AssertionError("Unauthorized/conflicting operation accepted")
        except urllib.error.HTTPError as error:
            assert error.code in (403, 409), error

    try:
        rejected("/health")
        rejected("/health", token="wrong")
        assert request("/health", token=broker.TOKEN)["protocol"] == "pot-macos-executor-v3"
        rejected("/desktop-stream")
        rejected("/desktop-ready", token="wrong")
        connection = LocalGuestConnection(
            "192.168.64.3", server.server_port, context=client, timeout=3
        )
        connection.request(
            "GET", "/desktop-ready", headers={"Authorization": "Bearer " + broker.TOKEN}
        )
        response = connection.getresponse()
        assert response.status == 503
        response.read()
        connection.close()
        # Exercise the authenticated TLS upgrade against a private fake RFB listener.
        listener = socket.socket()
        listener.bind(("127.0.0.1", 0))
        listener.listen()
        original_connect = socket.create_connection

        def desktop_connect(address, *args, **kwargs):
            return original_connect(
                listener.getsockname() if address == ("127.0.0.1", 5900) else address,
                *args,
                **kwargs,
            )

        socket.create_connection = desktop_connect
        (broker.PRIVATE / "config.json").write_text(
            json.dumps({"user": "fixture", "desktop_rfb": "loopback-none-3.8"})
        )

        def rfb_service():
            while True:
                conn, _ = listener.accept()
                with conn:
                    try:
                        conn.sendall(b"RFB 003.008\n")
                        data = conn.recv(64)
                    except (BrokenPipeError, ConnectionResetError):
                        # Readiness probes connect and close without an RFB session.
                        continue
                    if data:
                        conn.sendall(data)
                        return

        rfb_thread = threading.Thread(target=rfb_service, daemon=True)
        rfb_thread.start()
        try:
            assert request("/desktop-ready", token=broker.TOKEN)["ready"]
            with client.wrap_socket(
                original_connect(server.server_address, timeout=3), server_hostname="192.168.64.3"
            ) as tunnel:
                tunnel.sendall(
                    (
                        "GET /desktop-stream HTTP/1.1\r\nHost: guest\r\nAuthorization: Bearer "
                        + broker.TOKEN
                        + "\r\nConnection: Upgrade\r\nUpgrade: pot-rfb\r\n\r\n"
                    ).encode()
                )
                headers = b""
                while not headers.endswith(b"\r\n\r\n"):
                    headers += tunnel.recv(1)
                assert b"101 Switching Protocols" in headers
                assert tunnel.recv(12) == b"RFB 003.008\n"
                tunnel.sendall(b"private-rfb-test")
                assert tunnel.recv(64) == b"private-rfb-test"
            rfb_thread.join(timeout=3)
            assert not rfb_thread.is_alive()
        finally:
            socket.create_connection = original_connect
            listener.close()
        body = {
            "id": "one",
            "scope": "scope",
            "cwd": str(broker.WORK),
            "role": "main",
            "policy": {},
            "operation": "exec_command",
            "args": {"cmd": "hello"},
        }
        assert request("/execute", body, broker.TOKEN) == request("/execute", body, broker.TOKEN)
        assert Runner.dispatches == 1
        rejected("/inspect", {"id": "scope"})
        assert request("/inspect", {"id": "scope"}, broker.TOKEN)["sessions"] == ["live"]
        rejected("/execute", {**body, "args": {"cmd": "changed"}}, broker.TOKEN)
        rejected("/execute", {**body, "id": "two", "scope": "foreign"}, broker.TOKEN)
        for bad in (str(directory), str(broker.WORK / "../escape")):
            rejected("/execute", {**body, "id": "escape", "cwd": bad}, broker.TOKEN)
        closure = request("/finish", {"id": "scope"}, broker.TOKEN)
        assert closure["scope_closed"] is True and closure["settled"] is False
        assert closure["process_containment"] is False
        assert request("/inspect", {"id": "scope"}, broker.TOKEN)["ended"] is True
        rejected("/execute", {**body, "id": "late"}, broker.TOKEN)
        broker.DB.execute(
            "INSERT INTO operations VALUES (?,?,NULL)",
            ("unknown", json.dumps({**body, "id": "unknown"}, sort_keys=True)),
        )
        broker.DB.commit()
        rejected("/execute", {**body, "id": "unknown"}, broker.TOKEN)
        assert Runner.dispatches == 1
        failed_runner = Runner("failure", broker.WORK, {}, "main")
        broker.RUNNERS["failure"] = failed_runner
        original = broker.terminate

        def fail_termination(_):
            raise RuntimeError("Termination unconfirmed")

        broker.terminate = fail_termination
        try:
            broker.cancel("failure")
            raise AssertionError("Failed termination claimed scope closure")
        except RuntimeError:
            assert broker.RUNNERS["failure"] is failed_runner
        finally:
            broker.terminate = original
        assert broker.cancel("failure")["scope_closed"] is True
        print(
            "macOS guest TLS/auth, receipts/no-replay, owner/workspace boundaries and authenticated desktop tunnel: passed"
        )
    finally:
        server.shutdown()
        server.server_close()
        thread.join()
