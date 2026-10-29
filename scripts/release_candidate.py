"""Authenticate immutable preparation artifacts before creating a release tag."""

import argparse
import base64
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tempfile
from urllib.parse import quote
import zipfile

from release_contract import (
    checksum,
    checksum_name,
    distribution_names,
    macos_mode,
    macos_release_notes,
    release_assets,
    update_artifacts,
    version_number,
)

REPOSITORY = "jjyr/pot"
WORKFLOW = ".github/workflows/release-prepare.yml"
RECEIPT = ".release-candidate.json"
MANIFEST_MAX = 1024 * 1024
MANIFEST_KEYS = {
    "format",
    "repository",
    "version",
    "sha",
    "mode",
    "run_id",
    "run_attempt",
    "workflow_id",
    "workflow_sha",
    "workflow_path",
    "bundle",
    "files",
}
BUNDLE_KEYS = {"id", "name", "digest", "size_in_bytes"}


class Error(ValueError):
    """The selected candidate could not be authenticated."""


def require(condition, message):
    if not condition:
        raise Error(message)


def positive(value):
    return type(value) is int and value > 0


def sha(value, length=40):
    return isinstance(value, str) and re.fullmatch(r"[a-f0-9]{%d}" % length, value) is not None


def canonical(value):
    return json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n"


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate JSON key")
        result[key] = value
    return result


def decode(raw):
    return json.loads(raw, object_pairs_hook=unique_object)


def api(endpoint, method="GET", data=None, missing=False):
    command = [
        "gh",
        "api",
        "--hostname",
        "github.com",
        "repos/" + REPOSITORY + ("/" + endpoint if endpoint else ""),
        "--method",
        method,
    ]
    if data is not None:
        command += ["--input", "-"]
    try:
        result = subprocess.run(
            command,
            input=canonical(data) if data is not None else None,
            capture_output=True,
            text=True,
            timeout=30,
            check=False,
        )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise Error("GitHub API unavailable") from error
    if result.returncode:
        try:
            body = decode(result.stdout)
        except (ValueError, TypeError):
            body = {}
        if missing and isinstance(body, dict) and body.get("status") in (404, "404"):
            return None
        raise Error("GitHub API request failed: " + endpoint)
    return decode(result.stdout)


def download(artifact, path, timeout=900):
    # gh follows GitHub's signed artifact redirect; downloaded contents are data.
    try:
        with Path(path).open("xb") as output:
            result = subprocess.run(
                [
                    "gh",
                    "api",
                    "--hostname",
                    "github.com",
                    f"repos/{REPOSITORY}/actions/artifacts/{artifact['id']}/zip",
                ],
                stdout=output,
                stderr=subprocess.PIPE,
                timeout=timeout,
                check=False,
            )
    except (OSError, subprocess.TimeoutExpired) as error:
        raise Error("artifact download unavailable") from error
    require(result.returncode == 0, "artifact download failed")
    require(
        Path(path).stat().st_size == artifact["size_in_bytes"], "artifact archive size mismatch"
    )
    require(
        "sha256:" + checksum(Path(path)) == artifact["digest"], "artifact archive digest mismatch"
    )


def timestamp(value):
    require(isinstance(value, str), "missing artifact/run timestamp")
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    require(parsed.tzinfo is not None, "timestamp lacks timezone")
    return parsed


def repository():
    data = api("")
    require(
        data.get("full_name") == REPOSITORY and data.get("default_branch") == "main",
        "official repository/default branch mismatch",
    )
    return data


def main_sha():
    repository()
    value = api("commits/heads/main").get("sha")
    require(sha(value), "invalid main commit")
    return value


def ancestry(base, head):
    require(sha(base) and sha(head), "invalid ancestry commit")
    require(
        api(f"compare/{base}...{head}").get("status") in ("identical", "ahead"),
        "source/workflow is outside trusted main history",
    )


