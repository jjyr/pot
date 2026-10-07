#!/usr/bin/env python3
"""Exercise the real native Helper's private IPC without installation media."""

import argparse
import json
from pathlib import Path
import socket
import subprocess
import tempfile
import time

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--helper", type=Path, required=True)
args = parser.parse_args()
helper = args.helper.resolve(strict=True)
with tempfile.TemporaryDirectory(prefix="pot-mac-helper-", dir="/tmp") as temporary:
    root = Path(temporary)
    process = subprocess.Popen(
        [str(helper), str(root)], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE
    )
    endpoint = root / "macos-helper.sock"
    try:
        for _ in range(100):
            assert process.poll() is None, "Helper exited before IPC readiness"
            if endpoint.exists():
                break
            time.sleep(0.05)
        else:
            raise AssertionError("Helper IPC unavailable")

        def rpc(method, version=1):
            with socket.socket(socket.AF_UNIX) as stream:
                stream.settimeout(5)
                stream.connect(str(endpoint))
                stream.sendall(
                    (
                        json.dumps({"version": version, "id": "probe", "method": method}) + "\n"
                    ).encode()
                )
                line = stream.makefile().readline()
                return json.loads(line) if line else None

        assert rpc("status")["result"]["phase"] == "stopped"
        assert rpc("stop")["result"]["phase"] == "stopped"
        assert rpc("prepare").get("error"), "Missing platform accepted"
        assert rpc("unsupported").get("error")
        assert rpc("status", version=99) is None
        with socket.socket(socket.AF_UNIX) as stream:
            stream.settimeout(5)
            stream.connect(str(endpoint))
            stream.sendall(b"x" * 65538 + b"\n")
            try:
                assert not stream.recv(1024)
            except ConnectionResetError:
                pass
        duplicate = subprocess.run([str(helper), str(root)], capture_output=True, timeout=5)
        assert duplicate.returncode == 3, "Helper accepted a second controller"
        assert endpoint.stat().st_mode & 0o077 == 0
        print(
            "Native macOS Helper IPC, bounded protocol, owner lock and missing-platform rejection: passed; VM boot not tested"
        )
    finally:
        process.terminate()
        process.communicate(timeout=5)
