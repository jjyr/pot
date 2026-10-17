"""Podman plugin image inputs, build and immutable OCI export support."""

import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import tarfile
import tempfile
import urllib.request

from go_packaging import (
    BUILD,
    ROOT,
    NODE_HASHES,
    NOVNC_HASH,
    call,
    digest,
    download,
    go_build,
    node_archive,
)

GUEST_RECIPE_ASSETS = (
    "sandbox.sh",
    "install.sh",
    "executor.py",
    "skill_mounts.py",
    "operation_prepare.py",
    "desktop.sh",
    "desktop-config.sh",
    "desktop-session.sh",
    "pot-wallpaper.svg",
    "packages.lock",
    "test.html",
    "driver.py",
    "broker.py",
    "close-browser.py",
    "open-browser.sh",
    "base-image.json",
    "desktop-assets.json",
)
GUEST_WEB_ASSETS = ("index.html", "i18n.js", "locales.json")


def guest_image_config():
    path = ROOT / "providers/podman/image/base-image.json"
    config = json.loads(path.read_text())
    expected = {"schema_version", "distribution", "suite", "base_image", "snapshot"}
    if (
        set(config) != expected
        or config["schema_version"] != 1
        or config["distribution"] != "debian"
        or config["suite"] != "trixie"
        or not re.fullmatch(r"docker\.io/library/debian@sha256:[a-f0-9]{64}", config["base_image"])
        or not re.fullmatch(r"\d{8}T\d{6}Z", config["snapshot"])
    ):
        raise ValueError("A pinned Debian trixie base and snapshot are required")
    return config


def guest_desktop_assets():
    manifest = json.loads((ROOT / "providers/podman/image/desktop-assets.json").read_text())
    if set(manifest) != {"schema_version", "assets"} or manifest["schema_version"] != 1:
        raise ValueError("Pinned desktop asset manifest required")
    names = set()
    for asset in manifest["assets"]:
        if (
            set(asset) - {"filename", "checksum_file", "url", "sha256"}
            or not {"filename", "url", "sha256"} <= set(asset)
            or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", asset["filename"])
            or not asset["url"].startswith("https://")
            or not re.fullmatch(r"[a-f0-9]{64}", asset["sha256"])
        ):
            raise ValueError("Invalid pinned desktop asset")
        for name in [
            asset["filename"],
            *([asset["checksum_file"]] if "checksum_file" in asset else []),
        ]:
            if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]*", name) or name in names:
                raise ValueError("Invalid or duplicate desktop asset filename")
            names.add(name)
    return manifest["assets"]


def prepare_guest_desktop_assets(assets):
    for asset in guest_desktop_assets():
        archive = download(asset["url"], BUILD / "downloads" / asset["filename"], asset["sha256"])
        shutil.copy2(archive, assets / asset["filename"])
        if digest(assets / asset["filename"]) != asset["sha256"]:
            raise ValueError("Guest desktop asset checksum mismatch")
        if "checksum_file" in asset:
            (assets / asset["checksum_file"]).write_text(
                asset["sha256"] + "  " + asset["filename"] + "\n"
            )


