#!/usr/bin/env python3
"""Exercise source resolution, publication gates, complete assets and feed signing."""

import base64
import hashlib
import io
import json
import os
from pathlib import Path
import plistlib
import runpy
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock
import zipfile

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
from release_contract import (
    distribution_names,
    generate_cask,
    checksum,
    checksum_name,
    verify_oci,
    write_checksum,
)  # noqa: E402

plan = runpy.run_path(str(ROOT / "scripts/release-plan"))["plan"]
prepare = runpy.run_path(str(ROOT / "scripts/prepare-release"))["prepare"]
sign = runpy.run_path(str(ROOT / "scripts/update-release"))["sign"]
SHA = "a" * 40
OTHER = "b" * 40


class ReleaseIntent(unittest.TestCase):
    def resolve(self, endpoint, query):
        return OTHER if endpoint.startswith("pulls/") else SHA

    def test_every_tag_checks_but_only_stable_version_tags_publish(self):
        for tag, official, version in [
            ("nightly", "false", "0.0.0"),
            ("构建+preview", "false", "0.0.0"),
            ("v1.2.3-rc.1", "false", "0.0.0"),
            ("v01.2.3", "false", "0.0.0"),
            ("v1.2.3", "true", "1.2.3"),
        ]:
            with self.subTest(tag=tag):
                result = plan("push", "refs/tags/" + tag, "", "", False, SHA, self.resolve)
                self.assertEqual((result["official"], result["version"]), (official, version))

    def test_manual_semantic_tag_defaults_to_validation(self):
        self.assertEqual(
            plan("workflow_dispatch", "refs/tags/v1.2.3", "", "", False, SHA, self.resolve)[
                "official"
            ],
            "false",
        )

    def test_manual_release_requires_explicit_stable_tag(self):
        result = plan(
            "workflow_dispatch",
            "refs/heads/main",
            "refs/tags/v1.2.3",
            "",
            True,
            OTHER,
            self.resolve,
        )
        self.assertEqual(result, {"sha": SHA, "version": "1.2.3", "official": "true"})
        for ref in ["refs/heads/main", "v1.2.3", "refs/tags/v1.2.3-rc.1"]:
            with self.subTest(ref=ref), self.assertRaises(ValueError):
                plan("workflow_dispatch", ref, "", "", True, SHA, self.resolve)

    def test_pr_sha_is_pinned_and_never_publishes(self):
        result = plan("workflow_dispatch", "refs/tags/v1.2.3", "", "123", False, SHA, self.resolve)
        self.assertEqual(result["sha"], OTHER)
        self.assertEqual(result["official"], "false")
        with self.assertRaises(ValueError):
            plan("workflow_dispatch", "refs/tags/v1.2.3", "", "123", True, SHA, self.resolve)

    def test_invalid_or_ambiguous_sources_fail(self):
        for ref, pr in [
            ("main", "1"),
            ("", "0"),
            ("", "x"),
            ("a/../b", ""),
            ("$(touch unsafe)", ""),
        ]:
            with self.subTest(ref=ref, pr=pr), self.assertRaises(ValueError):
                plan("workflow_dispatch", "refs/heads/main", ref, pr, False, SHA, self.resolve)
        with self.assertRaises(ValueError):
            plan("push", "refs/tags/v1.2.3", "", "", False, SHA, lambda *_: OTHER)


class AssetsFixture:
    def oci_fixture(self, path, architectures=("amd64", "arm64")):
        with tarfile.open(path, "w") as archive:

            def add(name, value):
                data = json.dumps(value).encode()
                info = tarfile.TarInfo(name)
                info.size = len(data)
                archive.addfile(info, io.BytesIO(data))
                return {"digest": "sha256:" + hashlib.sha256(data).hexdigest()}

            def blob(value):
                data = json.dumps(value).encode()
                return add("blobs/sha256/" + hashlib.sha256(data).hexdigest(), value)

            manifests = []
            for architecture in architectures:
                config = blob(
                    {
                        "os": "linux",
                        "architecture": architecture,
                        "config": {
                            "Labels": {
                                "org.opencontainers.image.version": "1.2.3",
                                "org.opencontainers.image.revision": SHA,
                            }
                        },
                    }
                )
                manifests.append(blob({"config": config, "layers": []}))
            add("index.json", {"manifests": manifests})

    def fixture(self, root):
        for name in distribution_names("1.2.3"):
            path = root / name
            if name == "Pot-1.2.3-arm64.zip":
                with zipfile.ZipFile(path, "w") as archive:
                    archive.writestr(
                        "Pot.app/Contents/Info.plist",
                        plistlib.dumps({"CFBundleShortVersionString": "1.2.3"}),
                    )
            elif name.endswith(".oci.tar"):
                self.oci_fixture(path)
            else:
                path.write_bytes(("fixture:" + name).encode())
            write_checksum(path)


