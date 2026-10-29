#!/usr/bin/env python3
"""Run real OS sandbox tests against integrity-locked packaged SDK assets."""

import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
from pot_packaging import prepare_sandbox  # noqa: E402

sdk = ROOT / ".local/pot/sandbox-tests"
prepare_sandbox(sdk)
subprocess.run(
    [
        "go",
        "test",
        "-race",
        "-count=1",
        "-v",
        "./pkg/sandbox",
        "./pkg/executor",
        "-run",
        "^TestReal",
    ],
    cwd=ROOT,
    env={**os.environ, "POT_SANDBOX_INTEGRATION": "1", "POT_SANDBOX_SDK": str(sdk)},
    check=True,
)
subprocess.run(
    [
        "go",
        "test",
        "-race",
        "-count=1",
        "-v",
        "./internal/control",
        "-run",
        "^TestReal(UnixSocketRequiresHostApproval|MainSkillSandboxResources|TemporaryWorkspacePermissions)$",
    ],
    cwd=ROOT,
    env={**os.environ, "POT_SANDBOX_INTEGRATION": "1", "POT_SANDBOX_SDK": str(sdk)},
    check=True,
)
