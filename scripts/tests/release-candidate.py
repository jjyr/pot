#!/usr/bin/env python3
"""Exercise candidate provenance, exact artifact bytes and immutable tag binding."""

import base64
import copy
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock
import zipfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import release_candidate as c  # noqa: E402


def digest(data):
    return hashlib.sha256(data).hexdigest()


def zipped(entries):
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as archive:
        for name, data in entries:
            archive.writestr(name, data)
    return output.getvalue()


class Candidate(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.version, self.source, self.workflow = "1.2.3", "a" * 40, "b" * 40
        self.main = self.workflow
        self.repo = {"id": 90, "full_name": c.REPOSITORY, "default_branch": "main"}
        self.run = {
            "id": 7,
            "run_attempt": 1,
            "head_sha": self.workflow,
            "head_branch": "main",
            "repository": self.repo,
            "head_repository": self.repo,
            "workflow_id": 8,
            "path": c.WORKFLOW,
            "event": "workflow_dispatch",
            "pull_requests": [],
            "status": "in_progress",
            "conclusion": None,
            "run_started_at": "2026-10-29T00:00:00Z",
        }
        self.attempt = copy.deepcopy(self.run)
        self.meta, self.blobs, self.mutations = {}, {}, []
        self.ref, self.annotation = None, None
        self.real_api = c.api
        self.downloads = []
        self.api_patch = mock.patch.object(c, "api", side_effect=self.api)
        self.api_patch.start()
        self.addCleanup(self.api_patch.stop)
        patch = mock.patch.object(c.subprocess, "run", side_effect=self.transport)
        patch.start()
        self.addCleanup(patch.stop)
        patch = mock.patch.dict(
            os.environ,
            {
                "GITHUB_REPOSITORY": c.REPOSITORY,
                "GITHUB_EVENT_NAME": "workflow_dispatch",
                "GITHUB_REF": "refs/heads/main",
                "GITHUB_WORKFLOW_REF": f"{c.REPOSITORY}/{c.WORKFLOW}@refs/heads/main",
                "GITHUB_SHA": self.workflow,
                "GITHUB_RUN_ID": "7",
                "GITHUB_RUN_ATTEMPT": "1",
            },
        )
        patch.start()
        self.addCleanup(patch.stop)
        self.directory = self.root / "source"
        self.directory.mkdir()
        for name in c.expected_names(self.version):
            (self.directory / name).write_bytes(name.encode())
        payload = {"version": self.version, "protocol": 2, "artifacts": []}
        for kind, platform, name in c.update_artifacts(self.version):
            path = self.directory / name
            payload["artifacts"].append(
                {
                    "kind": kind,
                    "platform": platform,
                    "url": f"https://github.com/{c.REPOSITORY}/releases/download/v{self.version}/{name}",
                    "sha256": c.checksum(path),
                    "size": path.stat().st_size,
                }
            )
        (self.directory / "update.json").write_text(
            c.canonical(
                {
                    "payload": base64.b64encode(c.canonical(payload).encode()).decode(),
                    "signature": base64.b64encode(bytes(64)).decode(),
                }
            )
        )
        (self.directory / "MACOS-RELEASE.txt").write_text(
            c.macos_release_notes(self.version, self.source, "unsigned")
        )
        self.checksums()
        self.bundle_entries = [(path.name, path.read_bytes()) for path in self.directory.iterdir()]
        self.artifact(11, "bundle", zipped(self.bundle_entries))
        manifest_path = self.root / "candidate.json"
        c.finalize(self.version, self.source, "unsigned", self.directory, 11, manifest_path)
        self.manifest = json.loads(manifest_path.read_bytes())
        self.pack_manifest()
        for run in (self.run, self.attempt):
            run.update(status="completed", conclusion="success")

    def checksums(self):
        for channel in ("server", "desktop", "macos", "container"):
            names = c.distribution_names(self.version, channel)
            for name in names:
                (self.directory / (name + ".sha256")).write_text(
                    f"{c.checksum(self.directory / name)}  {name}\n"
                )
            if channel == "macos":
                names += ["update.json", "MACOS-RELEASE.txt"]
            (self.directory / c.checksum_name(channel)).write_text(
                "".join(f"{c.checksum(self.directory / name)}  {name}\n" for name in names)
            )

    def artifact(self, identity, kind, data):
        self.blobs[identity] = data
        self.meta[identity] = {
            "id": identity,
            "name": f"release-{kind}-7-1",
            "workflow_run": {
                "id": 7,
                "head_sha": self.workflow,
                "repository_id": 90,
                "head_repository_id": 90,
            },
            "expired": False,
            "expires_at": "2099-01-01T00:00:00Z",
            "created_at": "2026-10-29T00:00:01Z",
            "digest": "sha256:" + digest(data),
            "size_in_bytes": len(data),
        }

    def pack_manifest(self):
        self.artifact(
            12, "candidate", zipped([("candidate.json", c.canonical(self.manifest).encode())])
        )

    def replace_bundle(self, entries):
        self.artifact(11, "bundle", zipped(entries))
        self.manifest["bundle"] = {key: self.meta[11][key] for key in c.BUNDLE_KEYS}
        self.pack_manifest()

    def transport(self, command, **kwargs):
        path = next(arg for arg in command if arg.endswith("/zip"))
        identity = int(path.split("/")[-2])
        self.downloads.append(identity)
        kwargs["stdout"].write(self.blobs[identity])
        return subprocess.CompletedProcess(command, 0, stderr=b"")

    def api(self, endpoint, method="GET", data=None, missing=False):
        if method == "POST":
            self.mutations.append((endpoint, data))
            if endpoint == "git/tags":
                self.annotation = {
                    "tag": data["tag"],
                    "message": data["message"] + "\n",
                    "object": {"type": data["type"], "sha": data["object"]},
                }
                return {"sha": "c" * 40}
            if endpoint == "git/refs":
                self.ref = {"object": {"type": "tag", "sha": data["sha"]}}
                return self.ref
            raise AssertionError(endpoint)
        fixed = {
            "": self.repo,
            "commits/heads/main": {"sha": self.main},
            "actions/workflows/release-prepare.yml": {
                "id": 8,
                "path": c.WORKFLOW,
                "state": "active",
            },
            "actions/runs/7": self.run,
            "actions/runs/7/attempts/1": self.attempt,
            "git/ref/tags/v1.2.3": self.ref,
            "git/tags/" + "c" * 40: self.annotation,
        }
        if endpoint in fixed:
            return copy.deepcopy(fixed[endpoint])
        if endpoint.startswith("actions/artifacts/"):
            return copy.deepcopy(self.meta[int(endpoint.rsplit("/", 1)[1])])
        if endpoint.startswith("compare/"):
            base, head = endpoint.removeprefix("compare/").split("...")
            if base == head:
                return {"status": "identical"}
            if (base, head) in {
                (self.source, self.workflow),
                (self.source, self.main),
                (self.workflow, self.main),
            }:
                return {"status": "ahead"}
            return {"status": "diverged"}
        raise AssertionError(endpoint)

    def fetch(self, name="destination"):
        path = self.root / name
        result = c.fetch(7, 12, path)
        return path, result

    def rejected_fetch(self):
        with self.assertRaises(ValueError):
            self.fetch()
        self.assertFalse((self.root / "destination").exists())
        self.assertEqual(self.mutations, [])

    def test_roundtrip_identical_tag_retry_and_dispatch_snapshot(self):
        self.main = "d" * 40
        self.assertEqual(c.resolve(self.version)["sha"], self.workflow)
        path, result = self.fetch()
        self.assertEqual(result["manifest_digest"], digest(c.canonical(self.manifest).encode()))
        c.create_tag(path)
        c.create_tag(path)
        self.assertEqual(len(self.mutations), 2)
        self.assertEqual(
            json.loads(self.annotation["message"])["manifest_digest"], result["manifest_digest"]
        )
        with self.assertRaises(c.Error):
            c.resolve(self.version)

    def test_local_changes_never_create_tag(self):
        for change in ("missing", "extra", "changed", "symlink"):
            with self.subTest(change=change):
                path, _ = self.fetch(change)
                selected = path / "MACOS-RELEASE.txt"
                if change == "missing":
                    selected.unlink()
                elif change == "extra":
                    (path / "extra").write_text("x")
                elif change == "symlink":
                    selected.unlink()
                    selected.symlink_to(self.directory / selected.name)
                else:
                    selected.write_text("changed")
                with self.assertRaises(c.Error):
                    c.create_tag(path)
                self.assertEqual(self.mutations, [])

    def test_provenance_and_attempt_failures(self):
        for key, value in (
            ("event", "push"),
            ("head_branch", "feature"),
            ("path", "evil.yml"),
            ("workflow_id", 123),
            ("head_repository", {"full_name": "fork/pot"}),
            ("run_attempt", 2),
            ("conclusion", "failure"),
        ):
            with self.subTest(key=key):
                original = copy.deepcopy(self.attempt)
                self.attempt[key] = value
                self.rejected_fetch()
                self.attempt = original

    def test_old_successful_attempt_stays_pinned_after_failed_rerun(self):
        self.run.update(run_attempt=2, status="completed", conclusion="failure")
        path, result = self.fetch()
        self.assertEqual(result["run_attempt"], 1)
        c.create_tag(path)

    def test_expiry_and_inspect_uses_only_small_manifest(self):
        self.meta[11]["expired"] = True
        self.rejected_fetch()
        self.downloads.clear()
        value = c.inspect_candidate(7, 12, require_fresh=False)
        self.assertTrue(value["expired"])
        self.assertEqual(self.downloads, [12])
        self.meta[11]["expired"] = False
        self.meta[12]["expires_at"] = "2000-01-01T00:00:00Z"
        self.rejected_fetch()

    def test_artifact_digest_run_and_creation_are_enforced(self):
        for identity, key, value in (
            (11, "digest", "sha256:" + "0" * 64),
            (12, "name", "release-candidate-8-1"),
            (12, "workflow_run", {"id": 8, "head_sha": self.workflow}),
            (11, "created_at", "2020-01-01T00:00:00Z"),
            (11, "expires_at", "2099-01-01T00:00:00"),
        ):
            with self.subTest(identity=identity, key=key):
                original = copy.deepcopy(self.meta[identity])
                self.meta[identity][key] = value
                self.rejected_fetch()
                self.meta[identity] = original
        self.blobs[11] += b"altered"
        self.rejected_fetch()

    def test_zip_rejects_extra_missing_traversal_duplicate_and_symlink(self):
        for entries in (
            self.bundle_entries[1:],
            self.bundle_entries + [("extra", b"x")],
            self.bundle_entries[:-1] + [("../escape", b"x")],
            self.bundle_entries + [self.bundle_entries[0]],
        ):
            self.replace_bundle(entries)
            self.rejected_fetch()
        output = io.BytesIO()
        with zipfile.ZipFile(output, "w") as archive:
            for index, (name, body) in enumerate(self.bundle_entries):
                info = zipfile.ZipInfo(name)
                info.external_attr = (0o120777 if index == 0 else 0o100644) << 16
                archive.writestr(info, body)
        self.artifact(11, "bundle", output.getvalue())
        self.manifest["bundle"] = {key: self.meta[11][key] for key in c.BUNDLE_KEYS}
        self.pack_manifest()
        self.rejected_fetch()

    def test_wrong_manifest_file_bytes_and_source_fail(self):
        original_hash = self.manifest["files"][0]["sha256"]
        self.manifest["files"][0]["sha256"] = "0" * 64
        self.pack_manifest()
        self.rejected_fetch()
        self.manifest["files"][0]["sha256"] = original_hash
        self.manifest["sha"] = "e" * 40
        self.pack_manifest()
        self.rejected_fetch()

    def test_tag_conflicts_do_not_mutate(self):
        path, _ = self.fetch()
        c.create_tag(path)
        self.mutations.clear()
        for kind in ("lightweight", "moved", "candidate"):
            original_ref, original_tag = copy.deepcopy(self.ref), copy.deepcopy(self.annotation)
            if kind == "lightweight":
                self.ref["object"]["type"] = "commit"
            elif kind == "moved":
                self.annotation["object"]["sha"] = "d" * 40
            else:
                self.annotation["message"] += "different candidate"
            with self.assertRaises(c.Error):
                c.create_tag(path)
            self.assertEqual(self.mutations, [])
            self.ref, self.annotation = original_ref, original_tag

    def test_semantic_feed_notes_and_checksums_fail_before_tag(self):
        for name, body in (
            ("update.json", b"{}"),
            ("MACOS-RELEASE.txt", b"other mode"),
            ("SHA256SUMS-server", b"wrong"),
            ("pot-1.2.3-linux-amd64.tar.gz.sha256", b"wrong"),
        ):
            original_manifest = copy.deepcopy(self.manifest)
            entries = [(n, body if n == name else value) for n, value in self.bundle_entries]
            item = next(item for item in self.manifest["files"] if item["name"] == name)
            item.update(sha256=digest(body), size=len(body))
            self.replace_bundle(entries)
            self.rejected_fetch()
            self.manifest = original_manifest

    def test_well_formed_feed_with_wrong_version_url_or_hash_is_rejected(self):
        original = json.loads((self.directory / "update.json").read_bytes())
        for change in ("version", "url", "hash"):
            manifest = copy.deepcopy(self.manifest)
            envelope = copy.deepcopy(original)
            payload = json.loads(base64.b64decode(envelope["payload"]))
            if change == "version":
                payload["version"] = "1.2.2"
            elif change == "url":
                payload["artifacts"][0]["url"] = "https://example.com/wrong.zip"
            else:
                payload["artifacts"][0]["sha256"] = "0" * 64
            envelope["payload"] = base64.b64encode(c.canonical(payload).encode()).decode()
            body = c.canonical(envelope).encode()
            item = next(item for item in self.manifest["files"] if item["name"] == "update.json")
            item.update(size=len(body), sha256=digest(body))
            self.replace_bundle(
                [
                    (name, body if name == "update.json" else value)
                    for name, value in self.bundle_entries
                ]
            )
            self.rejected_fetch()
            self.manifest = manifest

    def test_manifest_canonical_fields_and_attempt_are_not_optional(self):
        for key, value in (
            ("format", True),
            ("run_attempt", 2),
            ("workflow_sha", "f" * 40),
            ("mode", "release"),
            ("version", "v1.2.3"),
        ):
            original = copy.deepcopy(self.manifest)
            self.manifest[key] = value
            self.pack_manifest()
            self.rejected_fetch()
            self.manifest = original
        raw = c.canonical(self.manifest).replace('"format":1', '"format":1,"format":1').encode()
        self.artifact(12, "candidate", zipped([("candidate.json", raw)]))
        self.rejected_fetch()

    def test_api_failure_before_tag_does_not_mutate(self):
        path, _ = self.fetch()
        with mock.patch.object(c, "api", side_effect=c.Error("API unavailable")):
            with self.assertRaises(c.Error):
                c.create_tag(path)
        self.assertEqual(self.mutations, [])

    def test_api_404_is_structured_and_errors_are_redacted(self):
        for status in (404, "404"):
            with mock.patch.object(
                c.subprocess,
                "run",
                return_value=subprocess.CompletedProcess(
                    [], 1, stdout=json.dumps({"status": status}), stderr="secret-token"
                ),
            ):
                self.assertIsNone(self.real_api("git/ref/tags/x", missing=True))
        with mock.patch.object(
            c.subprocess,
            "run",
            return_value=subprocess.CompletedProcess(
                [], 1, stdout="", stderr="secret-token HTTP 404"
            ),
        ):
            with self.assertRaises(c.Error) as failure:
                self.real_api("git/ref/tags/x", missing=True)
            self.assertNotIn("secret-token", str(failure.exception))


if __name__ == "__main__":
    unittest.main()
