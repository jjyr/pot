#!/usr/bin/env python3
"""Run the extracted native CLI, bundled Node and isolated HTTP service."""

import json
import os
from pathlib import Path
import signal
import socket
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.request

with tempfile.TemporaryDirectory(prefix="pot-server-smoke-") as temporary:
    root = Path(temporary)
    with tarfile.open(sys.argv[1]) as archive:
        archive.extractall(root, filter="data")
    installation = next(root.glob("pot-go-*"))
    binary = installation / "bin/pot"
    metadata = json.loads((installation / "Resources/update.json").read_text())
    assert metadata["version"] == os.environ["POT_APP_VERSION"], metadata
    version = subprocess.check_output([binary, "--version"], text=True)
    assert version.split()[1] == os.environ["POT_APP_VERSION"], version
    if revision := os.environ.get("POT_BUILD_REVISION"):
        assert f"({revision})" in version, version
    subprocess.run([installation / "Resources/node/bin/node", "--version"], check=True)
    subprocess.run(
        [
            installation / "Resources/node/bin/node",
            installation / "Resources/node/bin/npm",
            "--version",
        ],
        check=True,
    )
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        port = listener.getsockname()[1]
    with (root / "server.log").open("w+") as log:
        process = subprocess.Popen(
            [binary, "serve", "-root", root / "data", "-ui", f"127.0.0.1:{port}"],
            stdout=log,
            stderr=log,
            start_new_session=True,
        )
        try:
            deadline = time.monotonic() + 60
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    raise RuntimeError(f"server exited with {process.returncode}")
                try:
                    with urllib.request.urlopen(f"http://127.0.0.1:{port}/", timeout=2) as response:
                        assert response.status == 200
                        assert b"Pot" in response.read()
                    break
                except (urllib.error.URLError, TimeoutError):
                    time.sleep(0.25)
            else:
                raise RuntimeError("packaged server did not become ready")
        except BaseException:
            log.seek(0)
            print(log.read(), file=sys.stderr)
            raise
        finally:
            if process.poll() is None:
                os.killpg(process.pid, signal.SIGTERM)
            try:
                process.wait(timeout=15)
            except subprocess.TimeoutExpired:
                os.killpg(process.pid, signal.SIGKILL)
                process.wait()
                raise RuntimeError("packaged server failed to stop cleanly")
print("Native CLI, Node and HTTP service smoke passed")
