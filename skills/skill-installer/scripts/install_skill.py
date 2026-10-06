#!/usr/bin/env python3
"""Install validated local or GitHub bundles into Pot's writable skill directories."""

import argparse
import importlib.util
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import stat
import sys
import tempfile
from urllib.parse import quote, unquote, urlsplit
from urllib.request import Request, urlopen
import zipfile

validator_path = Path(__file__).resolve().parents[2] / "skill-creator/scripts/quick_validate.py"
spec = importlib.util.spec_from_file_location("pot_skill_validator", validator_path)
validator = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validator)


def github_source(repo, path, ref, url=None):
    if url:
        parsed = urlsplit(url)
        if parsed.scheme != "https" or parsed.netloc != "github.com":
            raise ValueError("Expected an HTTPS github.com URL")
        parts = parsed.path.strip("/").split("/")
        if len(parts) < 5 or parts[2] != "tree":
            raise ValueError("Expected a GitHub tree/ref/skill-path URL")
        repo, ref, path = "/".join(parts[:2]), unquote(parts[3]), unquote("/".join(parts[4:]))
    if not repo or not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", repo):
        raise ValueError("Expected owner/repo")
    if (
        not path
        or path.startswith("/")
        or any(p in ("", ".", "..") for p in path.split("/"))
        or "\\" in path
    ):
        raise ValueError("Skill path must be a relative directory inside the repository")
    if not ref or ref.startswith("-"):
        raise ValueError("Invalid Git ref")
    return repo, path, ref


def extract_bundle(archive, path, destination):
    """Extract only the selected directory, refusing traversal and special files."""
    with zipfile.ZipFile(archive) as zipped:
        roots = {info.filename.split("/")[0] for info in zipped.infolist()}
        if len(roots) != 1:
            raise ValueError("Unexpected repository archive layout")
        prefix = next(iter(roots)) + "/" + path + "/"
        found = False
        for info in zipped.infolist():
            if not info.filename.startswith(prefix) or info.filename == prefix:
                continue
            relative = info.filename[len(prefix) :]
            if (
                "\\" in relative
                or relative.startswith("/")
                or ".." in PurePosixPath(relative).parts
            ):
                raise ValueError("Archive path escapes selected bundle")
            mode = info.external_attr >> 16
            kind = stat.S_IFMT(mode)
            if kind not in (0, stat.S_IFREG, stat.S_IFDIR):
                raise ValueError("Archive contains a symlink or special file")
            target = destination / relative
            if info.is_dir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with zipped.open(info) as source, target.open("wb") as output:
                    shutil.copyfileobj(source, output)
                target.chmod(0o755 if mode & 0o111 else 0o644)
            found = True
        if not found:
            raise ValueError("Skill directory not found in repository archive")


def install_bundle(source, destination, replace=False):
    valid, message = validator.validate_skill(source)
    if not valid:
        raise ValueError(message)
    destination.mkdir(parents=True, exist_ok=True)
    target = destination / source.name
    if target.is_symlink() or (target.exists() and not target.is_dir()):
        raise ValueError("Destination is not an ordinary skill directory")
    if target.exists() and not replace:
        raise ValueError(f"Destination already exists: {target}")
    with tempfile.TemporaryDirectory(prefix=".skill-install-", dir=destination) as temp:
        stage = Path(temp) / source.name
        backup = Path(temp) / "previous"
        shutil.copytree(source, stage, symlinks=True)
        valid, message = validator.validate_skill(stage)
        if not valid:
            raise ValueError(message)
        for entry in [stage, *stage.rglob("*")]:
            mode = entry.lstat().st_mode
            if entry.is_dir():
                entry.chmod(0o755)
            elif entry.is_file() and not entry.is_symlink():
                entry.chmod(0o755 if mode & 0o111 else 0o644)
        valid, message = validator.validate_skill(stage)
        if not valid:
            raise ValueError(message)
        saved = False
        if target.exists():
            target.rename(backup)
            saved = True
        try:
            stage.rename(target)
        except OSError:
            if saved:
                backup.rename(target)
            raise
    return target


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    source = parser.add_mutually_exclusive_group(required=True)
    source.add_argument("--local", type=Path)
    source.add_argument("--repo")
    source.add_argument("--url")
    parser.add_argument("--path")
    parser.add_argument("--ref", default="main")
    parser.add_argument("--dest", type=Path, default=Path.home() / ".pot/agent/skills")
    parser.add_argument("--replace", action="store_true")
    args = parser.parse_args(argv)
    args.dest = args.dest.expanduser()
    if args.local:
        args.local = args.local.expanduser()
    try:
        if args.local:
            installed = install_bundle(args.local, args.dest, args.replace)
        else:
            repo, path, ref = github_source(args.repo, args.path, args.ref, args.url)
            with tempfile.TemporaryDirectory(prefix="pot-skill-download-") as temp:
                archive = Path(temp) / "repo.zip"
                bundle = Path(temp) / PurePosixPath(path).name
                headers = {"User-Agent": "pot-skill-installer"}
                token = os.environ.get("GH_TOKEN") or os.environ.get("GITHUB_TOKEN")
                if token:
                    headers["Authorization"] = f"Bearer {token}"
                request = Request(
                    f"https://codeload.github.com/{repo}/zip/{quote(ref, safe='')}", headers=headers
                )
                with urlopen(request, timeout=60) as response, archive.open("wb") as output:
                    shutil.copyfileobj(response, output)
                extract_bundle(archive, path, bundle)
                installed = install_bundle(bundle, args.dest, args.replace)
        print(f"Installed {installed}. Available on the next turn.")
        return 0
    except (OSError, ValueError, zipfile.BadZipFile) as error:
        print(f"Installation failed: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
