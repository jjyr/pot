#!/usr/bin/env python3
"""Original AGY CLI smoke in a network-disabled guest with a fresh empty HOME."""

import json
import os
from pathlib import Path
import signal
import subprocess
import tempfile

with tempfile.TemporaryDirectory(prefix="pot-agy-cli-") as temp:
    env = {"PATH": os.environ["PATH"], "HOME": temp}
    cli = "/usr/local/bin/agy"
    help_result = subprocess.run(
        [cli, "--help"], env=env, capture_output=True, timeout=20, check=False
    )
    help_text = (help_result.stdout + help_result.stderr).decode(errors="replace")
    assert help_result.returncode == 0
    assert all(
        flag in help_text
        for flag in ("--conversation", "--print", "--output-format", "stream-json", "--model")
    )
    # Empty credentials must fail without opening a login TTY.
    # The runner disables VM networking; this cannot reach a remote model.
    child = subprocess.Popen(
        [
            cli,
            "--print",
            "--input-format",
            "text",
            "--output-format",
            "stream-json",
            "--disable-slash-commands",
        ],
        env=env,
        cwd=temp,
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        start_new_session=True,
    )
    try:
        stdout, stderr = child.communicate(b"POT_OFFLINE_SMOKE", timeout=20)
    finally:
        try:
            os.killpg(child.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        child.wait()
    assert child.returncode != 0, "CLI unexpectedly accepted a prompt with empty credentials"
    assert len(stdout) <= 4 * 1024 * 1024 and len(stderr) <= 65536
    # Do not export CLI diagnostics, credentials or its generated files.
    assert Path(temp).is_dir()
    print(
        json.dumps(
            {
                "official_cli_help": True,
                "empty_home_failure_settled": True,
                "model_calls": 0,
                "account_acceptance": False,
                "role_isolation_acceptance": False,
            }
        )
    )
