#!/usr/bin/env python3
"""Synthetic stopped Helper IPC for a disposable UI-check App bundle. No VM."""

import json
import signal
import socket
import sys
from pathlib import Path

root = Path(sys.argv[1])
endpoint = root / "product/runtime.json"
if json.loads(endpoint.read_text()).get("boot_id") != "ui-fixture":
    raise SystemExit("UI Helper requires a fixture-owned root")
path = root / "helper.sock"
if path.exists():
    raise SystemExit("Refusing to replace an existing Helper socket")
server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
server.bind(str(path))
server.listen()


def stop(*_args):
    server.close()
    path.unlink(missing_ok=True)
    raise SystemExit(0)


signal.signal(signal.SIGTERM, stop)
signal.signal(signal.SIGINT, stop)
while True:
    connection, _ = server.accept()
    with connection:
        request = json.loads(connection.makefile("rb").readline(65536))
        if request.get("method") == "status":
            response = dict(result=dict(phase="stopped", generation=1, ip=""))
        else:
            response = dict(error="UI fixture supports status only")
        connection.sendall(json.dumps(response).encode() + b"\n")
