"""Shared deterministic inputs for Go-only product packaging."""

import hashlib
import json
import os
import re
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
WAILS_VERSION = "v3.0.0-beta.30"
WAILS_TRAY_SOURCE_HASH = "eadc44b2c628ef6ec9c3c568792c945f14be2a412ab14174b642386e9d15809e"
WAILS_TRAY_PATCH_HASH = "b658e13aea5f08e3a4b303c8a5b760928edaefa8f4ca6a9323766e8e8160a19c"


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


def apply_pinned_patch(source, patch, source_hash, patch_hash):
    if hashlib.sha256(source).hexdigest() != source_hash:
        raise ValueError("Wails tray source changed; review the pinned patch")
    if hashlib.sha256(patch).hexdigest() != patch_hash:
        raise ValueError("Wails tray patch changed; review and update its pinned hash")
    lines = patch.decode().splitlines(keepends=True)
    starts = [i for i, line in enumerate(lines) if line.startswith("@@ ")]
    if not starts:
        raise ValueError("Wails tray patch hunk required")
    for position, start in enumerate(starts):
        end = starts[position + 1] if position + 1 < len(starts) else len(lines)
        hunk = lines[start + 1 : end]
        old = "".join(line[1:] for line in hunk if line.startswith((" ", "-"))).encode()
        new = "".join(line[1:] for line in hunk if line.startswith((" ", "+"))).encode()
        if not old or source.count(old) != 1:
            raise ValueError("Wails tray patch context changed")
        source = source.replace(old, new, 1)
    return source


def apply_tray_patch(source, patch):
    return apply_pinned_patch(source, patch, WAILS_TRAY_SOURCE_HASH, WAILS_TRAY_PATCH_HASH)


def prepare_wails_module(work):
    """Patch only a disposable module copy; leave the source cache untouched."""
    module = json.loads(
        subprocess.check_output(
            ["go", "list", "-mod=readonly", "-m", "-json", "github.com/wailsapp/wails/v3"],
            cwd=ROOT,
        )
    )
    if module.get("Version") != WAILS_VERSION or module.get("Replace"):
        raise ValueError("Pinned Wails beta.30 without an existing replacement required")
    original = Path(module["Dir"])
    modifications = [
        (
            "pkg/application/systemtray_darwin.m",
            "wails-v3.0.0-beta.30-tray-aspect.patch",
            WAILS_TRAY_SOURCE_HASH,
            WAILS_TRAY_PATCH_HASH,
        ),
        (
            "pkg/application/systemtray_darwin.go",
            "wails-v3.0.0-beta.30-tray-accessibility.patch",
            "415547a6508f920432becbe69400fcc9f99e050dd81c6f86d5f4c611bd8f6c18",
            "3e8b13f6e7c6a0fcf460b67c8ae4ca9fdb29aceeb58512f82e254934b369dc13",
        ),
    ]
    sources = []
    for relative, patch_name, source_hash, patch_hash in modifications:
        source = (original / relative).read_bytes()
        patched = apply_pinned_patch(
            source, (ROOT / "patches" / patch_name).read_bytes(), source_hash, patch_hash
        )
        sources.append((relative, source, patched))
    local = work / "wails"
    shutil.copytree(original, local)
    for relative, source, patched in sources:
        destination = local / relative
        destination.chmod(0o600)
        destination.write_bytes(patched)
        if (original / relative).read_bytes() != source:
            raise ValueError("Upstream module cache changed during preparation")
    modfile = work / "app.mod"
    shutil.copy2(ROOT / "go.mod", modfile)
    shutil.copy2(ROOT / "go.sum", work / "app.sum")
    call(
        [
            "go",
            "mod",
            "edit",
            "-modfile=" + str(modfile),
            "-replace=github.com/wailsapp/wails/v3=" + str(local),
        ]
    )
    return modfile


def copy_tree(source, destination):
    if destination.exists():
        shutil.rmtree(destination)
    shutil.copytree(source, destination, ignore=shutil.ignore_patterns("__pycache__", "*.pyc"))


def node_archive(platform):
    if platform not in NODE_HASHES:
        raise ValueError(f"Unsupported Node platform: {platform}")
    name = f"node-v{NODE_VERSION}-{platform.replace('amd64', 'x64')}"
    return download(
        f"https://nodejs.org/dist/v{NODE_VERSION}/{name}.tar.gz",
        BUILD / "downloads" / f"{name}.tar.gz",
        NODE_HASHES[platform],
    )


def node(platform):
    name = f"node-v{NODE_VERSION}-{platform.replace('amd64', 'x64')}"
    archive = node_archive(platform)
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


def linker_flags(stripped=True):
    version = os.environ.get("POT_APP_VERSION", "0.1.0")
    if not re.fullmatch(r"\d+\.\d+\.\d+", version):
        raise ValueError("POT_APP_VERSION must be major.minor.patch")
    revision = os.environ.get("POT_BUILD_REVISION")
    if revision is None:
        environment = {
            key: value for key, value in os.environ.items() if not key.startswith("GIT_")
        }
        revision = subprocess.check_output(
            ["git", "rev-parse", "--short=12", "HEAD"], cwd=ROOT, env=environment, text=True
        ).strip()
        if subprocess.run(["git", "diff", "--quiet"], cwd=ROOT, env=environment).returncode:
            revision += "-dirty"
    if not re.fullmatch(r"[A-Za-z0-9._-]{1,100}", revision):
        raise ValueError("Invalid POT_BUILD_REVISION")
    values = ["-s", "-w"] if stripped else []
    values += [
        "-X=github.com/jjyr/pot/internal/buildinfo.Version=" + version,
        "-X=github.com/jjyr/pot/internal/buildinfo.Revision=" + revision,
        "-X=github.com/jjyr/pot/pkg/eval.Revision=" + revision,
    ]
    return " ".join(values)


