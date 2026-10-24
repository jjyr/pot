#!/usr/bin/env python3
"""Qualify compiled sandbox tests in a disposable Linux namespace-capable host."""

import os
import shutil
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
from pot_packaging import node, prepare_sandbox  # noqa: E402


def run(args, **kwargs):
    subprocess.run([str(arg) for arg in args], cwd=ROOT, check=True, **kwargs)


architecture = subprocess.check_output(
    ["docker", "info", "--format", "{{.Architecture}}"], text=True
).strip()
goarch = {"aarch64": "arm64", "arm64": "arm64", "x86_64": "amd64", "amd64": "amd64"}[architecture]
bundle = ROOT / ".local/sandbox-linux"
bundle.mkdir(parents=True, exist_ok=True)
shutil.copy2(ROOT / "plugins/computer-use/browser.mjs", bundle / "browser.mjs")
prepare_sandbox(bundle / "sandbox")
if (bundle / "node").exists():
    shutil.rmtree(bundle / "node")
shutil.copytree(node("linux-" + goarch), bundle / "node")
packages = {
    "executor": "pkg/executor",
    "sandbox": "pkg/sandbox",
    "processscope": "pkg/processscope",
    "control": "internal/control",
}
for package, source in packages.items():
    (bundle / source).mkdir(parents=True, exist_ok=True)
    run(
        ["go", "test", "-c", "-o", bundle / f"{package}.test", f"./{source}"],
        env={**os.environ, "GOOS": "linux", "GOARCH": goarch, "CGO_ENABLED": "0"},
    )
run(
    [
        "docker",
        "build",
        "-f",
        ROOT / "scripts/tests/Dockerfile.sandbox",
        "-t",
        "pot-sandbox-qualification",
        ROOT / "scripts/tests",
    ]
)
# Only test assets are mounted, read-only. Docker namespace restrictions are
# relaxed for this disposable test host, not in Pot's production SRT policy.
for package in ("sandbox", "processscope", "executor", "control"):
    run(
        [
            "docker",
            "run",
            "--rm",
            "--cap-add",
            "SYS_ADMIN",
            "--security-opt",
            "seccomp=unconfined",
            "--security-opt",
            "apparmor=unconfined",
            "--security-opt",
            "systempaths=unconfined",
            "--mount",
            f"type=bind,src={bundle},dst=/suite,readonly",
            "--workdir",
            f"/suite/{packages[package]}",
            "--env",
            "PATH=/suite/node/bin:/usr/local/bin:/usr/bin:/bin",
            "--env",
            "POT_SANDBOX_INTEGRATION=1",
            "--env",
            "POT_SANDBOX_SDK=/suite/sandbox",
            "--env",
            "POT_SANDBOX_BROWSER_SOURCE=/suite/browser.mjs",
            "pot-sandbox-qualification",
            f"/suite/{package}.test",
            "-test.v",
            "-test.run",
            "^TestReal(UnixSocketRequiresHostApproval|TaskWorktreePreparation)$"
            if package == "control"
            else "TestReal|TestDetached|TestCancellation",
            "-test.timeout",
            "90s",
        ]
    )