class CompleteDistribution(AssetsFixture, unittest.TestCase):
    def test_cask_and_global_checksums_bind_exact_versioned_assets(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.fixture(root)
            prepare("1.2.3", root)
            cask = (root / "pot.rb").read_text()
            self.assertIn('version "1.2.3"', cask)
            self.assertIn("/v#{version}/Pot-#{version}-arm64.zip", cask)
            self.assertNotIn("xattr", cask)
            self.assertNotIn("@SHA256@", cask)
            self.assertEqual(len((root / "SHA256SUMS").read_text().splitlines()), 7)

    def test_missing_container_cannot_be_a_complete_distribution(self):
        for missing in ["pot-container-1.2.3.oci.tar"]:
            with self.subTest(missing=missing), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                self.fixture(root)
                (root / missing).unlink()
                with self.assertRaisesRegex(ValueError, "missing or empty"):
                    prepare("1.2.3", root)
                self.assertFalse((root / "SHA256SUMS").exists())

    def test_channels_prepare_without_windows_or_macos_assets(self):
        for channel in ["server", "desktop", "container"]:
            with self.subTest(channel=channel), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                self.fixture(root)
                keep = set(distribution_names("1.2.3", channel))
                for path in root.iterdir():
                    if path.name.removesuffix(".sha256") not in keep:
                        path.unlink()
                prepare("1.2.3", root, channel=channel)
                self.assertTrue((root / checksum_name(channel)).exists())
                self.assertFalse((root / "pot.rb").exists())
                self.assertFalse((root / "update.json").exists())

    def test_each_channel_requires_its_own_artifacts_before_signing_or_publication(self):
        for channel in ["server", "desktop", "container", "macos"]:
            with self.subTest(channel=channel), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                self.fixture(root)
                (root / distribution_names("1.2.3", channel)[0]).unlink()
                with mock.patch(
                    "subprocess.run", side_effect=AssertionError("effect before asset validation")
                ):
                    with self.assertRaisesRegex(ValueError, "missing or empty"):
                        prepare("1.2.3", root, channel=channel)

    def test_missing_release_enable_or_signing_keys_fails_before_external_effects(self):
        for environment, error in [
            ({}, "Release disabled"),
            ({"POT_MACOS_RELEASE_ENABLED": "true"}, "Incomplete release configuration"),
        ]:
            with (
                self.subTest(environment=environment),
                mock.patch.dict(os.environ, environment, clear=True),
                mock.patch(
                    "subprocess.run",
                    side_effect=AssertionError("external effect before configuration gate"),
                ),
                mock.patch(
                    "subprocess.check_output",
                    side_effect=AssertionError("external effect before configuration gate"),
                ),
                self.assertRaisesRegex(SystemExit, error),
            ):
                runpy.run_path(str(ROOT / "scripts/release-preflight"))

    def test_container_must_have_both_real_architectures(self):
        with tempfile.TemporaryDirectory() as temporary:
            archive = Path(temporary) / "image.tar"
            self.oci_fixture(archive, ("amd64",))
            with self.assertRaisesRegex(ValueError, "amd64 and arm64"):
                verify_oci(archive, "1.2.3")

    def test_container_version_and_revision_must_match_the_validated_source(self):
        with tempfile.TemporaryDirectory() as temporary:
            archive = Path(temporary) / "image.tar"
            self.oci_fixture(archive)
            verify_oci(archive, "1.2.3", SHA)
            with self.assertRaisesRegex(ValueError, "version"):
                verify_oci(archive, "9.9.9", SHA)
            with self.assertRaisesRegex(ValueError, "revision"):
                verify_oci(archive, "1.2.3", OTHER)

    def test_tampered_assets_block_cask_and_release(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.fixture(root)
            (root / "Pot-1.2.3-arm64.zip").write_bytes(b"modified")
            with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                prepare("1.2.3", root)
            with self.assertRaises(ValueError):
                generate_cask("1.2.3", root)

    def test_cask_rejects_misnamed_bundle_version(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.fixture(root)
            archive = root / "Pot-1.2.3-arm64.zip"
            with zipfile.ZipFile(archive, "w") as packed:
                packed.writestr(
                    "Pot.app/Contents/Info.plist",
                    plistlib.dumps({"CFBundleShortVersionString": "9.9.9"}),
                )
            write_checksum(archive)
            with self.assertRaisesRegex(ValueError, "bundle version"):
                generate_cask("1.2.3", root)

    def test_cask_url_verification_checks_remote_bytes_and_https_redirects(self):
        for content, scheme, error in [
            (None, "https", None),
            (b"modified", "https", "SHA-256"),
            (None, "http", "HTTPS"),
        ]:
            with (
                self.subTest(scheme=scheme, error=error),
                tempfile.TemporaryDirectory() as temporary,
            ):
                root = Path(temporary)
                self.fixture(root)
                data = (root / "Pot-1.2.3-arm64.zip").read_bytes() if content is None else content

                class Response(io.BytesIO):
                    def geturl(self):
                        return scheme + "://release-assets.githubusercontent.com/fixture"

                with (
                    mock.patch.object(
                        sys, "argv", ["generate-cask", "1.2.3", str(root), "--verify-url"]
                    ),
                    mock.patch("urllib.request.urlopen", return_value=Response(data)) as request,
                    mock.patch("builtins.print"),
                ):
                    if error:
                        with self.assertRaisesRegex(ValueError, error):
                            runpy.run_path(str(ROOT / "scripts/generate-cask"))
                        self.assertFalse((root / "pot.rb").exists())
                    else:
                        runpy.run_path(str(ROOT / "scripts/generate-cask"))
                        self.assertTrue((root / "pot.rb").exists())
                    request.assert_called_once_with(
                        "https://github.com/jjyr/pot/releases/download/v1.2.3/Pot-1.2.3-arm64.zip",
                        timeout=60,
                    )

    def test_feed_binds_only_supported_update_installations(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.fixture(root)
            with mock.patch("subprocess.check_output", return_value=b"test-signature"):
                sign("1.2.3", root)
            envelope = json.loads((root / "update.json").read_text())
            payload = json.loads(base64.b64decode(envelope["payload"]))
            self.assertEqual(payload["version"], "1.2.3")
            self.assertEqual(len(payload["artifacts"]), 4)
            for artifact in payload["artifacts"]:
                self.assertTrue(
                    artifact["url"].startswith(
                        "https://github.com/jjyr/pot/releases/download/v1.2.3/"
                    )
                )
                self.assertIn("1.2.3", artifact["url"].split("/")[-1])
                self.assertEqual(len(artifact["sha256"]), 64)

    def test_real_signer_requires_matching_ed25519_key(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.fixture(root)
            key = root / "release.pem"
            public = subprocess.check_output(
                ["go", "run", "./scripts/sign-release", "keygen", str(key)], cwd=ROOT, text=True
            ).strip()
            with mock.patch.dict(
                os.environ, POT_UPDATE_PUBLIC_KEY=public, POT_UPDATE_PRIVATE_KEY=key.read_text()
            ):
                sign("1.2.3", root)
            envelope = json.loads((root / "update.json").read_text())
            self.assertEqual(len(base64.b64decode(envelope["signature"])), 64)
            (root / "update.json").unlink()
            with mock.patch.dict(
                os.environ,
                POT_UPDATE_PUBLIC_KEY=base64.b64encode(b"x" * 32).decode(),
                POT_UPDATE_PRIVATE_KEY=key.read_text(),
            ):
                with self.assertRaises(subprocess.CalledProcessError):
                    sign("1.2.3", root)
            self.assertFalse((root / "update.json").exists())


class GitHubPublication(AssetsFixture, unittest.TestCase):
    def test_independent_channels_add_assets_without_overwriting_other_checksums(self):
        self.exercise()

    def test_conflicting_existing_asset_blocks_before_upload(self):
        with self.assertRaisesRegex(ValueError, "Refusing to replace"):
            self.exercise(conflict=True)
        self.assertFalse(any(call[:3] == ["gh", "release", "upload"] for call in self.calls))

    def test_moved_tag_blocks_all_writes(self):
        with self.assertRaisesRegex(ValueError, "tag moved"):
            self.exercise(commit=OTHER)
        self.assertEqual(self.calls, [])

    def exercise(self, conflict=False, commit=SHA):
        publish = runpy.run_path(str(ROOT / "scripts/publish-release"))["publish"]
        self.calls = []
        sentinel = {"name": "SHA256SUMS-macos", "digest": "sha256:" + "f" * 64, "size": 7}
        release = {"target_commitish": "main", "draft": False, "assets": [sentinel.copy()]}
        real_run = subprocess.run
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.fixture(root)
            for channel in ["server", "desktop"]:
                prepare("1.2.3", root, channel=channel)
            if conflict:
                release["assets"].append(
                    {
                        "name": distribution_names("1.2.3", "server")[0],
                        "digest": "sha256:" + "f" * 64,
                        "size": 1,
                    }
                )

            def run(arguments, **options):
                self.calls.append(arguments)
                if arguments[0] == "sha256sum":
                    return real_run(arguments, stdout=subprocess.DEVNULL, **options)
                if arguments[:2] == ["gh", "api"]:
                    return subprocess.CompletedProcess(arguments, 0, json.dumps(release), "")
                self.assertEqual(arguments[:3], ["gh", "release", "upload"])
                path = Path(arguments[-1])
                release["assets"].append(
                    {
                        "name": path.name,
                        "digest": "sha256:" + checksum(path),
                        "size": path.stat().st_size,
                    }
                )
                return subprocess.CompletedProcess(arguments, 0, "", "")

            with (
                mock.patch.dict(os.environ, GITHUB_REPOSITORY="jjyr/pot", RELEASE_SHA=SHA),
                mock.patch("subprocess.run", side_effect=run),
                mock.patch("subprocess.check_output", return_value=commit),
                mock.patch("builtins.print"),
            ):
                publish("1.2.3", root, "server")
                publish("1.2.3", root, "desktop")
                count = sum(call[:3] == ["gh", "release", "upload"] for call in self.calls)
                publish("1.2.3", root, "server")
                self.assertEqual(
                    count, sum(call[:3] == ["gh", "release", "upload"] for call in self.calls)
                )
            self.assertIn(sentinel, release["assets"])
            names = [asset["name"] for asset in release["assets"]]
            self.assertEqual(len(names), len(set(names)))
            self.assertIn("SHA256SUMS-server", names)
            self.assertIn("SHA256SUMS-desktop", names)


@unittest.skipUnless(
    hasattr(os, "memfd_create"), "GHCR publisher uses Linux anonymous memory files"
)
class ContainerPublication(AssetsFixture, unittest.TestCase):
    def test_registry_uses_memory_auth_and_needs_no_macos_or_windows_signing(self):
        self.exercise()
        self.assertEqual(self.calls[-1][:2], ["skopeo", "copy"])
        self.assertEqual(self.calls[-1][-1], "docker://ghcr.io/jjyr/pot:1.2.3")

    def test_conflicting_image_is_not_overwritten(self):
        with self.assertRaisesRegex(ValueError, "existing image"):
            self.exercise(remote=b"different-manifest")
        self.assertFalse(any(call[:2] == ["skopeo", "copy"] for call in self.calls))

    def exercise(self, remote=b""):
        publish = runpy.run_path(str(ROOT / "scripts/publish-container"))["publish"]
        self.calls = []
        real_run = subprocess.run
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.oci_fixture(root / "pot-container-1.2.3.oci.tar")
            write_checksum(root / "pot-container-1.2.3.oci.tar")
            prepare("1.2.3", root, channel="container")

            def run(arguments, **options):
                self.calls.append(arguments)
                if arguments[0] == "sha256sum":
                    return real_run(arguments, stdout=subprocess.DEVNULL, **options)
                self.assertEqual(arguments[0], "skopeo")
                self.assertNotIn("synthetic-token", " ".join(arguments))
                fd = options["pass_fds"][0]
                auth = json.loads(os.pread(fd, 4096, 0))
                self.assertEqual(
                    base64.b64decode(auth["auths"]["ghcr.io"]["auth"]), b"tester:synthetic-token"
                )
                if arguments[1] == "inspect":
                    return subprocess.CompletedProcess(
                        arguments, 0 if remote else 1, remote, b"manifest unknown"
                    )
                return subprocess.CompletedProcess(arguments, 0)

            def output(arguments, **options):
                return SHA if arguments[0] == "gh" else b"manifest"

            with (
                mock.patch.dict(
                    os.environ,
                    GITHUB_REPOSITORY="jjyr/pot",
                    GITHUB_ACTOR="tester",
                    GH_TOKEN="synthetic-token",
                    POT_BUILD_REVISION=SHA,
                ),
                mock.patch("subprocess.run", side_effect=run),
                mock.patch("subprocess.check_output", side_effect=output),
                mock.patch("builtins.print"),
            ):
                publish("1.2.3", root)


if __name__ == "__main__":
    unittest.main()