def prepare_guest_assets(assets, arch):
    """Prepare one complete, verified guest build input in a fresh directory."""
    if arch not in {"amd64", "arm64"}:
        raise ValueError("Guest architecture must be amd64 or arm64")
    assets = Path(assets)
    if assets.exists() and (not assets.is_dir() or any(assets.iterdir())):
        raise ValueError("Guest assets directory must be fresh or empty")
    config = guest_image_config()
    recipe = ROOT / "providers/podman/image"
    for name in GUEST_RECIPE_ASSETS:
        if not (recipe / name).is_file():
            raise ValueError(f"Missing guest asset: {name}")
    call(["python3", ROOT / "scripts/localize-web"])
    for name in GUEST_WEB_ASSETS:
        if not (ROOT / ".local/web/sandbox" / name).is_file():
            raise ValueError(f"Missing localized guest asset: {name}")
    assets.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=assets.parent) as temporary:
        staged = Path(temporary) / "assets"
        staged.mkdir()
        for name in GUEST_RECIPE_ASSETS:
            shutil.copy2(recipe / name, staged / name)
        for name in GUEST_WEB_ASSETS:
            shutil.copy2(ROOT / ".local/web/sandbox" / name, staged / name)
        go_build("./cmd/pot-tool-executor", staged / "pot-tool-executor", "linux-" + arch)
        archive = node_archive("linux-" + arch)
        shutil.copy2(archive, staged / "node.tar.gz")
        expected = NODE_HASHES["linux-" + arch]
        if digest(staged / "node.tar.gz") != expected:
            raise ValueError("Guest Node archive checksum mismatch")
        (staged / "node.sha256").write_text(expected + "  node.tar.gz\n")
        archive = download(
            "https://github.com/novnc/noVNC/archive/refs/tags/v1.7.0.tar.gz",
            BUILD / "resources/novnc.tar.gz",
            NOVNC_HASH,
        )
        shutil.copy2(archive, staged / "novnc.tar.gz")
        prepare_guest_desktop_assets(staged)
        manifest = {
            "architecture": arch,
            "base": config,
            "assets": sorted(p.name for p in staged.iterdir()),
        }
        if assets.exists():
            assets.rmdir()
        staged.rename(assets)
    return manifest


def verify_oci_layout(layout):
    layout = Path(layout)
    if layout.is_symlink() or not layout.is_dir():
        raise ValueError("Unsafe OCI image layout")
    for item in layout.rglob("*"):
        if item.is_symlink() or not (item.is_file() or item.is_dir()):
            raise ValueError("Unsafe OCI image layout member")
    if json.loads((layout / "oci-layout").read_text()) != {"imageLayoutVersion": "1.0.0"}:
        raise ValueError("OCI image layout version is invalid")
    seen = set()

    def descriptor(value):
        digest_value = value.get("digest", "")
        if not re.fullmatch(r"sha256:[a-f0-9]{64}", digest_value):
            raise ValueError("OCI descriptor requires SHA-256")
        if digest_value in seen:
            return
        seen.add(digest_value)
        blob = layout / "blobs/sha256" / digest_value.split(":", 1)[1]
        if (
            not blob.is_file()
            or blob.stat().st_size != value["size"]
            or digest(blob) != digest_value.split(":", 1)[1]
        ):
            raise ValueError("OCI image blob is missing or corrupt")
        if value.get("mediaType") in {
            "application/vnd.oci.image.index.v1+json",
            "application/vnd.oci.image.manifest.v1+json",
            "application/vnd.docker.distribution.manifest.list.v2+json",
            "application/vnd.docker.distribution.manifest.v2+json",
        }:
            index(json.loads(blob.read_bytes()))

    def index(value):
        for entry in value.get("manifests", []):
            descriptor(entry)
        if "config" in value:
            descriptor(value["config"])
        for entry in value.get("layers", []):
            descriptor(entry)

    value = json.loads((layout / "index.json").read_text())
    if value.get("schemaVersion") != 2 or not value.get("manifests"):
        raise ValueError("OCI image index is empty or invalid")
    index(value)
    return value


def oci_image_descriptor(layout, architecture):
    """Return the one Linux image manifest and validate its actual config CPU."""
    layout = Path(layout)
    root = verify_oci_layout(layout)
    images = []

    def visit(descriptor):
        value = json.loads(
            (layout / "blobs/sha256" / descriptor["digest"].split(":", 1)[1]).read_bytes()
        )
        if "manifests" in value:
            for child in value["manifests"]:
                visit(child)
            return
        config = json.loads(
            (layout / "blobs/sha256" / value["config"]["digest"].split(":", 1)[1]).read_bytes()
        )
        if config.get("os") != "linux" or config.get("architecture") != architecture:
            raise ValueError("OCI image architecture does not match the selected Linux target")
        images.append((descriptor, config))

    for descriptor in root["manifests"]:
        visit(descriptor)
    if len(images) != 1:
        raise ValueError("One architecture-specific OCI image is required")
    return images[0]


