"""Integrity checks shared by macOS archive sealing and extracted-package smoke."""

import json
import subprocess

from release_contract import macos_mode


def macho_files(root):
    magics = {bytes.fromhex(value) for value in ["cffaedfe", "feedfacf", "cafebabe", "bebafeca"]}
    for path in sorted(root.rglob("*"), key=lambda p: len(p.parts), reverse=True):
        if path.is_file() and not path.is_symlink():
            with path.open("rb") as source:
                if source.read(4) in magics:
                    yield path


def verify_resources(resources, version, kind):
    metadata = json.loads((resources / "update.json").read_text())
    if (metadata["version"], metadata["kind"], metadata["platform"]) != (
        version,
        kind,
        "darwin-arm64",
    ):
        raise ValueError("macOS installation metadata does not match release")
    for name in [
        "node/bin/node",
        "node/bin/npm",
        "node/bin/npx",
        "web/index.html",
        "sandbox/launcher.mjs",
        "sandbox/node_modules/@anthropic-ai/sandbox-runtime/package.json",
        "sandbox/THIRD-PARTY-NOTICES.txt",
        "LICENSE.txt",
        "GoDependencyLicenses.txt",
        "ModelDirectoryLicense.txt",
        "FrontendFontLicense.txt",
        "FrontendDiagramLicense.txt",
        "FrontendVirtualizerLicense.txt",
        "FrontendIconLicense.txt",
    ]:
        if not (resources / name).is_file():
            raise ValueError(f"missing bundled macOS resource: {name}")
    for name in ["plugins", "skills"]:
        if not (resources / name).is_dir() or not any((resources / name).iterdir()):
            raise ValueError(f"missing bundled macOS resources: {name}")
    for name in ["sandbox-image", "sandbox-image.json"]:
        if (resources / name).exists():
            raise ValueError(f"unexpected legacy sandbox resource: {name}")


def verify_signatures(root, mode):
    macos_mode(mode)
    files = list(macho_files(root))
    if not files:
        raise ValueError("macOS archive contains no Mach-O executables")
    targets = files + ([root] if root.suffix == ".app" else [])
    for path in targets:
        subprocess.run(["codesign", "--verify", "--strict", str(path)], check=True)
        details = subprocess.run(
            ["codesign", "--display", "--verbose=4", str(path)],
            capture_output=True,
            text=True,
            check=True,
        ).stderr
        if mode == "unsigned" and "Signature=adhoc" not in details.splitlines():
            raise ValueError(f"expected ad-hoc signature: {path}")
        if mode == "signed" and not any(
            line.startswith("Authority=Developer ID Application:") for line in details.splitlines()
        ):
            raise ValueError(f"expected Developer ID signature: {path}")
    if root.suffix == ".app":
        subprocess.run(["codesign", "--verify", "--deep", "--strict", str(root)], check=True)
        if mode == "signed":
            subprocess.run(["xcrun", "stapler", "validate", str(root)], check=True)
            subprocess.run(["spctl", "--assess", "--type", "execute", str(root)], check=True)