def trusted_environment():
    for key, expected in {
        "GITHUB_REPOSITORY": REPOSITORY,
        "GITHUB_EVENT_NAME": "workflow_dispatch",
        "GITHUB_REF": "refs/heads/main",
        "GITHUB_WORKFLOW_REF": f"{REPOSITORY}/{WORKFLOW}@refs/heads/main",
    }.items():
        require(os.environ.get(key) == expected, "untrusted preparation environment: " + key)
    value = os.environ.get("GITHUB_SHA")
    require(sha(value), "invalid workflow SHA")
    return value


def validate_run(run_id, completed=True, attempt=None):
    require(
        positive(run_id) and (attempt is None or positive(attempt)), "invalid preparation identity"
    )
    main = main_sha()
    workflow = api("actions/workflows/" + WORKFLOW.rsplit("/", 1)[1])
    require(
        workflow.get("path") == WORKFLOW
        and workflow.get("state") == "active"
        and positive(workflow.get("id")),
        "untrusted preparation workflow",
    )
    current = api(f"actions/runs/{run_id}")
    require(positive(current.get("run_attempt")), "invalid current attempt")
    attempt = current["run_attempt"] if attempt is None else attempt
    require(attempt <= current["run_attempt"], "preparation attempt does not exist")
    pinned = api(f"actions/runs/{run_id}/attempts/{attempt}")
    for run in (current, pinned):
        require(
            run.get("id") == run_id
            and run.get("workflow_id") == workflow["id"]
            and run.get("path") == WORKFLOW,
            "preparation workflow identity mismatch",
        )
        require(
            all(
                run.get(key, {}).get("full_name") == REPOSITORY
                for key in ("repository", "head_repository")
            ),
            "foreign preparation repository",
        )
        require(
            run.get("event") == "workflow_dispatch"
            and run.get("head_branch") == "main"
            and run.get("pull_requests") == [],
            "untrusted preparation event/ref",
        )
        require(sha(run.get("head_sha")), "invalid workflow commit")
    require(
        pinned.get("run_attempt") == attempt and pinned["head_sha"] == current["head_sha"],
        "preparation attempt identity mismatch",
    )
    if completed:
        require(
            pinned.get("status") == "completed" and pinned.get("conclusion") == "success",
            "selected preparation attempt was not successful",
        )
    else:
        require(current["run_attempt"] == attempt, "cannot finalize an older attempt")
        require(
            pinned.get("status") == "in_progress"
            or (pinned.get("status") == "completed" and pinned.get("conclusion") == "success"),
            "preparation is not running/successful",
        )
    timestamp(pinned.get("run_started_at"))
    ancestry(pinned["head_sha"], main)
    return pinned


def artifact(artifact_id, run, kind, require_fresh=True):
    require(positive(artifact_id), "invalid artifact ID")
    data = api(f"actions/artifacts/{artifact_id}")
    require(
        data.get("id") == artifact_id
        and data.get("name") == f"release-{kind}-{run['id']}-{run['run_attempt']}",
        "artifact identity/attempt mismatch",
    )
    origin = data.get("workflow_run", {})
    require(
        origin.get("id") == run["id"] and origin.get("head_sha") == run["head_sha"],
        "artifact preparation mismatch",
    )
    require(
        origin.get("repository_id") == run["repository"]["id"]
        and origin.get("head_repository_id") == run["head_repository"]["id"],
        "artifact repository identity mismatch",
    )
    require(type(data.get("expired")) is bool, "missing expiration state")
    expired = data["expired"] or timestamp(data.get("expires_at")) <= datetime.now(timezone.utc)
    require(not require_fresh or not expired, "candidate artifact expired")
    require(
        timestamp(data.get("created_at")) >= timestamp(run["run_started_at"]),
        "artifact predates selected attempt",
    )
    require(
        isinstance(data.get("digest"), str)
        and re.fullmatch(r"sha256:[a-f0-9]{64}", data["digest"]),
        "artifact has no authenticated digest",
    )
    require(positive(data.get("size_in_bytes")), "invalid artifact size")
    return dict(data, expired=expired)