def publish_guest_image(layout, output, metadata):
    """Publish one verified image/metadata directory, retaining old output on failure."""
    layout, output = Path(layout), Path(output)
    descriptor, _ = oci_image_descriptor(layout, metadata["architecture"])
    if descriptor["digest"] != metadata["manifest_digest"]:
        raise ValueError("Guest metadata does not match the built image")
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".image-publish-", dir=output.parent) as temporary:
        staged, previous = Path(temporary) / "new", Path(temporary) / "old"
        staged.mkdir()
        shutil.copytree(layout, staged / "sandbox-image")
        (staged / "sandbox-image.json").write_text(json.dumps(metadata, indent=2) + "\n")
        if output.exists():
            output.rename(previous)
        try:
            staged.rename(output)
        except BaseException:
            if previous.exists():
                previous.rename(output)
            raise


def context_fingerprint(context):
    fingerprint = hashlib.sha256()
    for path in sorted(Path(context).rglob("*")):
        if path.is_symlink() or not (path.is_dir() or path.is_file()):
            raise ValueError("Guest build context must contain only regular files/directories")
        if path.is_file() and path.relative_to(context).as_posix() != "build-inputs.json":
            fingerprint.update(path.relative_to(context).as_posix().encode() + b"\0")
            with path.open("rb") as source:
                while chunk := source.read(1024 * 1024):
                    fingerprint.update(chunk)
            fingerprint.update(b"\0")
    return fingerprint.hexdigest()


def prepare_guest_context(context, architecture):
    context = Path(context)
    if context.exists() and (not context.is_dir() or any(context.iterdir())):
        raise ValueError("Guest build context must be fresh or empty")
    context.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(dir=context.parent) as temporary:
        staged = Path(temporary) / "context"
        staged.mkdir()
        manifest = prepare_guest_assets(staged / "assets", architecture)
        shutil.copy2(ROOT / "providers/podman/image/Dockerfile", staged / "Dockerfile")
        manifest["source_hash"] = context_fingerprint(staged)
        (staged / "build-inputs.json").write_text(json.dumps(manifest, indent=2) + "\n")
        if context.exists():
            context.rmdir()
        staged.rename(context)
    return manifest


def validate_guest_context(context, architecture):
    context = Path(context)
    manifest = json.loads((context / "build-inputs.json").read_text())
    if manifest.get("architecture") != architecture or manifest.get("base") != guest_image_config():
        raise ValueError("Prepared context does not match the selected architecture/base")
    if manifest.get("source_hash") != context_fingerprint(context):
        raise ValueError("Prepared context checksum changed")
    return manifest


def unpack_oci_archive(archive, destination):
    destination = Path(destination)
    if destination.exists():
        raise ValueError("OCI unpack destination must not exist")
    with tarfile.open(archive) as source:
        seen = set()
        for entry in source:
            name = entry.name.removeprefix("./")
            if name in {"", "."} and entry.isdir():
                continue
            if (
                Path(name).is_absolute()
                or ".." in Path(name).parts
                or entry.issym()
                or entry.islnk()
                or not (entry.isfile() or entry.isdir())
                or name in seen
            ):
                raise ValueError("Unsafe or duplicate OCI archive member")
            seen.add(name)
            tarfile.data_filter(entry, destination)
        destination.mkdir(parents=True)
        try:
            source.extractall(destination, filter="data")
            verify_oci_layout(destination)
        except BaseException:
            shutil.rmtree(destination)
            raise


def podman_command(connection=None, system=None):
    import platform

    if system is None:
        system = platform.system()
    if connection:
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,127}", connection):
            raise ValueError("A named dedicated Podman connection is required")
        return ["podman", "--connection", connection]
    if system == "Darwin":
        raise ValueError(
            "macOS image builds require --connection for an explicitly provisioned dedicated Podman builder"
        )
    if system != "Linux":
        raise ValueError("Podman image builds require Linux or macOS")
    return ["podman", "--remote=false"]


