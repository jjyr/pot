#!/usr/bin/env python3
"""Install the native Antigravity plugin without replacing local modifications."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import shlex
import shutil
import subprocess
import tempfile

REPO = Path(__file__).resolve().parents[2]
SOURCE = REPO / "integrations/antigravity/plugin"


def digest(data):
    return hashlib.sha256(data).hexdigest()


def install(home, binary, agy="agy", build=True, activate=True):
    destination = home / ".config/pot-antigravity/plugin"
    root = home / ".config/pot-antigravity/agent"
    for path in [destination, root]:
        if any(p.is_symlink() for p in [path, *path.parents]):
            raise ValueError("Refusing symlink at installation destination")
    if build:
        subprocess.run(
            ["go", "build", "-mod=readonly", "-o", str(binary), "./cmd/pot-antigravity-agent"],
            cwd=REPO,
            check=True,
        )
    if not binary.is_file():
        raise ValueError("Agent Link executable missing")
    executable = destination / "bin/pot-antigravity-agent"
    desired = {str(p.relative_to(SOURCE)): p.read_bytes() for p in SOURCE.rglob("*") if p.is_file()}
    config = json.loads(desired["mcp_config.json"])
    config["mcpServers"]["pot"].update(command=str(executable), args=["host", "--root", str(root)])
    desired["mcp_config.json"] = (json.dumps(config, indent=2) + "\n").encode()
    desired["hooks.json"] = desired["hooks.json"].replace(
        b"__POT_POLICY_COMMAND__",
        json.dumps(shlex.quote(str(executable)) + " policy")[1:-1].encode(),
    )
    desired["bin/pot-antigravity-agent"] = binary.read_bytes()
    receipt = destination / ".pot-install.json"
    if receipt.is_symlink():
        raise ValueError("Refusing symlink install receipt")
    previous = json.loads(receipt.read_text()) if receipt.exists() else {}
    if not isinstance(previous, dict) or any(
        Path(k).is_absolute() or ".." in Path(k).parts for k in previous
    ):
        raise ValueError("Invalid install receipt")
    for relative in set(previous) | set(desired):
        target = destination / relative
        if any(p.is_symlink() for p in [target, *target.parents]):
            raise ValueError("Refusing symlink in package")
        if target.exists() and previous.get(relative) != digest(target.read_bytes()):
            raise ValueError("Existing local change retained: " + relative)
    for relative, data in desired.items():
        target = destination / relative
        target.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        fd, temporary = tempfile.mkstemp(prefix=".pot-", dir=target.parent)
        with os.fdopen(fd, "wb") as stream:
            stream.write(data)
        os.chmod(temporary, 0o700 if relative.startswith("bin/") else 0o600)
        os.replace(temporary, target)
    for relative in previous.keys() - desired.keys():
        (destination / relative).unlink(missing_ok=True)
    receipt.write_text(json.dumps({k: digest(v) for k, v in desired.items()}, indent=2) + "\n")
    if activate:
        subprocess.run(
            [agy, "plugin", "validate", str(destination), "--strict", "--json"], check=True
        )
        subprocess.run([agy, "plugin", "install", str(destination)], check=True)
        subprocess.run([agy, "plugin", "enable", "pot"], check=True)
    return destination


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--home", type=Path, default=Path.home())
    parser.add_argument("--binary", type=Path)
    parser.add_argument("--agy", default=shutil.which("agy") or "agy")
    parser.add_argument("--source-only", action="store_true")
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="pot-antigravity-build-") as temporary:
        binary = args.binary or Path(temporary) / "pot-antigravity-agent"
        destination = install(
            args.home.resolve(),
            binary.resolve(),
            args.agy,
            build=args.binary is None,
            activate=not args.source_only,
        )
    print("Pot Antigravity plugin installed:", destination)
    print("Run pot-setup in Antigravity to pair with Pot.")


if __name__ == "__main__":
    main()
