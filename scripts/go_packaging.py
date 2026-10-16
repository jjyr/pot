"""Shared deterministic inputs for Go-only product packaging."""

import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tarfile
import tempfile
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
BUILD = ROOT / ".local/go"
NODE_VERSION = "24.21.0"
NODE_HASHES = {
    "darwin-arm64": "bed7eea5325e1108f32ce5228ddd6a5f0f08a499ee42aa7442aea583702f6057",
    "linux-arm64": "724282c3b43aec998aa9527380465b45d229e021b58035f5f4f63095eabfe5d5",
    "linux-amd64": "6e1db87ef58b8819e5d5402eff1536491b18edd8eb7bee5ef7897876e88dc5ff",
}
NOVNC_HASH = "b1003a11b6e6e8d8f7f5e5586daae7f8ca651d8aee0aa155ff9ac841c48f52c6"


def call(arguments, **options):
    return subprocess.run([str(value) for value in arguments], cwd=ROOT, check=True, **options)


def digest(path):
    with path.open("rb") as source:
        return hashlib.file_digest(source, "sha256").hexdigest()


def download(url, destination, expected):
    destination.parent.mkdir(parents=True, exist_ok=True)
    if not destination.exists():
        with tempfile.NamedTemporaryFile(dir=destination.parent, delete=False) as target:
            temporary = Path(target.name)
            try:
                with urllib.request.urlopen(url, timeout=120) as source:
                    shutil.copyfileobj(source, target)
                target.flush()
                os.fsync(target.fileno())
                if digest(temporary) != expected:
                    raise ValueError(f"Checksum mismatch: {destination.name}")
                temporary.replace(destination)
            finally:
                temporary.unlink(missing_ok=True)
    if digest(destination) != expected:
        raise ValueError(f"Checksum mismatch: {destination.name}")
    return destination


def extract(archive, destination):
    destination.mkdir(parents=True, exist_ok=True)
    with tarfile.open(archive) as source:
        source.extractall(destination, filter="data")


def copy_tree(source, destination):
    if destination.exists():
        shutil.rmtree(destination)
    shutil.copytree(source, destination, ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))


def node(platform):
    archive_platform = platform.replace("amd64", "x64")
    name = f"node-v{NODE_VERSION}-{archive_platform}"
    archive = download(
        f"https://nodejs.org/dist/v{NODE_VERSION}/{name}.tar.gz",
        BUILD / "downloads" / f"{name}.tar.gz",
        NODE_HASHES[platform],
    )
    target = BUILD / "node" / platform
    stamp = target / ".pot-archive-sha256"
    if not stamp.exists() or stamp.read_text() != NODE_HASHES[platform]:
        with tempfile.TemporaryDirectory(dir=BUILD) as temporary:
            extract(archive, Path(temporary))
            copy_tree(Path(temporary) / name, target)
        stamp.write_text(NODE_HASHES[platform])
    return target


def novnc():
    archive = download(
        "https://github.com/novnc/noVNC/archive/refs/tags/v1.7.0.tar.gz",
        BUILD / "resources/novnc.tar.gz",
        NOVNC_HASH,
    )
    target = BUILD / "novnc"
    if not (target / "core/rfb.js").exists():
        with tempfile.TemporaryDirectory(dir=BUILD) as temporary:
            extract(archive, Path(temporary))
            copy_tree(Path(temporary) / "noVNC-1.7.0", target)
    return target


def go_build(package, destination, platform, stripped=True):
    goos, goarch = platform.split("-")
    destination.parent.mkdir(parents=True, exist_ok=True)
    arguments = ["go", "build", "-mod=readonly", "-trimpath"]
    if stripped:
        arguments += ["-ldflags=-s -w"]
    arguments += ["-o", destination, package]
    call(arguments, env={**os.environ, "GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"})


