#!/usr/bin/env python3
"""Verify reusable Go packages from an independent module outside this checkout."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[2]
FIXTURE = ROOT / "scripts/tests/testdata/go-public-api"
MODULE = "github.com/jjyr/pot"


def run(*arguments, cwd, env, capture=False):
    result = subprocess.run(
        arguments,
        cwd=cwd,
        env=env,
        check=True,
        text=True,
        stdout=subprocess.PIPE if capture else None,
        timeout=240,
    )
    return result.stdout


def main():
    env = dict(os.environ, GOWORK="off", GOTOOLCHAIN="local")
    with tempfile.TemporaryDirectory(prefix="pot-public-api-") as directory:
        temporary = Path(directory).resolve()
        if temporary.is_relative_to(ROOT):
            raise RuntimeError("external consumer must run outside the repository")
        worker = temporary / "pot-composition-worker"
        run(
            "go",
            "build",
            "-mod=readonly",
            "-o",
            str(worker),
            "./cmd/pot-composition-worker",
            cwd=ROOT,
            env=env,
        )
        consumer = temporary / "consumer"
        shutil.copytree(FIXTURE, consumer)
        shutil.copy2(ROOT / "go.sum", consumer / "go.sum")
        run(
            "go",
            "mod",
            "edit",
            f"-replace={MODULE}={ROOT}",
            cwd=consumer,
            env=env,
        )
        dependencies = run(
            "go",
            "list",
            "-mod=mod",
            "-deps",
            f"{MODULE}/pkg/agent",
            f"{MODULE}/pkg/modelprotocol",
            cwd=consumer,
            env=env,
            capture=True,
        ).splitlines()
        forbidden = (
            f"{MODULE}/internal/",
            f"{MODULE}/pkg/runtime",
            f"{MODULE}/pkg/storage",
            "github.com/wailsapp/",
            "modernc.org/sqlite",
            "tailscale.com/",
        )
        violations = [name for name in dependencies if name.startswith(forbidden)]
        if violations:
            raise RuntimeError(f"agent/model protocol imported product dependencies: {violations}")
        env["POT_PUBLIC_API_WORKER"] = str(worker)
        run(
            "go",
            "test",
            "-mod=mod",
            "-race",
            "-count=1",
            "-timeout=90s",
            "./...",
            cwd=consumer,
            env=env,
        )
    print("Independent Go module: public APIs, worker isolation and dependency boundaries passed")


if __name__ == "__main__":
    main()
