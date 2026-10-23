#!/usr/bin/env python3
"""Launch the extracted macOS App with its disposable preview fixture."""

import os
from pathlib import Path
import plistlib
import subprocess
import sys
import tempfile
import time

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from macos_release import verify_resources, verify_signatures  # noqa: E402

with tempfile.TemporaryDirectory(prefix="pot-macos-smoke-") as temporary:
    root = Path(temporary)
    subprocess.run(["ditto", "-x", "-k", sys.argv[1], str(root)], check=True)
    contents = root / "Pot.app/Contents"
    info = plistlib.loads((contents / "Info.plist").read_bytes())
    assert info["CFBundleShortVersionString"] == os.environ["POT_APP_VERSION"]
    verify_resources(contents / "Resources", os.environ["POT_APP_VERSION"], "app")
    if mode := os.environ.get("POT_MACOS_RELEASE_MODE"):
        verify_signatures(root / "Pot.app", mode)
    version = subprocess.check_output([contents / "MacOS/pot", "--version"], text=True)
    assert version.split()[1] == os.environ["POT_APP_VERSION"], version
    if revision := os.environ.get("POT_BUILD_REVISION"):
        assert f"({revision})" in version, version
    subprocess.run([contents / "Resources/node/bin/node", "--version"], check=True)
    subprocess.run(
        [
            contents / "Resources/node/bin/node",
            contents / "Resources/node/bin/npm",
            "--version",
        ],
        check=True,
    )
    with (root / "preview.log").open("w+") as log:
        process = subprocess.Popen([contents / "MacOS/pot-app", "-preview"], stdout=log, stderr=log)
        try:
            deadline = time.monotonic() + 10
            while time.monotonic() < deadline:
                if process.poll() is not None:
                    log.seek(0)
                    raise RuntimeError(
                        f"App preview exited early ({process.returncode}): {log.read()}"
                    )
                time.sleep(0.25)
        finally:
            if process.poll() is None:
                process.terminate()
            process.wait(timeout=15)
print("macOS bundle metadata, CLI, Node and native preview launch passed")