def build_guest_image(context, output, architecture, connection=None, reference=None):
    context, output = Path(context), Path(output)
    manifest = validate_guest_context(context, architecture)
    command = podman_command(connection)
    reference = reference or f"localhost/pot-sandbox:{manifest['source_hash'][:20]}-{architecture}"
    if not re.fullmatch(r"[a-z0-9][a-z0-9._:/@-]{0,250}", reference):
        raise ValueError("Invalid image reference")
    call(
        [
            *command,
            "build",
            "--format=oci",
            "--network=private",
            "--platform=linux/" + architecture,
            "--file",
            context / "Dockerfile",
            "--build-arg",
            "BASE_IMAGE=" + manifest["base"]["base_image"],
            "--label",
            "dev.pot.input.sha256=" + manifest["source_hash"],
            "--tag",
            reference,
            context,
        ]
    )
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".image-export-", dir=output.parent) as temporary:
        archive, layout = Path(temporary) / "image.oci.tar", Path(temporary) / "image"
        call([*command, "save", "--format=oci-archive", "--output", archive, reference])
        unpack_oci_archive(archive, layout)
        descriptor, config = oci_image_descriptor(layout, architecture)
        if (
            config.get("config", {}).get("Labels", {}).get("dev.pot.input.sha256")
            != manifest["source_hash"]
        ):
            raise ValueError("Exported image does not match the reviewed build context")
        metadata = {
            "schema_version": 1,
            "os": "linux",
            "architecture": architecture,
            "reference": reference,
            "manifest_digest": descriptor["digest"],
            "source_hash": manifest["source_hash"],
            "base": manifest["base"],
        }
        publish_guest_image(layout, output, metadata)
    return metadata


def guest_image_main():
    import argparse
    import platform

    parser = argparse.ArgumentParser(
        description="Build/export one architecture-specific Debian OCI image using Podman"
    )
    parser.add_argument(
        "--arch",
        choices=["amd64", "arm64"],
        default={"x86_64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine()),
    )
    parser.add_argument(
        "--connection",
        help="Dedicated Podman builder connection (required on macOS; never starts a machine)",
    )
    parser.add_argument("--tag", help="Local image reference; nothing is pushed")
    parser.add_argument("--context", type=Path, help="Fresh/empty prepared-context destination")
    parser.add_argument(
        "--prepared-context",
        type=Path,
        help="Use an existing checksum-verified context without rebuilding inputs",
    )
    parser.add_argument(
        "--output",
        type=Path,
        help="Image output directory; defaults to .local/go/images/linux-ARCH",
    )
    parser.add_argument("--prepare-only", action="store_true")
    args = parser.parse_args()
    if not args.arch:
        parser.error("Specify --arch for this host")
    if args.context and args.prepared_context:
        parser.error("Choose --context or --prepared-context")
    if not args.prepare_only:
        podman_command(args.connection)
    context = (
        args.prepared_context or args.context or BUILD / "container-image" / args.arch
    ).resolve()
    if args.prepared_context:
        validate_guest_context(context, args.arch)
    elif args.context:
        prepare_guest_context(context, args.arch)
    else:
        context.parent.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(dir=context.parent) as temporary:
            staged = Path(temporary) / "context"
            prepare_guest_context(staged, args.arch)
            previous = Path(temporary) / "previous"
            if context.exists():
                context.rename(previous)
            try:
                staged.rename(context)
            except BaseException:
                if previous.exists():
                    previous.rename(context)
                raise
    if args.prepare_only:
        print(context)
        return
    output = (args.output or BUILD / "images" / ("linux-" + args.arch)).resolve()
    print(
        json.dumps(
            build_guest_image(context, output, args.arch, args.connection, args.tag), indent=2
        )
    )