def expected_names(version):
    return set().union(
        *(
            release_assets(version, channel)
            for channel in ("server", "desktop", "macos", "container")
        )
    ) | {"pot.rb", "pot-cli.rb"}


def safe_path(path):
    path = Path(path).absolute()
    require(not path.is_symlink(), "symlink path rejected")
    # Canonicalize platform aliases (for example macOS /var) before staging.
    return path.parent.resolve() / path.name


def files_in(directory, receipt=False):
    directory = safe_path(directory)
    require(directory.is_dir(), "candidate directory missing")
    result = {}
    for path in directory.iterdir():
        require(
            not path.is_symlink() and stat.S_ISREG(path.stat().st_mode) and path.stat().st_size > 0,
            "candidate contains non-regular/empty file",
        )
        if receipt and path.name == RECEIPT:
            continue
        result[path.name] = path
    return result


def verify_files(directory, entries, receipt=False):
    paths = files_in(directory, receipt)
    require(set(paths) == {item["name"] for item in entries}, "candidate file set mismatch")
    for item in entries:
        path = paths[item["name"]]
        require(
            path.stat().st_size == item["size"] and checksum(path) == item["sha256"],
            "candidate file hash/size mismatch: " + path.name,
        )


def validate_feed(directory, version):
    # Crypto authentication is a separate trusted workflow step. Bind that
    # signed payload to the exact four update packages before making any tag.
    path = Path(directory) / "update.json"
    require(path.stat().st_size <= MANIFEST_MAX, "update feed too large")
    envelope = decode(path.read_bytes())
    require(
        isinstance(envelope, dict) and set(envelope) == {"payload", "signature"},
        "invalid update envelope",
    )
    payload = decode(base64.b64decode(envelope["payload"], validate=True))
    require(
        len(base64.b64decode(envelope["signature"], validate=True)) == 64,
        "invalid update signature encoding",
    )
    require(
        payload.get("version") == version and payload.get("protocol") == 2,
        "update feed version/protocol differs from candidate",
    )
    entries = payload.get("artifacts")
    require(
        isinstance(entries, list) and len(entries) == 4,
        "update feed must describe all four packages",
    )
    expected = []
    for kind, platform, name in update_artifacts(version):
        artifact_path = Path(directory) / name
        expected.append(
            {
                "kind": kind,
                "platform": platform,
                "url": f"https://github.com/{REPOSITORY}/releases/download/v{version}/{name}",
                "sha256": checksum(artifact_path),
                "size": artifact_path.stat().st_size,
            }
        )
    require(
        sorted(map(canonical, entries)) == sorted(map(canonical, expected)),
        "signed update payload does not match candidate packages",
    )


def validate_contents(directory, version, source, mode):
    directory = Path(directory)
    validate_feed(directory, version)
    require(
        (directory / "MACOS-RELEASE.txt").read_text() == macos_release_notes(version, source, mode),
        "macOS disclosure does not match candidate identity",
    )
    for channel in ("server", "desktop", "macos", "container"):
        names = distribution_names(version, channel)
        for name in names:
            require(
                (directory / (name + ".sha256")).read_text()
                == f"{checksum(directory / name)}  {name}\n",
                "distribution checksum sidecar mismatch",
            )
        if channel == "macos":
            names += ["update.json", "MACOS-RELEASE.txt"]
        expected = "".join(f"{checksum(directory / name)}  {name}\n" for name in names)
        require(
            (directory / checksum_name(channel)).read_text() == expected,
            "channel checksum manifest mismatch",
        )


def safe_zip(archive, allowed, destination, manifest=False):
    with zipfile.ZipFile(archive) as packed:
        infos = packed.infolist()
        require(
            len(infos) == len(allowed) and {info.filename for info in infos} == set(allowed),
            "artifact ZIP file set mismatch",
        )
        for info in infos:
            mode = stat.S_IFMT(info.external_attr >> 16)
            require(
                info.filename not in ("", ".", "..")
                and "/" not in info.filename
                and "\\" not in info.filename,
                "unsafe ZIP path",
            )
            require(
                not info.is_dir() and mode in (0, stat.S_IFREG) and not info.flag_bits & 1,
                "non-regular/encrypted ZIP entry",
            )
            require(
                (
                    info.file_size <= MANIFEST_MAX
                    if manifest
                    else info.file_size == allowed[info.filename]
                ),
                "ZIP entry size mismatch",
            )
        destination = Path(destination)
        destination.mkdir()
        for info in infos:
            with packed.open(info) as source, (destination / info.filename).open("xb") as output:
                shutil.copyfileobj(source, output, 1024 * 1024)


