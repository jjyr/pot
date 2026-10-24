#!/usr/bin/env python3
"""Local core tests, complete normal tests and conservative race selection."""

import argparse
from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[1]
MANIFEST = ROOT / "scripts/go-test-normal-only.json"
CORE_MANIFEST = ROOT / "scripts/go-test-core.json"
TEST_FUNCTION = re.compile(r"^func ((?:Test|Fuzz|Example)\w*)\s*\(", re.MULTILINE)


def test_names(paths):
    return {
        name
        for path in paths
        for name in TEST_FUNCTION.findall(path.read_text())
        if name != "TestMain"
    }


def normal_only(manifest):
    exclusions = {}
    for group in manifest["normal_only"]:
        if not group["reason"].strip() or not group["tests"]:
            raise ValueError("each normal-only group needs a reason and tests")
        package = group["package"]
        if not re.fullmatch(r"\./[a-zA-Z0-9_/.-]+", package) or ".." in package:
            raise ValueError(f"invalid package: {package}")
        known = test_names((ROOT / package).glob("*_test.go"))
        excluded = exclusions.setdefault(package, set())
        for name in group["tests"]:
            if name not in known:
                raise ValueError(f"stale normal-only test: {package}:{name}")
            if name in excluded:
                raise ValueError(f"duplicate normal-only test: {package}:{name}")
            excluded.add(name)
    return exclusions


def decode_packages(output):
    decoder = json.JSONDecoder()
    packages = []
    rest = output.strip()
    while rest:
        package, end = decoder.raw_decode(rest)
        packages.append(package)
        rest = rest[end:].lstrip()
    return packages


def race_plan(packages, exclusions):
    """Unlisted packages/tests always get race, including new tests in old files."""
    full, selected = [], []
    for package in packages:
        directory = Path(package["Dir"])
        name = "./" + directory.relative_to(ROOT).as_posix()
        files = package.get("TestGoFiles", []) + package.get("XTestGoFiles", [])
        if not files:
            continue
        tests = test_names(directory / file for file in files)
        excluded = tests & exclusions.get(name, set())
        if not excluded:
            full.append(name)
        elif remaining := tests - excluded:
            selected.append((name, sorted(remaining)))
    return sorted(full), sorted(selected)


def core_plan(packages, manifest):
    """Explicit local coverage; reject removed or platform-inactive selections."""
    active = {
        "./" + Path(package["Dir"]).relative_to(ROOT).as_posix(): package for package in packages
    }
    full = manifest["packages"]
    selected = manifest["selected"]
    names = full + list(selected)
    if not names or len(names) != len(set(names)):
        raise ValueError("core packages must be nonempty and unique")
    for name in names:
        if name not in active:
            raise ValueError(f"unknown core package: {name}")
        package = active[name]
        files = package.get("TestGoFiles", []) + package.get("XTestGoFiles", [])
        tests = test_names(Path(package["Dir"]) / file for file in files)
        if name in selected:
            wanted = selected[name]
            if not wanted or len(wanted) != len(set(wanted)):
                raise ValueError(f"core test selection must be nonempty and unique: {name}")
            for test in wanted:
                if test not in tests:
                    raise ValueError(f"unknown core test: {name}:{test}")
        elif not tests:
            raise ValueError(f"core package has no active tests: {name}")
    return full, sorted(selected.items())


def selected_commands(command, full, selected):
    commands = [command + full] if full else []
    for package, tests in selected:
        pattern = "^(" + "|".join(re.escape(test) for test in tests) + ")$"
        commands.append(command + [package, "-run", pattern])
    return commands


def run(command):
    print("+ " + shlex.join(command), file=sys.stderr, flush=True)
    started = time.monotonic()
    result = subprocess.run(command, cwd=ROOT, check=False)
    print(
        f"elapsed {time.monotonic() - started:.3f}s; exit {result.returncode}",
        file=sys.stderr,
        flush=True,
    )
    return result.returncode


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=["core", "normal", "race", "race-all"])
    parser.add_argument("--timeout", default=os.environ.get("POT_GO_TEST_TIMEOUT", "20m"))
    parser.add_argument("--count", type=int)
    parser.add_argument("--json", action="store_true")
    parser.add_argument(
        "--list", action="store_true", help="print test plan without executing tests"
    )
    args = parser.parse_args()
    exclusions = normal_only(json.loads(MANIFEST.read_text()))
    command = ["go", "test", f"-timeout={args.timeout}"]
    if args.count is not None:
        command.append(f"-count={args.count}")
    if args.json:
        command.append("-json")
    if args.mode == "normal":
        commands = [command + ["./..."]]
    elif args.mode == "race-all":
        commands = [command + ["-race", "./..."]]
    elif args.mode == "core":
        packages = decode_packages(
            subprocess.check_output(["go", "list", "-json", "./..."], cwd=ROOT, text=True)
        )
        full, selected = core_plan(packages, json.loads(CORE_MANIFEST.read_text()))
        commands = selected_commands(command, full, selected)
    else:
        packages = decode_packages(
            subprocess.check_output(["go", "list", "-race", "-json", "./..."], cwd=ROOT, text=True)
        )
        full, selected = race_plan(packages, exclusions)
        commands = selected_commands(command + ["-race"], full, selected)
    if args.list:
        for command in commands:
            print(shlex.join(command))
        return
    if not commands:
        return
    if args.mode == "core":
        # Bound independent integration groups instead of serializing their I/O.
        with ThreadPoolExecutor(max_workers=4) as pool:
            results = list(pool.map(run, commands))
    else:
        results = [run(command) for command in commands]
    raise SystemExit(next((code for code in results if code), 0))


if __name__ == "__main__":
    main()