def swift_helpers():
    scratch = BUILD / "swift-build"
    environment = {
        key: value
        for key, value in os.environ.items()
        if key not in {"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR"}
    }
    environment.update(
        SWIFTPM_MODULECACHE_OVERRIDE=str(BUILD / "swift-modules"),
        CLANG_MODULE_CACHE_PATH=str(BUILD / "clang-modules"),
    )
    call(
        [
            "swift",
            "build",
            "-c",
            "release",
            "--package-path",
            ROOT / "helper",
            "--disable-sandbox",
            "--scratch-path",
            scratch,
            "--force-resolved-versions",
            "-j",
            "4",
        ],
        env=environment,
    )
    destination = BUILD / "helper-build/release"
    destination.mkdir(parents=True, exist_ok=True)
    for name in ("PotHelper", "PotMacOSHelper", "PotImageBuilder"):
        shutil.copy2(scratch / "release" / name, destination / name)
        call(
            [
                "codesign",
                "--force",
                "--sign",
                "-",
                "--entitlements",
                ROOT / "helper/virtualization.entitlements",
                destination / name,
            ]
        )
    notices = [
        "Unmodified Swift Helper dependencies; pinned sources in helper/Package.resolved.\n",
        (ROOT / "helper/Package.resolved").read_text(),
    ]
    for checkout in sorted((scratch / "checkouts").iterdir()):
        for license_file in sorted(checkout.iterdir()):
            if license_file.is_file() and license_file.name.upper().startswith(
                ("LICENSE", "NOTICE", "COPYING")
            ):
                notices.extend(
                    [
                        f"\n{checkout.name}/{license_file.name}\n",
                        license_file.read_text(errors="replace"),
                    ]
                )
    (destination / "SwiftDependencyLicenses.txt").write_text("\n".join(notices))
    return destination


def prepare_resources(destination, platform):
    destination.mkdir(parents=True, exist_ok=True)
    call(["python3", ROOT / "scripts/localize-web"])
    for source, name in (
        (ROOT / "skills", "skills"),
        (ROOT / "plugins", "plugins"),
        (ROOT / "web/src", "web"),
        (ROOT / ".local/web/desktop", "desktop"),
        (novnc(), "novnc"),
        (node(platform), "node"),
    ):
        copy_tree(source, destination / name)
    copy_tree(destination / "novnc", destination / "desktop-assets")
    shutil.copy2(ROOT / "LICENSE", destination / "LICENSE.txt")
    shutil.copy2(
        ROOT / "web/src/assets/IBMPlexSans-OFL.txt", destination / "FrontendFontLicense.txt"
    )
    if platform == "darwin-arm64":
        copy_tree(ROOT / "resources/sandbox/macos/xcode", destination / "macos-bootstrap")
        go_build(
            "./cmd/pot-tool-executor", destination / "macos-bootstrap/pot-tool-executor", platform
        )
    call(
        [
            "python3",
            ROOT / "scripts/go-notices",
            "--output",
            destination / "GoDependencyLicenses.txt",
            "./cmd/pot",
            "./cmd/pot-tool-executor",
        ]
    )


def prepare_image_inputs():
    resources = BUILD / "resources"
    resources.mkdir(parents=True, exist_ok=True)
    kernel = resources / "vmlinux"
    kernel_hash = "fb2cfb79eb1ae19447a85d75682d7fa5cfec97e24beb2609a492b806e8072c8d"
    if not kernel.exists():
        archive = download(
            "https://github.com/kata-containers/kata-containers/releases/download/3.32.0/kata-static-3.32.0-arm64.tar.zst",
            BUILD / "downloads/kata-static-3.32.0-arm64.tar.zst",
            "8736c054d9223974735394f822000823baef509e1c33405ec798240fa9b6e4b5",
        )
        with kernel.open("wb") as target:
            call(
                ["tar", "xOf", archive, "opt/kata/share/kata-containers/vmlinux-6.18.35-197-debug"],
                stdout=target,
            )
    if digest(kernel) != kernel_hash:
        raise ValueError("Pinned guest kernel checksum mismatch")
    novnc()
    go_build("./cmd/pot-tool-executor", resources / "pot-tool-executor", "linux-arm64")
    if not (BUILD / "helper-build/release/PotImageBuilder").exists():
        swift_helpers()
