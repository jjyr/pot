"""Public verification orchestration; packaging/signature/OCI contracts stay shared."""

import argparse
import base64
from datetime import datetime, timezone
from concurrent.futures import ThreadPoolExecutor
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import plistlib
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.request
import zipfile

from macos_release import REQUIRED_RESOURCE_DIRECTORIES, REQUIRED_RESOURCE_FILES
from release_binary import embedded_go_strings
from release_contract import (
    ROOT,
    checksum_name,
    distribution_names,
    generate_cask,
    generate_formula,
    macos_release_notes,
    release_assets,
    update_artifacts,
    verify_oci_metadata,
    version_number,
)

CHANNELS = ("server", "desktop", "macos")  # container is published to GHCR, not GitHub
LIMIT = 16 * 1024 * 1024


class Incomplete(Exception):
    """Publication or installation is not yet complete."""

    def __init__(self, message, evidence=None):
        super().__init__(message)
        self.evidence = evidence


def require(condition, message):
    if not condition:
        raise ValueError(message)


class HTTPSOnly(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        require(newurl.startswith("https://"), "insecure download redirect")
        # Never forward even an anonymous registry bearer token to a CDN.
        result = super().redirect_request(req, fp, code, msg, headers, newurl)
        result.remove_header("Authorization")
        return result


class Network:
    """No ambient credentials, netrc, cookies or Docker auth; bounded HTTP retries."""

    def __init__(self):
        self.opener = urllib.request.build_opener(HTTPSOnly())
        self.retries = []

    def open(self, url, headers=None):
        require(url.startswith("https://"), "HTTPS required")
        for attempt in range(3):
            try:
                return self.opener.open(
                    urllib.request.Request(
                        url, headers={"User-Agent": "pot-verify-release", **(headers or {})}
                    ),
                    timeout=90,
                )
            except urllib.error.HTTPError as error:
                # Do not retry integrity errors, auth failures, or missing publication.
                retry = error.code in {429, 502, 503, 504} or (
                    error.code == 403 and error.headers.get("X-RateLimit-Remaining") == "0"
                )
                delay = error.headers.get("Retry-After", "2")
                delay = int(delay) if delay.isdigit() else 2
                if not retry or attempt == 2 or delay > 30:
                    if error.code == 404 or retry:
                        raise Incomplete(f"HTTP {error.code}: resource unavailable") from None
                    raise ValueError(f"HTTP {error.code}: anonymous access refused") from None
                self.retries.append({"http_status": error.code, "delay_seconds": delay})
                time.sleep(delay)

    def data(self, url, headers=None):
        with self.open(url, headers) as response:
            data = response.read(LIMIT + 1)
            require(len(data) <= LIMIT, "metadata exceeds limit")
            return data, response.headers

    def json(self, url, headers=None):
        return json.loads(self.data(url, headers)[0])

    def download(self, url, destination, size, digest=None, headers=None):
        require(isinstance(size, int) and size > 0, "invalid public byte size")
        h = hashlib.sha256()
        total = 0
        with self.open(url, headers) as response:
            with destination.open("wb") if destination else io.BytesIO() as output:
                while chunk := response.read(1024 * 1024):
                    total += len(chunk)
                    require(
                        total <= size,
                        f"download exceeds declared byte size: expected {size}, received at least {total}",
                    )
                    h.update(chunk)
                    if destination:
                        output.write(chunk)
        actual = h.hexdigest()
        require(total == size, f"truncated download: expected {size}, received {total}")
        if digest:
            require(
                digest == "sha256:" + actual,
                f"download SHA256 conflict: expected {digest}, received sha256:{actual}",
            )
        return {"size": total, "sha256": actual}


def expected_assets(version):
    return set().union(*(release_assets(version, channel) for channel in CHANNELS))


def validate_release(release, version, source, resolved):
    require(resolved == source, "release tag does not resolve to expected source")
    require(release["tag_name"] == "v" + version, "release tag mismatch")
    require(
        release["target_commitish"] in {source, "v" + version}, "release target source mismatch"
    )
    if release["draft"] or release["prerelease"]:
        raise Incomplete("stable release must be public and neither draft nor prerelease")
    names = [item["name"] for item in release["assets"]]
    require(len(names) == len(set(names)), "duplicate release asset filenames")
    expected = expected_assets(version)
    require(
        not (set(names) - expected), f"unexpected release assets: {sorted(set(names) - expected)}"
    )
    if expected - set(names):
        raise Incomplete(f"missing release assets: {sorted(expected - set(names))}")


def verify_manifest(text, names, downloads):
    entries = {}
    for line in text.splitlines():
        match = re.fullmatch(r"([a-f0-9]{64})  ([^/\\]+)", line)
        require(match is not None, "unsafe/malformed checksum manifest entry")
        digest, name = match.groups()
        require(
            name not in {".", ".."} and name not in entries, "duplicate/unsafe manifest filename"
        )
        entries[name] = digest
    require(set(entries) == set(names), "checksum manifest asset set mismatch")
    for name, digest in entries.items():
        require(downloads[name]["sha256"] == digest, f"checksum conflict: {name}")


def verify_feed(output, version, key, downloads, repository):
    result = subprocess.run(
        ["go", "run", "./scripts/sign-release", "verify"],
        cwd=ROOT,
        input=(output / "update.json").read_bytes(),
        capture_output=True,
        env=dict(os.environ, POT_UPDATE_PUBLIC_KEY=key),
        timeout=120,
    )
    require(result.returncode == 0, "Ed25519 feed signature verification failed with trusted key")
    feed = json.loads(result.stdout)
    require(feed["version"] == version, "signed feed version mismatch")
    require(
        isinstance(feed["storage_format"], str)
        and re.fullmatch(r".+-[a-f0-9]{64}", feed["storage_format"]),
        "invalid signed storage format/schema digest",
    )
    expected = {(kind, platform): name for kind, platform, name in update_artifacts(version)}
    require(len(feed["artifacts"]) == len(expected), "signed feed artifact count mismatch")
    seen = set()
    for item in feed["artifacts"]:
        identity = (item["kind"], item["platform"])
        require(
            identity in expected and identity not in seen, "duplicate/unexpected signed feed target"
        )
        seen.add(identity)
        name = expected[identity]
        require(
            item["url"] == f"https://github.com/{repository}/releases/download/v{version}/{name}",
            "signed feed URL mismatch",
        )
        require(
            (item["size"], item["sha256"]) == (downloads[name]["size"], downloads[name]["sha256"]),
            f"signed feed byte binding mismatch: {name}",
        )
    return feed


def safe_name(name):
    path = PurePosixPath(name)
    require(
        not path.is_absolute() and ".." not in path.parts and "\\" not in name,
        "archive path escapes installation",
    )
    return str(path)


class Archive:
    """Inspect regular files only; never extract trees or execute package contents."""

    def __init__(self, packed):
        self.packed = packed
        self.zip = isinstance(packed, zipfile.ZipFile)
        self.entries = {}
        for entry in packed.infolist() if self.zip else packed.getmembers():
            raw = entry.filename if self.zip else entry.name
            name = safe_name(raw)
            require(name not in self.entries, "duplicate archive path")
            self.entries[name] = entry
            link = None
            if self.zip and (entry.external_attr >> 16) & 0o170000 == 0o120000:
                require(entry.file_size <= 4096, "archive symlink too large")
                link = packed.read(entry).decode()
            if not self.zip:
                require(
                    entry.isfile() or entry.isdir() or entry.issym() or entry.islnk(),
                    "unsupported archive special file",
                )
                if entry.issym() or entry.islnk():
                    link = entry.linkname
            if link is not None:
                hardlink = not self.zip and entry.islnk()
                resolved = PurePosixPath(link) if hardlink else PurePosixPath(name).parent / link
                parts = []
                require(
                    not resolved.is_absolute() and "\\" not in link, "absolute/unsafe archive link"
                )
                for part in resolved.parts:
                    if part == "..":
                        require(len(parts) > 1, "archive link escapes installation")
                        parts.pop()
                    elif part != ".":
                        parts.append(part)

    def read(self, name, limit=LIMIT):
        entry = self.entries[name]
        if self.zip:
            require(
                not entry.is_dir() and (entry.external_attr >> 16) & 0o170000 != 0o120000,
                "metadata must be a regular file",
            )
            stream = self.packed.open(entry)
        else:
            require(entry.isfile(), "metadata must be a regular file")
            stream = self.packed.extractfile(entry)
        with stream:
            data = stream.read(limit + 1)
        require(len(data) <= limit, "archive member exceeds limit")
        return data

    def resources(self, root):
        for name in REQUIRED_RESOURCE_FILES:
            require(root + name in self.entries, f"missing resource: {name}")
        for directory in REQUIRED_RESOURCE_DIRECTORIES:
            require(
                any(name.startswith(root + directory + "/") for name in self.entries),
                f"missing resource directory: {directory}",
            )


def verify_binary(data, version, source, key, storage, platform):
    with tempfile.TemporaryDirectory(prefix="pot-inspect-") as temporary:
        binary = Path(temporary) / "binary"
        binary.write_bytes(data)  # no executable permissions; go only reads build info
        info = subprocess.check_output(["go", "version", "-m", binary], text=True, timeout=30)
    system, architecture = platform.split("-")
    require("\tbuild\tGOOS=" + system in info.splitlines(), "binary OS mismatch")
    require("\tbuild\tGOARCH=" + architecture in info.splitlines(), "binary architecture mismatch")
    embedded_go_strings(
        data,
        {
            "version": version,
            "source": source,
            "public_key": key,
            "schema_digest": storage.rsplit("-", 1)[-1],
        },
    )
    require("\tbuild\tvcs.revision=" + source in info.splitlines(), "binary VCS source mismatch")
    require("\tbuild\tvcs.modified=false" in info.splitlines(), "binary built from dirty source")


def verify_packages(output, version, source, key, feed):
    records = []
    for kind, platform, name in update_artifacts(version):
        packed = zipfile.ZipFile(output / name) if kind == "app" else tarfile.open(output / name)
        with packed:
            archive = Archive(packed)
            root = "Pot.app/Contents/" if kind == "app" else f"pot-{platform}/"
            require(
                all(
                    n == root.split("/")[0]
                    or n.startswith(root.split("/")[0] + "/")
                    or (kind == "app" and (n == "__MACOSX" or n.startswith("__MACOSX/")))
                    for n in archive.entries
                ),
                "unexpected installation root",
            )
            metadata = json.loads(archive.read(root + "Resources/update.json"))
            require(
                metadata
                == {
                    "version": version,
                    "kind": kind,
                    "platform": platform,
                    "storage_format": feed["storage_format"],
                },
                "installation metadata mismatch",
            )
            archive.resources(root + "Resources/")
            if kind == "app":
                info = plistlib.loads(archive.read(root + "Info.plist"))
                require(
                    info["CFBundleShortVersionString"] == version
                    and info["CFBundleVersion"] == version,
                    "App version mismatch",
                )
            binary = root + ("MacOS/pot-app" if kind == "app" else "bin/pot")
            verify_binary(
                archive.read(binary, 512 * 1024 * 1024),
                version,
                source,
                key,
                feed["storage_format"],
                platform,
            )
            records.append({"archive": name, "kind": kind, "platform": platform, "source": source})
    # Debian envelope and embedded binaries: read only, no dpkg/install hooks.
    with (output / f"pot-desktop_{version}_linux_amd64.deb").open("rb") as package:
        require(package.read(8) == b"!<arch>\n", "invalid Debian ar envelope")
        members = {}
        while header := package.read(60):
            require(len(header) == 60 and header[58:] == b"`\n", "truncated Debian ar header")
            name, size = header[:16].decode().strip().rstrip("/"), int(header[48:58])
            require(
                name not in members and 0 <= size <= 1024 * 1024 * 1024, "invalid Debian member"
            )
            members[name] = package.read(size)
            require(len(members[name]) == size, "truncated Debian member")
            if size % 2:
                require(len(package.read(1)) == 1, "truncated Debian padding")
        require(members.get("debian-binary") == b"2.0\n", "invalid Debian format")
        with tarfile.open(fileobj=io.BytesIO(members["control.tar.gz"])) as packed:
            control = Archive(packed).read("control").decode()
            require(
                f"Version: {version}\n" in control and "Architecture: amd64\n" in control,
                "Debian version/architecture mismatch",
            )
        with tarfile.open(fileobj=io.BytesIO(members["data.tar.gz"])) as packed:
            archive = Archive(packed)
            root = "opt/pot-desktop/"
            archive.resources(root + "Resources/")
            require(
                root + "Resources/update.json" not in archive.entries,
                "desktop must not advertise self-update",
            )
            for binary in ["pot", "pot-app"]:
                verify_binary(
                    archive.read(root + "bin/" + binary, 512 * 1024 * 1024),
                    version,
                    source,
                    key,
                    feed["storage_format"],
                    "linux-amd64",
                )
    return records


def verify_registry(network, repository, version, source, mode):
    repository = repository.lower()
    base = f"https://ghcr.io/v2/{repository}/"
    # Anonymous, pull-only bearer token; never read Docker credentials or persist it.
    token = network.json(
        f"https://ghcr.io/token?service=ghcr.io&scope=repository:{repository}:pull"
    )["token"]
    headers = {
        "Authorization": "Bearer " + token,
        "Accept": "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json",
    }
    index, response_headers = network.data(base + "manifests/" + version, headers)
    digest = "sha256:" + hashlib.sha256(index).hexdigest()
    require(
        response_headers.get("Docker-Content-Digest") == digest, "registry index digest mismatch"
    )
    manifests = set()

    # Identify manifests from the index, including nested indexes, without probing blob endpoints.
    def reader(digest):
        collection = "manifests/" if digest in manifests else "blobs/"
        data = network.data(base + collection + digest, headers)[0]
        document = json.loads(data)
        manifests.update(d["digest"] for d in document.get("manifests", []))
        return data

    document = json.loads(index)
    require("manifests" in document, "version must name a multiarchitecture index")
    manifests.update(d["digest"] for d in document["manifests"])
    result = verify_oci_metadata(document, reader, version, source)
    require(result["layers"], "OCI images have no layer descriptors")
    result.update(
        index_digest=digest,
        mode=mode,
        runtime_smoke="not_run: native build jobs own runtime qualification",
    )
    if mode == "full":

        def pull(item):
            digest, size = item
            network.download(base + "blobs/" + digest, None, size, digest, headers)
            return {"digest": digest, "size": size}

        result["anonymous_pulled_layers"] = []
        try:
            with ThreadPoolExecutor(max_workers=3) as pool:
                for receipt in pool.map(pull, result["layers"].items()):
                    result["anonymous_pulled_layers"].append(receipt)
        except Exception as error:
            error.evidence = result
            raise
    else:
        raise Incomplete("OCI metadata passed; full anonymous layer pull was not requested", result)
    return result


def homebrew_fields(text):
    fields = {}
    for name in ["version", "sha256", "url"]:
        values = re.findall(r"^[ \t]*" + name + r' "([^"\n]+)"[ \t]*$', text, re.MULTILINE)
        require(len(values) == 1, f"Homebrew requires exactly one active {name} field")
        fields[name] = values[0]
    require(re.fullmatch(r"[a-f0-9]{64}", fields["sha256"]), "invalid Homebrew SHA256")
    return fields


def verify_brew(network, repository, version, output, phase):
    generate_cask(version, output)
    generate_formula(version, output)
    statuses = []
    snapshot = network.json(f"https://api.github.com/repos/{repository}/commits/HEAD")["sha"]
    require(re.fullmatch(r"[a-f0-9]{40}", snapshot), "invalid Homebrew snapshot source")
    for remote, generated in [("Casks/pot.rb", "pot.rb"), ("Formula/pot-cli.rb", "pot-cli.rb")]:
        text = network.data(f"https://raw.githubusercontent.com/{repository}/{snapshot}/{remote}")[
            0
        ].decode()
        expected = (output / generated).read_text()
        actual, wanted = homebrew_fields(text), homebrew_fields(expected)
        if actual == wanted:
            statuses.append({"path": remote, "status": "passed", "snapshot_source": snapshot})
            continue
        current = version_number(actual["version"])
        if tuple(map(int, current.split("."))) >= tuple(map(int, version.split("."))):
            raise ValueError(f"conflicting/newer Homebrew pointer: {remote}")
        statuses.append(
            {
                "path": remote,
                "status": "pending_update",
                "current_version": current,
                "snapshot_source": snapshot,
            }
        )
    if phase == "installation" and any(item["status"] != "passed" for item in statuses):
        raise Incomplete("published bytes verified; Homebrew pointers still await merge", statuses)
    return statuses


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("version", type=version_number)
    parser.add_argument("source", help="Trusted expected 40-character source SHA (required)")
    parser.add_argument(
        "--public-key",
        default=os.environ.get("POT_UPDATE_PUBLIC_KEY"),
        help="Trusted pinned base64 Ed25519 key; or POT_UPDATE_PUBLIC_KEY",
    )
    parser.add_argument("--macos-mode", choices=["signed", "unsigned"], required=True)
    parser.add_argument("--phase", choices=["publication", "installation"], default="installation")
    parser.add_argument("--oci", choices=["full", "metadata"], default="full")
    parser.set_defaults(repository="jjyr/pot")
    parser.add_argument("--output", type=Path, default=Path(".local/release-verification"))
    parser.add_argument("--markdown", action="store_true")
    args = parser.parse_args(argv)
    args.output.mkdir(parents=True, exist_ok=True)
    report = {
        "verified_at": datetime.now(timezone.utc).isoformat(),
        "version": args.version,
        "expected_source": args.source,
        "expected_macos_mode": args.macos_mode,
        "trusted_key_sha256": hashlib.sha256((args.public_key or "").encode()).hexdigest(),
        "phase": args.phase,
        "scope": "public_bytes_and_metadata",
        "checks": [],
        "limitations": [
            "Static Go string globals and VCS inspection; package executables and --version are never run.",
            "Native CLI/App/WebView smoke and codesign/notarization are build-job qualifications, not performed here.",
            "OCI full mode transfers and hashes every layer for both architectures; no container runtime is invoked.",
            "Signed feed has no source field: source is checked in every bound package binary and macOS disclosure.",
        ],
    }
    network = Network()

    def check(name, operation):
        try:
            evidence = operation()
            pending = name == "homebrew" and any(item["status"] != "passed" for item in evidence)
            status = "pending_update" if pending else "passed"
            report["checks"].append({"name": name, "status": status, "evidence": evidence})
            print(f"{status.upper()} {name}", flush=True)
            return evidence
        except Exception as error:
            status = "incomplete" if isinstance(error, Incomplete) else "failed"
            report["checks"].append({"name": name, "status": status, "detail": str(error)})
            if getattr(error, "evidence", None) is not None:
                report["checks"][-1]["evidence"] = error.evidence
            print(f"{status.upper()} {name}: {error}", flush=True)
            return None

    def preflight():
        require(sys.version_info >= (3, 12), "Python 3.12 or newer is required")
        require(
            re.fullmatch(r"[a-f0-9]{40}", args.source),
            "expected source must be full lowercase commit SHA",
        )
        require(
            re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", args.repository), "invalid repository"
        )
        require(
            shutil.which("go"),
            "Go is required for existing Ed25519 verifier and static binary inspection",
        )
        require(
            args.public_key and len(base64.b64decode(args.public_key, validate=True)) == 32,
            "trusted pinned public key required",
        )
        return {
            "go": shutil.which("go"),
            "go_version": subprocess.check_output(["go", "version"], text=True, timeout=15).strip(),
            "python_version": sys.version.split()[0],
            "anonymous_network": True,
        }

    if check("preflight", preflight):

        def assets():
            api = f"https://api.github.com/repos/{args.repository}"
            release = network.json(api + "/releases/tags/v" + args.version)
            resolved = network.json(api + "/commits/refs/tags/v" + args.version)["sha"]
            report["observed_release"] = {
                name: release[name]
                for name in ["id", "tag_name", "target_commitish", "draft", "prerelease"]
            }
            report["observed_release"]["resolved_source"] = resolved
            report["observed_release"]["assets"] = [
                {key: asset.get(key) for key in ["name", "size", "digest"]}
                for asset in release["assets"]
            ]
            validate_release(release, args.version, args.source, resolved)
            downloads = {}
            report["downloads"] = downloads
            with tempfile.TemporaryDirectory(prefix="pot-public-release-") as temporary:
                output = Path(temporary)
                for asset in release["assets"]:
                    name = asset["name"]
                    report["asset_in_progress"] = name
                    url = f"https://github.com/{args.repository}/releases/download/v{args.version}/{name}"
                    require(asset["browser_download_url"] == url, "unexpected asset URL")
                    if not any(
                        name in distribution_names(args.version, channel) for channel in CHANNELS
                    ):
                        require(asset["size"] <= LIMIT, "release metadata exceeds size limit")
                    downloads[name] = network.download(
                        url, output / name, asset["size"], asset.get("digest")
                    )
                report.pop("asset_in_progress", None)
                for channel in CHANNELS:
                    names = distribution_names(args.version, channel)
                    for name in names:
                        verify_manifest(
                            (output / (name + ".sha256")).read_text(), [name], downloads
                        )
                    if channel == "macos":
                        names += ["update.json", "MACOS-RELEASE.txt"]
                    verify_manifest((output / checksum_name(channel)).read_text(), names, downloads)
                feed = verify_feed(
                    output, args.version, args.public_key, downloads, args.repository
                )
                report["signed_feed"] = feed
                check(
                    "packages",
                    lambda: verify_packages(
                        output, args.version, args.source, args.public_key, feed
                    ),
                )
                require(
                    (output / "MACOS-RELEASE.txt").read_text()
                    == macos_release_notes(args.version, args.source, args.macos_mode),
                    "macOS mode/version/source disclosure mismatch",
                )
                check(
                    "homebrew",
                    lambda: verify_brew(network, args.repository, args.version, output, args.phase),
                )
            return {
                "release_id": release["id"],
                "tag": release["tag_name"],
                "source": resolved,
                "anonymous_assets": len(downloads),
                "macos_mode": args.macos_mode,
            }

        check("public_assets_and_signed_feed", assets)
        check(
            "ghcr",
            lambda: verify_registry(network, args.repository, args.version, args.source, args.oci),
        )
    names = {item["name"] for item in report["checks"]}
    for name in ["public_assets_and_signed_feed", "packages", "homebrew", "ghcr"]:
        if name not in names:
            report["checks"].append(
                {
                    "name": name,
                    "status": "blocked",
                    "detail": "prerequisite verification did not complete",
                }
            )
    report["native_qualification"] = {
        "status": "not_run",
        "required_for_public_byte_verification": False,
        "detail": "Native CLI --version/service, App/WebView, codesign/notarization and container execution belong to prepublication platform jobs; static inspection cannot qualify them.",
    }
    states = {item["status"] for item in report["checks"]}
    report["status"] = (
        "failed" if "failed" in states else "incomplete" if "incomplete" in states else "passed"
    )
    report["network_retries"] = network.retries
    (args.output / "report.json").write_text(json.dumps(report, indent=2) + "\n")
    if args.markdown:
        lines = [
            f"Release v{args.version}: {report['status']}",
            "",
            f"Expected source: `{args.source}`",
            "",
        ]
        lines += [
            f"- {c['name']}: **{c['status']}**" + (": " + c["detail"] if "detail" in c else "")
            for c in report["checks"]
        ]
        lines += [
            "",
            "Native qualification: **not_run** (owned by platform build jobs).",
            "",
            "Scope and limitations:",
            "",
        ] + ["- " + item for item in report["limitations"]]
        (args.output / "report.md").write_text("\n".join(lines) + "\n")
    print(f"{report['status'].upper()}: {args.output / 'report.json'}")
    return {"passed": 0, "failed": 1, "incomplete": 2}[report["status"]]