def go_build(package, destination, platform, stripped=True):
    goos, goarch = platform.split("-")
    destination.parent.mkdir(parents=True, exist_ok=True)
    arguments = ["go", "build", "-mod=readonly", "-trimpath"]
    arguments += ["-ldflags=" + linker_flags(stripped)]
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
            ROOT / "plugins/apple-vm/helper",
            "--disable-sandbox",
            "--scratch-path",
            scratch,
            "--product",
            "PotMacOSHelper",
            "-j",
            "4",
        ],
        env=environment,
    )
    destination = BUILD / "helper-build/release"
    if destination.exists():
        shutil.rmtree(destination)
    destination.mkdir(parents=True)
    for name in ("PotMacOSHelper",):
        shutil.copy2(scratch / "release" / name, destination / name)
        call(
            [
                "codesign",
                "--force",
                "--sign",
                "-",
                "--entitlements",
                ROOT / "plugins/apple-vm/helper/virtualization.entitlements",
                destination / name,
            ]
        )
    (destination / "SwiftDependencyLicenses.txt").write_text(
        "PotMacOSHelper uses Apple Foundation and Virtualization system frameworks.\n"
        "No third-party Swift package dependencies are bundled.\n"
    )
    return destination


def prepare_resources(destination, platform):
    goos, goarch = platform.split("-")
    destination.mkdir(parents=True, exist_ok=True)
    call(["python3", ROOT / "scripts/localize-web"])
    for source, name in (
        (ROOT / "skills", "skills"),
        (ROOT / "web/src", "web"),
        (ROOT / ".local/web/desktop", "desktop"),
        (novnc(), "novnc"),
        (node(platform), "node"),
    ):
        copy_tree(source, destination / name)
    copy_tree(destination / "novnc", destination / "desktop-assets")
    shutil.copy2(ROOT / "LICENSE", destination / "LICENSE.txt")
    shutil.copy2(ROOT / "resources/models-dev/LICENSE", destination / "ModelDirectoryLicense.txt")
    shutil.copy2(
        ROOT / "web/src/assets/IBMPlexSans-OFL.txt", destination / "FrontendFontLicense.txt"
    )
    shutil.copy2(
        ROOT / "web/src/assets/Lucide-LICENSE.txt", destination / "FrontendIconLicense.txt"
    )
    prepare_plugins(destination / "plugins", platform)
    if goos == "darwin":
        shutil.copy2(
            destination / "plugins/apple-vm/SwiftDependencyLicenses.txt",
            destination / "SwiftDependencyLicenses.txt",
        )
    call(
        [
            "python3",
            ROOT / "scripts/go-notices",
            "--output",
            destination / "GoDependencyLicenses.txt",
            "./cmd/pot",
            "./cmd/pot-tool-executor",
            "./plugins/podman/cmd",
            *(["./plugins/apple-vm/cmd"] if goos == "darwin" else []),
        ],
        env={**os.environ, "GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"},
    )


def prepare_plugins(destination, platform):
    """Package declared capabilities; never prepare an engine or guest image."""
    goos, _ = platform.split("-")
    destination.mkdir(parents=True, exist_ok=True)
    for source in sorted((ROOT / "plugins").iterdir()):
        manifest_path = source / "plugin.json"
        if not manifest_path.is_file():
            continue
        manifest = json.loads(manifest_path.read_text())
        contributions = manifest["contributes"]
        providers = contributions.get("sandbox_providers", [])
        supported = [p for p in providers if goos in p["host_os"]]
        if providers and not supported and not contributions.get("tools"):
            continue
        package = destination / source.name
        if package.exists():
            shutil.rmtree(package)
        package.mkdir(parents=True)
        shutil.copy2(manifest_path, package / "plugin.json")
        tools = contributions.get("tools")
        if tools:
            for name in tools["files"]:
                target = package / name
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(source / name, target)
        for resource in contributions.get("resources", []):
            name = resource["catalog"]
            target = package / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source / name, target)
        for provider in supported:
            target = package / provider["executable"]
            target.parent.mkdir(parents=True, exist_ok=True)
            go_build("./plugins/" + source.name + "/cmd", target, platform)
        if source.name == "apple-vm" and supported:
            copy_tree(source / "bootstrap", package / "bootstrap")
            go_build("./cmd/pot-tool-executor", package / "bootstrap/pot-tool-executor", platform)
            helpers = swift_helpers()
            shutil.copy2(helpers / "PotMacOSHelper", package / "bin/PotMacOSHelper")
            shutil.copy2(
                helpers / "SwiftDependencyLicenses.txt", package / "SwiftDependencyLicenses.txt"
            )
