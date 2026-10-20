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


def install(home, binary, build=True, codex=None):
    destination = home / ".codex/plugins/pot"
    marketplace = home / ".agents/plugins/marketplace.json"
    config = home / ".config/pot-codex/bridge.json"
    if any(
        p.is_symlink()
        for p in [
            destination,
            *destination.parents,
            marketplace,
            *marketplace.parents,
            config,
            *config.parents,
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
    mcp = json.loads(desired[".mcp.json"])
    mcp["mcpServers"]["pot"]["command"] = str(destination / "bin/pot-codex-bridge")
    mcp["mcpServers"]["pot"]["args"] = [
        "host",
        "--root",
        str(home / ".config/pot-codex/agent"),
    ]
    desired[".mcp.json"] = (json.dumps(mcp, indent=2) + "\n").encode()
    # Protect edits and unmanaged packages; local-pair.json belongs to the owner.
    for relative in set(desired) | set(previous) | {"bin/pot-codex-bridge", ".pot-install.json"}:
        target = destination / relative
        if any(p.is_symlink() for p in [target, *target.parents] if p != home.parent):
            raise ValueError(f"Refusing symlink: {relative}")
        if (
            relative != ".pot-install.json"
            and target.exists()
            and previous.get(relative) != digest(target)
        ):
            raise ValueError(f"Existing local change retained: {relative}")
    if build:
        go = shutil.which("go")
        if not go:
            raise ValueError(
                "Go is required to build the bridge; use mise exec -- python3 integrations/codex/install.py or --binary PATH"
            )
        subprocess.run(
            [go, "build", "-mod=readonly", "-o", str(binary), "./cmd/pot-codex-bridge"],
            cwd=REPO,
            check=True,
        )
    if not binary.is_file():
        raise ValueError("Bridge binary missing; build it or pass --binary PATH")
    desired["bin/pot-codex-bridge"] = binary.read_bytes()
    target_binary = destination / "bin/pot-codex-bridge"
    if target_binary.exists() and previous.get("bin/pot-codex-bridge") != digest(target_binary):
        raise ValueError("Locally modified bridge binary retained")
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
    if not config.exists():
        atomic(
            config,
            json.dumps(
                {
                    "listen": "127.0.0.1:8791",
                    "database": "bridge.sqlite",
                    "codex": codex or shutil.which("codex") or "codex",
                    "workspaces": {},
                },
                indent=2,
            )
            + "\n",
        )
    if not codex and not shutil.which("codex"):
        print(
            "Codex CLI is missing from PATH: install/login locally on B before running the Bridge."
        )
    print(f"Installed local plugin source: {destination}")
    print("Local source registered; enable Pot with a supported Codex CLI or plugin directory.")
    print(
        "The message Bridge starts automatically when Codex enables the plugin. Open its connections page with pot_open_connections; no network/system configuration was changed."
    )
    return destination


def activate(home, codex_home, codex):
    # This is the documented CLI in Codex 0.163.0. Probe support rather than
    # inventing commands or silently rewriting an older config.toml.
    env = {**os.environ, "HOME": str(home), "CODEX_HOME": str(codex_home)}
    probe = subprocess.run(
        [codex, "plugin", "add", "--help"], env=env, capture_output=True, timeout=15
    )
    if probe.returncode != 0:
        print(
            "This Codex CLI has no plugin add command. Install Pot through its plugin directory; see the integration README for the local MCP fallback."
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
        "Pot plugin installed and enabled in Codex; start a new chat. Pairing/connection is still separate."
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
    parser.add_argument("--binary", type=Path, help="Use an already built bridge executable")
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
            binary = args.binary.resolve() if args.binary else Path(directory) / "pot-codex-bridge"
            home = args.home.resolve()
            codex = args.codex or shutil.which("codex")
            install(home, binary, build=args.binary is None, codex=codex)
            if codex and not args.source_only:
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
