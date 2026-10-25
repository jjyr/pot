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
        ("cli", target, f"pot-{version}-{target}.tar.gz")
        for target in ("darwin-arm64", "linux-amd64", "linux-arm64")
    ]


def distribution_names(version, channel="validation"):
    version_number(version)
    channels = {
        "server": [f"pot-{version}-linux-{arch}.tar.gz" for arch in ("amd64", "arm64")],
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
        assets += ["update.json", "MACOS-RELEASE.txt"]
    return assets


def cli_installation(extracted):
    """Locate the single Pot installation and validate its platform metadata."""
    entries = list(extracted.iterdir())
    if len(entries) != 1 or not entries[0].is_dir():
        raise ValueError("CLI archive must contain exactly one installation")
    installation = entries[0]
    metadata = json.loads((installation / "Resources/update.json").read_text())
    platform = metadata["platform"]
    if metadata["kind"] != "cli" or platform not in {"darwin-arm64", "linux-amd64", "linux-arm64"}:
        raise ValueError("unsupported CLI package metadata")
    if installation.name != f"pot-{platform}":
        raise ValueError("CLI directory does not match package metadata")
    if not (installation / "bin/pot").is_file():
        raise ValueError("CLI package has no bin/pot")
    return installation


def verify_oci_metadata(index, read_blob, version, revision=None):
    """Validate the same image metadata for an OCI archive or anonymous registry."""
    platforms = set()
    images = []
    layers = {}

    def blob(descriptor):
        digest = descriptor["digest"]
        if not re.fullmatch(r"sha256:[a-f0-9]{64}", digest):
            raise ValueError("invalid OCI metadata digest")
        data = read_blob(digest)
        if (
            len(data) != descriptor["size"]
            or "sha256:" + hashlib.sha256(data).hexdigest() != digest
        ):
            raise ValueError("OCI metadata checksum/size mismatch")
        return json.loads(data)

    def visit(manifest, declared=None, descriptor=None):
        if "manifests" in manifest:
            for child in manifest["manifests"]:
                platform = child.get("platform", {})
                # BuildKit's separate provenance attestations are not runnable images.
                if platform.get("os") != "unknown":
                    visit(blob(child), platform, child)
        elif "config" in manifest:
            config = blob(manifest["config"])
            labels = config.get("config", {}).get("Labels", {})
            if labels.get("org.opencontainers.image.source") != "https://github.com/jjyr/pot":
                raise ValueError("OCI image source repository does not match release")
            if labels.get("org.opencontainers.image.version") != version:
                raise ValueError("OCI image version does not match release version")
            if revision and labels.get("org.opencontainers.image.revision") != revision:
                raise ValueError("OCI image revision does not match validated commit")
            platform = (config.get("os"), config.get("architecture"))
            if declared and (declared.get("os"), declared.get("architecture")) != platform:
                raise ValueError("OCI descriptor/config architecture mismatch")
            if platform in platforms:
                raise ValueError("duplicate OCI image architecture")
            platforms.add(platform)
            images.append(
                {
                    "platform": "/".join(platform),
                    "labels": labels,
                    "manifest_digest": descriptor["digest"] if descriptor else None,
                    "config_digest": manifest["config"]["digest"],
                }
            )
            for layer in manifest.get("layers", []):
                digest, size = layer["digest"], layer["size"]
                if not re.fullmatch(r"sha256:[a-f0-9]{64}", digest) or size < 0:
                    raise ValueError("invalid OCI layer descriptor")
                if digest in layers and layers[digest] != size:
                    raise ValueError("conflicting OCI layer sizes")
                layers[digest] = size
        else:
            raise ValueError("invalid OCI image manifest")

    visit(index)
    if platforms != {("linux", "amd64"), ("linux", "arm64")}:
        raise ValueError(f"OCI distribution must contain Linux amd64 and arm64: {platforms}")
    return {"images": images, "layers": layers}


def verify_oci(archive, version, revision=None):
    """Read manifest/config blobs and require both actual Linux architectures."""
    with tarfile.open(archive) as packed:

        def read(name):
            with packed.extractfile(name) as source:
                data = source.read(16 * 1024 * 1024 + 1)
            if len(data) > 16 * 1024 * 1024:
                raise ValueError("OCI metadata exceeds size limit")
            return data

        return verify_oci_metadata(
            json.loads(read("index.json")),
            lambda digest: read("blobs/sha256/" + digest.split(":")[1]),
            version,
            revision,
        )


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


def generate_formula(version, output):
    """Reuse the final macOS CLI archive; never build a second CLI artifact."""
    version_number(version)
    archive = output / f"pot-{version}-darwin-arm64.tar.gz"
    expected = f"{checksum(archive)}  {archive.name}\n"
    if archive.with_name(archive.name + ".sha256").read_text() != expected:
        raise ValueError("CLI archive checksum does not match its sidecar")
    root = "pot-darwin-arm64/"
    with tarfile.open(archive) as packed:
        metadata = json.load(packed.extractfile(root + "Resources/update.json"))
        binary = packed.getmember(root + "bin/pot")
        if not binary.isfile() or binary.mode & 0o111 == 0:
            raise ValueError("CLI archive must contain executable bin/pot")
    if (metadata["version"], metadata["kind"], metadata["platform"]) != (
        version,
        "cli",
        "darwin-arm64",
    ):
        raise ValueError("CLI package metadata does not match formula version/platform")
    template = (ROOT / "packaging/homebrew/pot-cli.rb.in").read_text()
    formula = template.replace("@VERSION@", version).replace("@SHA256@", checksum(archive))
    if re.search(r"@[A-Z_]+@", formula):
        raise ValueError("unexpanded formula template variable")
    (output / "pot-cli.rb").write_text(formula)