def validate_manifest(manifest, run):
    require(
        isinstance(manifest, dict)
        and set(manifest) == MANIFEST_KEYS
        and type(manifest["format"]) is int
        and manifest["format"] == 1,
        "invalid manifest format/fields",
    )
    require(
        manifest["repository"] == REPOSITORY and manifest["workflow_path"] == WORKFLOW,
        "foreign manifest",
    )
    for key, value in {
        "run_id": run["id"],
        "run_attempt": run["run_attempt"],
        "workflow_id": run["workflow_id"],
    }.items():
        require(
            positive(manifest[key]) and manifest[key] == value,
            "manifest preparation identity mismatch",
        )
    require(
        manifest["workflow_sha"] == run["head_sha"] and sha(manifest["sha"]),
        "manifest source identity mismatch",
    )
    require(
        isinstance(manifest["version"], str)
        and version_number(manifest["version"]) == manifest["version"],
        "invalid manifest version",
    )
    macos_mode(manifest["mode"])
    ancestry(manifest["sha"], run["head_sha"])
    ancestry(manifest["sha"], main_sha())
    require(
        isinstance(manifest["bundle"], dict) and set(manifest["bundle"]) == BUNDLE_KEYS,
        "invalid bundle descriptor",
    )
    require(isinstance(manifest["files"], list), "invalid file manifest")
    names = []
    for item in manifest["files"]:
        require(
            isinstance(item, dict) and set(item) == {"name", "sha256", "size"},
            "invalid file descriptor",
        )
        require(
            isinstance(item["name"], str) and sha(item["sha256"], 64) and positive(item["size"]),
            "invalid file hash/size",
        )
        names.append(item["name"])
    require(
        len(names) == len(set(names)) and set(names) == expected_names(manifest["version"]),
        "incomplete/extra candidate files",
    )


def load_remote(run_id, manifest_id, require_fresh=True):
    require(positive(run_id) and positive(manifest_id), "invalid candidate identity")
    selected = api(f"actions/artifacts/{manifest_id}")
    match = re.fullmatch(rf"release-candidate-{run_id}-([1-9][0-9]*)", selected.get("name", ""))
    require(match is not None, "manifest artifact identity mismatch")
    run = validate_run(run_id, attempt=int(match.group(1)))
    metadata = artifact(manifest_id, run, "candidate", require_fresh)
    require(metadata["size_in_bytes"] <= 2 * MANIFEST_MAX, "manifest artifact too large")
    with tempfile.TemporaryDirectory(prefix="pot-candidate-manifest-") as temporary:
        archive = Path(temporary) / "manifest.zip"
        download(metadata, archive, timeout=30)
        extracted = Path(temporary) / "files"
        safe_zip(archive, {"candidate.json": None}, extracted, manifest=True)
        raw = (extracted / "candidate.json").read_bytes()
    manifest = decode(raw)
    require(canonical(manifest).encode() == raw, "manifest is not canonical JSON")
    validate_manifest(manifest, run)
    bundle = artifact(manifest["bundle"]["id"], run, "bundle", require_fresh)
    require(
        {key: bundle[key] for key in BUNDLE_KEYS} == manifest["bundle"],
        "bundle ID/digest metadata changed",
    )
    return manifest, metadata, bundle


