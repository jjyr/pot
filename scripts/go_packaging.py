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


def copy_tree(source, destination, *, symlinks=False):
    if destination.exists():
        shutil.rmtree(destination)
    shutil.copytree(
        source,
        destination,
        symlinks=symlinks,
        ignore=shutil.ignore_patterns("__pycache__", "*.pyc"),
    )


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
    if (
        not stamp.exists()
        or stamp.read_text() != NODE_HASHES[platform]
        or not (target / "bin/npm").is_symlink()
    ):
        with tempfile.TemporaryDirectory(dir=BUILD) as temporary:
            extract(archive, Path(temporary))
            copy_tree(Path(temporary) / name, target, symlinks=True)
        stamp.write_text(NODE_HASHES[platform])
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
    public_key = os.environ.get("POT_UPDATE_PUBLIC_KEY", "")
    if public_key:
        import base64

        if len(base64.b64decode(public_key, validate=True)) != 32:
            raise ValueError("POT_UPDATE_PUBLIC_KEY must be a base64 Ed25519 public key")
    values = ["-s", "-w"] if stripped else []
    values += [
        "-X=github.com/jjyr/pot/internal/buildinfo.Version=" + version,
        "-X=github.com/jjyr/pot/internal/buildinfo.UpdatePublicKey=" + public_key,
        "-X=github.com/jjyr/pot/internal/buildinfo.UpdateSchemaDigest=" + schema_digest(),
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
    call(
        arguments,
        env={
            **os.environ,
            "GOOS": goos,
            "GOARCH": goarch,
            "CGO_ENABLED": "1" if goos == "darwin" else "0",
        },
    )


def prepare_resources(destination, platform):
    goos, goarch = platform.split("-")
    destination.mkdir(parents=True, exist_ok=True)
    for source, name in (
        (ROOT / "skills", "skills"),
        (ROOT / "web/src", "web"),
        (node(platform), "node"),
    ):
        copy_tree(source, destination / name, symlinks=name == "node")
    shutil.copy2(ROOT / "LICENSE", destination / "LICENSE.txt")
    (destination / "ModelDirectoryLicense.txt").write_text(
        "\n\n".join(
            (ROOT / "resources" / name / "LICENSE").read_text().rstrip()
            for name in ("models-dev", "provider-logos")
        )
        + "\n"
    )
    shutil.copy2(
        ROOT / "web/src/assets/IBMPlexSans-OFL.txt", destination / "FrontendFontLicense.txt"
    )
    shutil.copy2(
        ROOT / "web/src/vendor/pele-LICENSE.txt", destination / "FrontendDiagramLicense.txt"
    )
    (destination / "FrontendIconLicense.txt").write_text(
        "\n\n".join(
            (ROOT / "web/src/assets" / name).read_text().rstrip()
            for name in ("Lucide-LICENSE.txt", "Octicons-LICENSE.txt")
        )
        + "\n"
    )
    prepare_sandbox(destination / "sandbox")
    prepare_plugins(destination / "plugins", platform)
    call(
        [
            "python3",
            ROOT / "scripts/go-notices",
            "--output",
            destination / "GoDependencyLicenses.txt",
            "./cmd/pot",
        ],
        env={**os.environ, "GOOS": goos, "GOARCH": goarch, "CGO_ENABLED": "0"},
    )


def prepare_plugins(destination, platform):
    """Synchronize a dedicated generated directory with current plugin packages."""
    destination = destination.absolute()
    if destination.is_symlink():
        raise ValueError("plugin output must not be a symlink")
    destination = destination.resolve()
    source_root = (ROOT / "plugins").resolve()
    if destination.is_relative_to(source_root) or source_root.is_relative_to(destination):
        raise ValueError("plugin output must not overlap plugin sources")
    # Build and validate first; a bad declaration must not erase previous output.
    destination.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix=".plugins-", dir=destination.parent) as temporary:
        staged = Path(temporary)
        package_plugins(staged)
        destination.mkdir(exist_ok=True)
        # Only generated output children are removed. Never follow their symlinks.
        for child in destination.iterdir():
            if child.is_symlink() or not child.is_dir():
                child.unlink()
            else:
                shutil.rmtree(child)
        for package in staged.iterdir():
            shutil.move(package, destination / package.name)


