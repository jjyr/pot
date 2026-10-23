"""Release naming and metadata shared by packaging and publishing tools."""

import hashlib
import json
import plistlib
import re
from pathlib import Path
import zipfile
import tarfile

VERSION = r"(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)"
ROOT = Path(__file__).resolve().parents[1]


def version_number(value):
    version = value.removeprefix("v")
    if not re.fullmatch(VERSION, version):
        raise ValueError("version must be major.minor.patch without leading zeroes")
    return version


def macos_mode(value):
    if value not in {"unsigned", "signed"}:
        raise ValueError("POT_MACOS_RELEASE_MODE must explicitly select unsigned or signed")
    return value


def macos_release_notes(version, revision, mode):
    macos_mode(mode)
    if not re.fullmatch(r"[a-f0-9]{40}", revision or ""):
        raise ValueError("macOS release requires the validated 40-character commit SHA")
    status = (
        "Ad-hoc signed for integrity only; no Developer ID signature or Apple notarization."
        if mode == "unsigned"
        else "Developer ID signed and Apple notarized."
    )
    return (
        f"Pot {version_number(version)}\nCommit: {revision}\nmacOS mode: {mode}\n{status}\n"
        "The Ed25519-signed update feed authenticates Pot artifacts independently of Apple.\n"
        "Installation and Gatekeeper instructions:\n"
        f"https://github.com/jjyr/pot/blob/v{version}/docs/desktop-installation.md#macos\n"
    )


def checksum(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def write_checksum(path):
    path.with_name(path.name + ".sha256").write_text(f"{checksum(path)}  {path.name}\n")


def update_artifacts(version):
    version_number(version)
    return [("app", "darwin-arm64", f"Pot-{version}-arm64.zip")] + [
        ("cli", target, f"pot-go-{version}-{target}.tar.gz")
        for target in ("darwin-arm64", "linux-amd64", "linux-arm64")
    ]


def distribution_names(version, channel="validation"):
    version_number(version)
    channels = {
        "server": [f"pot-go-{version}-linux-{arch}.tar.gz" for arch in ("amd64", "arm64")],
        "desktop": [f"pot-desktop_{version}_linux_amd64.deb"],
        "macos": [name for _, _, name in update_artifacts(version)],
        "container": [f"pot-container-{version}.oci.tar"],
    }
    if channel == "validation":
        return channels["macos"] + channels["desktop"] + channels["container"]
    return channels[channel]


def checksum_name(channel):
    return "SHA256SUMS" if channel == "validation" else "SHA256SUMS-" + channel


def release_assets(version, channel):
    names = distribution_names(version, channel)
    assets = names + [name + ".sha256" for name in names] + [checksum_name(channel)]
    if channel == "macos":
        assets += ["pot.rb", "update.json", "MACOS-RELEASE.txt"]
    return assets


def verify_oci(archive, version, revision=None):
    """Read manifest/config blobs and require both actual Linux architectures."""
    platforms = set()
    with tarfile.open(archive) as packed:

        def read(name):
            with packed.extractfile(name) as source:
                data = source.read(16 * 1024 * 1024 + 1)
            if len(data) > 16 * 1024 * 1024:
                raise ValueError("OCI metadata exceeds size limit")
            return data

        def blob(descriptor):
            algorithm, digest = descriptor["digest"].split(":", 1)
            if algorithm != "sha256" or not re.fullmatch(r"[a-f0-9]{64}", digest):
                raise ValueError("invalid OCI metadata digest")
            data = read("blobs/sha256/" + digest)
            if hashlib.sha256(data).hexdigest() != digest:
                raise ValueError("OCI metadata checksum mismatch")
            return json.loads(data)

        def visit(manifest):
            if "manifests" in manifest:
                for child in manifest["manifests"]:
                    # BuildKit may include separate provenance attestations.
                    if child.get("platform", {}).get("os") != "unknown":
                        visit(blob(child))
            elif "config" in manifest:
                config = blob(manifest["config"])
                labels = config.get("config", {}).get("Labels", {})
                if labels.get("org.opencontainers.image.version") != version:
                    raise ValueError("OCI image version does not match release version")
                if revision and labels.get("org.opencontainers.image.revision") != revision:
                    raise ValueError("OCI image revision does not match validated commit")
                platforms.add((config.get("os"), config.get("architecture")))
            else:
                raise ValueError("invalid OCI image manifest")

        visit(json.loads(read("index.json")))
    if platforms != {("linux", "amd64"), ("linux", "arm64")}:
        raise ValueError(f"OCI distribution must contain Linux amd64 and arm64: {platforms}")


def generate_cask(version, output):
    version_number(version)
    archive = output / f"Pot-{version}-arm64.zip"
    expected = f"{checksum(archive)}  {archive.name}\n"
    if archive.with_name(archive.name + ".sha256").read_text() != expected:
        raise ValueError("macOS archive checksum does not match its sidecar")
    with zipfile.ZipFile(archive) as packed:
        info = plistlib.loads(packed.read("Pot.app/Contents/Info.plist"))
    if info.get("CFBundleShortVersionString") != version:
        raise ValueError("macOS bundle version does not match cask version")
    template = (ROOT / "packaging/homebrew/pot.rb.in").read_text()
    cask = template.replace("@VERSION@", version).replace("@SHA256@", checksum(archive))
    if re.search(r"@[A-Z_]+@", cask):
        raise ValueError("unexpanded cask template variable")
    (output / "pot.rb").write_text(cask)