def result_for(manifest, metadata, bundle):
    return {
        "version": manifest["version"],
        "sha": manifest["sha"],
        "mode": manifest["mode"],
        "run_id": manifest["run_id"],
        "run_attempt": manifest["run_attempt"],
        "manifest_id": metadata["id"],
        "manifest_digest": hashlib.sha256(canonical(manifest).encode()).hexdigest(),
        "bundle_id": bundle["id"],
        "expires_at": min(metadata["expires_at"], bundle["expires_at"], key=timestamp),
        "expired": metadata["expired"] or bundle["expired"],
    }


def inspect_candidate(run_id, manifest_id, require_fresh=True):
    try:
        return result_for(*load_remote(run_id, manifest_id, require_fresh))
    except (ValueError, OSError, KeyError, TypeError, zipfile.BadZipFile) as error:
        if isinstance(error, Error):
            raise
        raise Error("candidate metadata unavailable or invalid") from error


def resolve(version, ref="main"):
    version = version_number(version)
    workflow_sha = trusted_environment()
    ancestry(workflow_sha, main_sha())
    require(isinstance(ref, str) and bool(ref), "source ref is required")
    source = (
        workflow_sha
        if ref in ("main", "refs/heads/main", "heads/main")
        else api("commits/" + quote(ref, safe="")).get("sha")
    )
    ancestry(source, workflow_sha)
    ancestry(source, main_sha())
    require(api("git/ref/tags/v" + version, missing=True) is None, "release tag already exists")
    return {"version": version, "sha": source}


def finalize(version, sha_value, mode, directory, artifact_id, output):
    version, mode = version_number(version), macos_mode(mode)
    workflow_sha = trusted_environment()
    run = validate_run(
        int(os.environ["GITHUB_RUN_ID"]),
        completed=False,
        attempt=int(os.environ["GITHUB_RUN_ATTEMPT"]),
    )
    require(run["head_sha"] == workflow_sha, "preparation workflow SHA mismatch")
    ancestry(sha_value, workflow_sha)
    ancestry(sha_value, main_sha())
    require(api("git/ref/tags/v" + version, missing=True) is None, "release tag already exists")
    paths = files_in(directory)
    require(set(paths) == expected_names(version), "final bundle file set mismatch")
    validate_contents(directory, version, sha_value, mode)
    bundle = artifact(artifact_id, run, "bundle")
    manifest = {
        "format": 1,
        "repository": REPOSITORY,
        "version": version,
        "sha": sha_value,
        "mode": mode,
        "run_id": run["id"],
        "run_attempt": run["run_attempt"],
        "workflow_id": run["workflow_id"],
        "workflow_sha": workflow_sha,
        "workflow_path": WORKFLOW,
        "bundle": {key: bundle[key] for key in BUNDLE_KEYS},
        "files": [
            {"name": name, "sha256": checksum(path), "size": path.stat().st_size}
            for name, path in sorted(paths.items())
        ],
    }
    output = safe_path(output)
    require(
        not output.is_relative_to(safe_path(directory)), "manifest must be outside uploaded bundle"
    )
    output.parent.mkdir(parents=True, exist_ok=True)
    with output.open("x") as destination:
        destination.write(canonical(manifest))
    return {
        "version": version,
        "sha": sha_value,
        "mode": mode,
        "run_id": run["id"],
        "run_attempt": run["run_attempt"],
        "bundle_id": artifact_id,
    }


def fetch(run_id, manifest_id, directory):
    target = safe_path(directory)
    require(
        not target.exists() or (target.is_dir() and not any(target.iterdir())),
        "fetch requires an empty destination",
    )
    target.parent.mkdir(parents=True, exist_ok=True)
    manifest, metadata, bundle = load_remote(run_id, manifest_id)
    with tempfile.TemporaryDirectory(prefix=".pot-candidate-", dir=target.parent) as temporary:
        archive = Path(temporary) / "bundle.zip"
        download(bundle, archive)
        stage = Path(temporary) / "files"
        safe_zip(archive, {item["name"]: item["size"] for item in manifest["files"]}, stage)
        verify_files(stage, manifest["files"])
        validate_contents(stage, manifest["version"], manifest["sha"], manifest["mode"])
        (stage / RECEIPT).write_text(canonical({"run_id": run_id, "manifest_id": manifest_id}))
        if target.exists():
            target.rmdir()
        stage.rename(target)
    return result_for(manifest, metadata, bundle)


