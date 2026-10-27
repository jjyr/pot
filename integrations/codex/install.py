#!/usr/bin/env python3
"""Install Pot's local Codex marketplace package without changing existing work."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

REPO = Path(__file__).resolve().parents[2]
SOURCE = REPO / "integrations/codex/plugin"


def load(path):
    return json.loads(path.read_text())


def atomic(path, data):
    path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, temp = tempfile.mkstemp(prefix=".pot-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as stream:
            stream.write(data)
        os.replace(temp, path)
    finally:
        Path(temp).unlink(missing_ok=True)


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def install(home, binary=None, build=False, receiver=False):
    if receiver:
        return install_receiver(home, binary, build)
    destination = home / ".codex/plugins/pot"
    marketplace = home / ".agents/plugins/marketplace.json"
    if any(
        p.is_symlink()
        for p in [
            destination,
            *destination.parents,
            marketplace,
            *marketplace.parents,
        ]
        if p != home.parent
    ):
        raise ValueError("Refusing a symlink at the installation destination")
    catalog = load(marketplace) if marketplace.exists() else {"name": "personal", "plugins": []}
    if not isinstance(catalog.get("plugins"), list):
        raise ValueError("Invalid existing marketplace; repair it before installation")
    old = {entry["name"]: entry for entry in catalog["plugins"]}
    expected = {
        "name": "pot",
        "source": {"source": "local", "path": "./.codex/plugins/pot"},
        "policy": {"installation": "AVAILABLE", "authentication": "ON_INSTALL"},
        "category": "Productivity",
    }
    if "pot" in old and old["pot"] != expected:
        raise ValueError("A different pot marketplace entry exists; review it before replacing")
    receipt = destination / ".pot-install.json"
    previous = load(receipt) if receipt.exists() else {}
    if not isinstance(previous, dict) or any(
        Path(key).is_absolute() or ".." in Path(key).parts for key in previous
    ):
        raise ValueError("Invalid installation receipt")
    desired = {
        str(path.relative_to(SOURCE)): path.read_bytes()
        for path in SOURCE.rglob("*")
        if path.is_file()
    }
    # Protect edits and unmanaged packages; private Agent Link data belongs to the owner.
    for relative in set(desired) | set(previous) | {"bin/pot-codex-agent", ".pot-install.json"}:
        target = destination / relative
        if any(p.is_symlink() for p in [target, *target.parents] if p != home.parent):
            raise ValueError(f"Refusing symlink: {relative}")
        if (
            relative != ".pot-install.json"
            and target.exists()
            and previous.get(relative) != digest(target)
        ):
            raise ValueError(f"Existing local change retained: {relative}")
    for relative, data in desired.items():
        path = destination / relative
        path.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        fd, temp = tempfile.mkstemp(prefix=".pot-", dir=path.parent)
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
        os.chmod(temp, 0o700 if relative.startswith("bin/") else 0o600)
        os.replace(temp, path)
    for relative in previous.keys() - desired.keys():
        (destination / relative).unlink(missing_ok=True)
    atomic(
        receipt,
        json.dumps({relative: digest(destination / relative) for relative in desired}, indent=2)
        + "\n",
    )
    old["pot"] = expected
    catalog["plugins"] = list(old.values())
    atomic(marketplace, json.dumps(catalog, indent=2) + "\n")
    print(f"Installed optional setup helper: {destination}")
    print("Independent MCP connections and receiver processes have separate lifecycles.")
    return destination


def install_receiver(home, binary, build):
    destination = home / ".local/bin/pot-codex-agent"
    if any(p.is_symlink() for p in [destination, *destination.parents]):
        raise ValueError("Refusing symlink at receiver destination")
    if build:
        go = shutil.which("go")
        if not go:
            raise ValueError("Go is required to build the receiver; use --receiver --binary PATH")
        subprocess.run(
            [go, "build", "-mod=readonly", "-o", str(binary), "./cmd/pot-codex-agent"],
            cwd=REPO,
            check=True,
        )
    if binary is None or not binary.is_file():
        raise ValueError("Receiver binary missing; build it or pass --binary PATH")
    if destination.exists() and destination.read_bytes() != binary.read_bytes():
        raise ValueError("Receiver executable exists; explicitly replace it while stopped")
    destination.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=".pot-", dir=destination.parent)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(binary.read_bytes())
        os.chmod(temporary, 0o700)
        os.replace(temporary, destination)
    finally:
        Path(temporary).unlink(missing_ok=True)
    print(f"Installed independent receiver executable: {destination}")
    print("Connect and start it explicitly; the optional helper was not installed.")
    return destination


def activate(home, codex_home, codex):
    # This is the documented CLI in Codex 0.163.0. Probe support rather than
    # inventing commands or silently rewriting an older config.toml.
    env = {**os.environ, "CODEX_HOME": str(codex_home)}
    probe = subprocess.run(
        [codex, "plugin", "add", "--help"], env=env, capture_output=True, timeout=15
    )
    if probe.returncode != 0:
        print(
            "This Codex CLI has no plugin add command. Install Pot through its plugin directory; see the integration README for independent remote MCP setup."
        )
        return False
    catalog = load(home / ".agents/plugins/marketplace.json")
    subprocess.run(
        [codex, "plugin", "add", "pot@" + catalog["name"], "--json"],
        env=env,
        check=True,
        timeout=30,
    )
    check = subprocess.run(
        [codex, "plugin", "list", "--json"],
        env=env,
        check=True,
        capture_output=True,
        text=True,
        timeout=30,
    )
    installed = json.loads(check.stdout)["installed"]
    if not any(
        item.get("pluginId") == "pot@" + catalog["name"] and item.get("enabled")
        for item in installed
    ):
        raise ValueError("Codex did not report the Pot plugin installed/enabled")
    print(
        "Pot plugin installed and enabled in Codex; start a new chat. Independent MCP setup is still separate."
    )
    return True


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--home",
        type=Path,
        default=Path.home(),
        help="Personal configuration home (use an isolated directory for testing)",
    )
    parser.add_argument(
        "--receiver",
        action="store_true",
        help="Install the independent receiver instead of the optional helper; never start it",
    )
    parser.add_argument(
        "--binary", type=Path, help="Built receiver executable (requires --receiver)"
    )
    parser.add_argument(
        "--codex", help="Codex executable for supported plugin activation (defaults to PATH)"
    )
    parser.add_argument("--codex-home", type=Path, help="Codex configuration/cache directory")
    parser.add_argument(
        "--source-only",
        action="store_true",
        help="Register local source without activating in Codex",
    )
    args = parser.parse_args()
    try:
        with tempfile.TemporaryDirectory(prefix="pot-codex-install-") as directory:
            binary = args.binary.resolve() if args.binary else Path(directory) / "pot-codex-agent"
            home = args.home.resolve()
            codex = args.codex or shutil.which("codex")
            if args.binary and not args.receiver:
                raise ValueError("--binary requires --receiver")
            install(
                home,
                binary,
                build=args.receiver and args.binary is None,
                receiver=args.receiver,
            )
            if codex and not args.source_only and not args.receiver:
                codex_home = args.codex_home or (
                    Path(os.environ["CODEX_HOME"])
                    if os.environ.get("CODEX_HOME")
                    else home / ".codex"
                )
                activate(home, codex_home.resolve(), codex)
    except (OSError, ValueError, subprocess.CalledProcessError, subprocess.TimeoutExpired) as error:
        print(f"Installation stopped: {error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