def package_plugins(destination):
    """Package declared capability files and brand assets into a fresh directory."""
    destination.mkdir(parents=True, exist_ok=True)
    for source in sorted((ROOT / "plugins").iterdir()):
        if source.is_symlink():
            raise ValueError("unsafe plugin source")
        manifest_path = source / "plugin.json"
        if not manifest_path.is_file():
            continue
        manifest = json.loads(manifest_path.read_text())
        contributions = manifest["contributes"]
        if not contributions or set(contributions) - {
            "tools",
            "background",
            "model_providers",
            "mcp",
        }:
            raise ValueError("unsupported plugin capability")
        targets = {"tools": "runtime", "background": "control", "mcp": "control"}
        declarations = []
        for capability, declaration in contributions.items():
            if capability == "model_providers":
                if not declaration.get("providers"):
                    raise ValueError("missing model provider definitions")
                adapter = declaration.get("adapter")
                if adapter is None:
                    continue
                declaration = adapter
                target = "control"
            else:
                target = targets[capability]
            if declaration["runtime"] != "node" or declaration["execution"] != target:
                raise ValueError("unsupported plugin execution target")
            declarations.append(declaration)
        package = destination / source.name
        package.mkdir(parents=True)
        shutil.copy2(manifest_path, package / "plugin.json")
        for name in sorted(
            {name for declaration in declarations for name in declaration["files"]}
            | {
                name
                for name in ("icon.svg", "icon.png", "icon-NOTICE.txt", "icon-LICENSE.txt")
                if (source / name).is_file()
            }
        ):
            relative = Path(name)
            if (
                relative.is_absolute()
                or ".." in relative.parts
                or any((source / part).is_symlink() for part in (relative, *relative.parents))
            ):
                raise ValueError("unsafe plugin file")
            target = package / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(source / relative, target)


def prepare_sandbox(destination):
    """Install integrity-locked SDK assets, then apply checked local extension."""
    source = ROOT / "sandbox"
    cached = BUILD / "sandbox-sdk"
    stamp = cached / ".pot-lock-sha256"
    expected = digest(source / "package-lock.json") + digest(source / "patch-sdk.mjs")
    if not stamp.exists() or stamp.read_text() != expected:
        cached.mkdir(parents=True, exist_ok=True)
        for name in ("package.json", "package-lock.json"):
            shutil.copy2(source / name, cached / name)
        call(
            [
                "npm",
                "ci",
                "--prefix",
                cached,
                "--ignore-scripts",
                "--omit=dev",
                "--no-audit",
                "--no-fund",
            ]
        )
        call(["node", source / "patch-sdk.mjs", cached])
        stamp.write_text(expected)
    copy_tree(cached, destination)
    shutil.copy2(source / "launcher.mjs", destination / "launcher.mjs")
    notices = []
    for manifest in sorted((cached / "node_modules").glob("**/package.json")):
        package = json.loads(manifest.read_text())
        license_file = next(
            (
                path
                for name in ("LICENSE", "LICENSE.txt", "LICENSE.md", "license", "license.txt")
                if (path := manifest.parent / name).is_file()
            ),
            None,
        )
        if license_file is not None:
            notices.append(
                f"{package.get('name', manifest.parent.name)} {package.get('version', '')}\n{license_file.read_text()}"
            )
    (destination / "THIRD-PARTY-NOTICES.txt").write_text("\n\n".join(notices) + "\n")


def update_metadata(resources, kind, target):
    """Record the complete installation contract beside its immutable assets."""
    import json

    version = os.environ.get("POT_APP_VERSION", "0.1.0")
    (resources / "update.json").write_text(
        json.dumps(
            {
                "version": version,
                "kind": kind,
                "platform": target,
                "storage_format": storage_format(),
            }
        )
        + "\n"
    )


def storage_format():
    source = (ROOT / "internal/selfupdate/release.go").read_text()
    return re.search(r'const StorageFormat = "([^"]+)"', source).group(1) + "-" + schema_digest()


def schema_digest():
    root = ROOT / "pkg/storage/migrations"
    digest = hashlib.sha256()
    for path in sorted(root.glob("*/*.sql")):
        digest.update(path.relative_to(root).as_posix().encode() + b"\0")
        digest.update(path.read_bytes() + b"\0")
    return digest.hexdigest()