def verify(directory):
    path = safe_path(directory) / RECEIPT
    require(
        not path.is_symlink() and path.is_file() and path.stat().st_size <= MANIFEST_MAX,
        "candidate receipt missing/unsafe",
    )
    receipt = decode(path.read_bytes())
    require(
        isinstance(receipt, dict) and set(receipt) == {"run_id", "manifest_id"},
        "invalid candidate receipt",
    )
    manifest, metadata, bundle = load_remote(receipt["run_id"], receipt["manifest_id"])
    verify_files(directory, manifest["files"], receipt=True)
    validate_contents(directory, manifest["version"], manifest["sha"], manifest["mode"])
    return result_for(manifest, metadata, bundle)


def create_tag(directory):
    result = verify(directory)
    tag = "v" + result["version"]
    message = canonical(
        {
            "format": 1,
            **{
                key: result[key]
                for key in (
                    "version",
                    "sha",
                    "mode",
                    "run_id",
                    "run_attempt",
                    "manifest_id",
                    "manifest_digest",
                    "bundle_id",
                )
            },
        }
    ).rstrip("\n")

    def existing():
        ref = api("git/ref/tags/" + tag, missing=True)
        if ref is None:
            return False
        require(
            ref.get("object", {}).get("type") == "tag" and sha(ref["object"].get("sha")),
            "existing release tag is not annotated",
        )
        value = api("git/tags/" + ref["object"]["sha"])
        require(
            value.get("tag") == tag
            and value.get("object", {}).get("type") == "commit"
            and value["object"].get("sha") == result["sha"],
            "existing release tag target differs",
        )
        require(
            value.get("message") in (message, message + "\n"),
            "existing release tag binds another candidate",
        )
        return True

    if not existing():
        created = api(
            "git/tags",
            method="POST",
            data={"tag": tag, "message": message, "object": result["sha"], "type": "commit"},
        )
        require(sha(created.get("sha")), "invalid created tag object")
        try:
            api("git/refs", method="POST", data={"ref": "refs/tags/" + tag, "sha": created["sha"]})
        except Error:
            if not existing():
                raise
        require(existing(), "created tag was not confirmed")
    return dict(result, tag=tag)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    resolve_parser = sub.add_parser("resolve")
    resolve_parser.add_argument("--version", required=True)
    resolve_parser.add_argument("--ref", default="main")
    final = sub.add_parser("finalize")
    for name in ("version", "sha", "mode", "directory", "artifact-id"):
        final.add_argument("--" + name, required=True, type=int if name == "artifact-id" else str)
    final.add_argument("--output", default="candidate-manifest/candidate.json")
    for command in ("fetch", "inspect"):
        current = sub.add_parser(command)
        current.add_argument("--run-id", type=int, required=True)
        current.add_argument("--manifest-id", type=int, required=True)
        if command == "fetch":
            current.add_argument("--directory", required=True)
    for command in ("verify", "create-tag"):
        sub.add_parser(command).add_argument("--directory", required=True)
    args = parser.parse_args(argv)
    values = vars(args)
    command = values.pop("command")
    if command == "finalize":
        values["sha_value"] = values.pop("sha")
    try:
        result = {
            "resolve": resolve,
            "finalize": finalize,
            "fetch": fetch,
            "inspect": inspect_candidate,
            "verify": verify,
            "create-tag": create_tag,
        }[command](**values)
        if os.environ.get("GITHUB_OUTPUT"):
            with open(os.environ["GITHUB_OUTPUT"], "a") as output:
                for key, value in result.items():
                    output.write(f"{key}={str(value).lower() if type(value) is bool else value}\n")
        print(canonical(result), end="")
        return 0
    except (ValueError, OSError, KeyError, TypeError, zipfile.BadZipFile) as error:
        print("release candidate: " + str(error), file=sys.stderr)
        return 1
