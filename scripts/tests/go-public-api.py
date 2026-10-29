#!/usr/bin/env python3
"""Verify reusable Go packages from an independent module outside this checkout."""

import os
from pathlib import Path
import shutil
import signal
import subprocess
import sys
import tempfile
import time

ROOT = Path(__file__).resolve().parents[2]
FIXTURE = ROOT / "scripts/tests/testdata/go-public-api"
MODULE = "github.com/jjyr/pot"
DEFAULT_TIMEOUT = 240
BUILD_TIMEOUT = 900


def run(*arguments, cwd, env, phase, timeout=DEFAULT_TIMEOUT, capture=False):
    """Run one subprocess in a new session, logging the phase to stderr.

    Returns captured stdout when ``capture`` is true (otherwise ``None``).
    Raises ``RuntimeError`` with a phase-specific message on launch failure,
    timeout, nonzero exit or cleanup failure; preserves KeyboardInterrupt.
    """
    print(f"[public-api] phase start: {phase} (budget {timeout}s)", file=sys.stderr, flush=True)
    started = time.monotonic()
    try:
        process = subprocess.Popen(
            arguments,
            cwd=cwd,
            env=env,
            text=True,
            start_new_session=True,
            stdout=subprocess.PIPE if capture else None,
            stderr=None,
        )
    except OSError as error:
        print(
            f"[public-api] phase {phase!r} failed to launch: {error}",
            file=sys.stderr,
            flush=True,
        )
        raise RuntimeError(f"phase {phase!r} failed to start: {error}") from error

    try:
        try:
            stdout, _ = process.communicate(timeout=timeout)
        except subprocess.TimeoutExpired as error:
            print(
                f"[public-api] phase {phase!r} timed out after {timeout}s",
                file=sys.stderr,
                flush=True,
            )
            _stop(process, phase)
            raise RuntimeError(f"phase {phase!r} timed out after {timeout}s") from error
        except BaseException:
            _stop(process, phase)
            raise
        if process.returncode != 0:
            raise RuntimeError(f"phase {phase!r} failed with exit code {process.returncode}")
    finally:
        for pipe in (process.stdout, process.stderr):
            if pipe is not None:
                pipe.close()
    print(
        f"[public-api] phase complete: {phase!r} (elapsed {time.monotonic() - started:.2f}s)",
        file=sys.stderr,
        flush=True,
    )
    return stdout


def _stop(process, phase):
    # Kill the whole session's process group, including compiler/test children.
    # Do not communicate again: an escaped child could retain a captured pipe.
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    except OSError as error:
        raise RuntimeError(f"phase {phase!r} process-group cleanup failed: {error}") from error
    try:
        process.wait(timeout=10)
    except subprocess.TimeoutExpired as error:
        raise RuntimeError(f"phase {phase!r} parent failed to reap after 10s") from error


def run_consumer_tests(consumer, temporary, env):
    compile_dir = temporary / "compiled-tests"
    compile_dir.mkdir()

    # The old combined command shared one outer budget for compilation and tests.
    # Warm the race build cache without executing tests; keep the Go driver below
    # so package discovery, vet, test execution and the 90s runtime limit remain.
    run(
        "go",
        "test",
        "-c",
        "-mod=mod",
        "-race",
        "-o",
        str(compile_dir),
        "./...",
        cwd=consumer,
        env=env,
        phase="race compilation",
        timeout=BUILD_TIMEOUT,
    )
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
        phase="race tests",
        timeout=DEFAULT_TIMEOUT,
    )


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
            phase="worker build",
            timeout=BUILD_TIMEOUT,
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
            phase="module setup",
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
            phase="dependency inspection",
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
        run_consumer_tests(consumer, temporary, env)
    print("Independent Go module: public APIs, worker isolation and dependency boundaries passed")


if __name__ == "__main__":
    main()
