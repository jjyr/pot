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
    macos_release_notes,
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
            self.assertEqual(len((root / "SHA256SUMS").read_text().splitlines()), 6)

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
                        with mock.patch.dict(os.environ, POT_BUILD_REVISION=SHA):
                            prepare(
                                "1.2.3",
                                root,
                                channel=channel,
                                sign_feed=channel == "macos",
                                macos_mode="unsigned" if channel == "macos" else None,
                            )

    def test_missing_mode_or_credentials_fails_before_external_effects(self):
        for environment, error, exception in [
            ({}, "explicitly select", ValueError),
            ({"POT_MACOS_RELEASE_MODE": "auto"}, "explicitly select", ValueError),
            ({"POT_MACOS_RELEASE_MODE": "unsigned"}, "POT_UPDATE_PRIVATE_KEY", SystemExit),
            (
                {
                    "POT_MACOS_RELEASE_MODE": "signed",
                    "POT_UPDATE_PUBLIC_KEY": "fixture",
                    "POT_UPDATE_PRIVATE_KEY": "fixture",
                },
                "MACOS_CERTIFICATE_P12",
                SystemExit,
            ),
        ]:
            with (
                self.subTest(environment=environment),
                mock.patch.dict(os.environ, environment, clear=True),
                mock.patch("subprocess.run", side_effect=AssertionError("effect before gate")),
                mock.patch(
                    "subprocess.check_output", side_effect=AssertionError("effect before gate")
                ),
                self.assertRaisesRegex(exception, error),
            ):
                runpy.run_path(str(ROOT / "scripts/release-preflight"))

    def test_signed_requires_each_apple_setting_without_fallback(self):
        names = [
            "MACOS_CERTIFICATE_P12",
            "MACOS_CERTIFICATE_PASSWORD",
            "MACOS_SIGNING_IDENTITY",
            "APPLE_ID",
            "APPLE_TEAM_ID",
            "APPLE_APP_PASSWORD",
        ]
        configured = {
            name: "fixture" for name in names + ["POT_UPDATE_PUBLIC_KEY", "POT_UPDATE_PRIVATE_KEY"]
        }
        configured["POT_MACOS_RELEASE_MODE"] = "signed"
        for name in names:
            environment = {key: value for key, value in configured.items() if key != name}
            with (
                self.subTest(missing=name),
                mock.patch.dict(os.environ, environment, clear=True),
                mock.patch(
                    "subprocess.check_output", side_effect=AssertionError("effect before gate")
                ),
                self.assertRaisesRegex(SystemExit, name),
            ):
                runpy.run_path(str(ROOT / "scripts/release-preflight"))

    def test_unsigned_preflight_needs_real_update_key_pair_but_no_apple_account(self):
        with tempfile.TemporaryDirectory() as temporary:
            key = Path(temporary) / "fixture.pem"
            public = subprocess.check_output(
                ["go", "run", "./scripts/sign-release", "keygen", str(key)],
                cwd=ROOT,
                text=True,
            ).strip()
            environment = {
                "PATH": os.environ["PATH"],
                "HOME": os.environ["HOME"],
                "POT_MACOS_RELEASE_MODE": "unsigned",
                "POT_UPDATE_PUBLIC_KEY": public,
                "POT_UPDATE_PRIVATE_KEY": key.read_text(),
                "RELEASE_VERSION": "1.2.3",
                "RELEASE_SHA": SHA,
                "GITHUB_REPOSITORY": "jjyr/pot",
            }
            with (
                mock.patch.dict(os.environ, environment, clear=True),
                mock.patch("subprocess.check_output", return_value=SHA),
                mock.patch("builtins.print"),
            ):
                runpy.run_path(str(ROOT / "scripts/release-preflight"))
                with (
                    mock.patch("subprocess.check_output", return_value=OTHER),
                    self.assertRaisesRegex(SystemExit, "tag moved"),
                ):
                    runpy.run_path(str(ROOT / "scripts/release-preflight"))
                with (
                    mock.patch.dict(
                        os.environ, POT_UPDATE_PUBLIC_KEY=base64.b64encode(b"x" * 32).decode()
                    ),
                    self.assertRaises(subprocess.CalledProcessError),
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
    def test_new_draft_is_discovered_and_published_after_complete_upload(self):
        self.exercise_draft([])

    def test_existing_draft_is_reused_without_creation(self):
        self.exercise_draft([1])

    def test_ambiguous_drafts_stop_before_writes(self):
        self.exercise_draft([1, 2], rejected="Multiple releases")

    def test_reviewed_release_id_binds_uploads_and_publication(self):
        self.exercise_draft([1, 2], selected=1)

    def test_reviewed_draft_must_target_validated_source(self):
        self.exercise_draft([1], selected=1, target=OTHER, rejected="validated source")

    def test_incomplete_draft_remains_unpublished(self):
        self.exercise_draft([1], selected=1, omit=True, rejected="incomplete")

    def exercise_draft(self, ids, selected=None, target=SHA, omit=False, rejected=None):
        publish = runpy.run_path(str(ROOT / "scripts/publish-release"))["publish"]
        releases = [
            {
                "id": value,
                "tag_name": "v1.2.3",
                "target_commitish": target,
                "draft": True,
                "assets": [],
            }
            for value in ids
        ]
        writes = []
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.fixture(root)
            prepare("1.2.3", root, channel="server")

            def run(arguments, **options):
                if arguments[:2] == ["gh", "api"]:
                    if "--method" not in arguments:
                        endpoint = arguments[2]
                        if "/releases/tags/" in endpoint:
                            published = next((r for r in releases if not r["draft"]), None)
                            return subprocess.CompletedProcess(
                                arguments,
                                0 if published else 1,
                                json.dumps(published or {"status": "404"}),
                                "",
                            )
                        if endpoint.endswith("releases?per_page=100"):
                            return subprocess.CompletedProcess(
                                arguments, 0, json.dumps([releases]), ""
                            )
                        release = next(
                            r for r in releases if r["id"] == int(endpoint.rsplit("/", 1)[1])
                        )
                        return subprocess.CompletedProcess(arguments, 0, json.dumps(release), "")
                    method = arguments[arguments.index("--method") + 1]
                    endpoint = next(a for a in arguments if "/releases/" in a)
                    value = int(endpoint.split("/releases/", 1)[1].split("/", 1)[0])
                    release = next(r for r in releases if r["id"] == value)
                    self.assertEqual(value, selected)
                    if method == "PATCH":
                        self.assertEqual(len(release["assets"]), 5)
                        release["draft"] = False
                        writes.append(("publish", value))
                        return subprocess.CompletedProcess(arguments, 0, "{}", "")
                    self.assertEqual(method, "POST")
                    path = Path(arguments[-1])
                elif arguments[:3] == ["gh", "release", "create"]:
                    self.assertFalse(releases)
                    releases.append(
                        {
                            "id": 1,
                            "tag_name": "v1.2.3",
                            "target_commitish": SHA,
                            "draft": True,
                            "assets": [],
                        }
                    )
                    writes.append(("create", 1))
                    return subprocess.CompletedProcess(arguments, 0, "", "")
                elif arguments[:3] == ["gh", "release", "edit"]:
                    self.assertEqual(len(releases), 1)
                    self.assertEqual(len(releases[0]["assets"]), 5)
                    releases[0]["draft"] = False
                    writes.append(("publish", releases[0]["id"]))
                    return subprocess.CompletedProcess(arguments, 0, "", "")
                else:
                    self.assertEqual(arguments[:3], ["gh", "release", "upload"])
                    self.assertEqual(len(releases), 1)
                    release = releases[0]
                    path = Path(arguments[-1])
                writes.append(("upload", release["id"]))
                if not omit:
                    release["assets"].append(
                        {
                            "name": path.name,
                            "digest": "sha256:" + checksum(path),
                            "size": path.stat().st_size,
                        }
                    )
                return subprocess.CompletedProcess(arguments, 0, "{}", "")

            with (
                mock.patch.dict(os.environ, GITHUB_REPOSITORY="jjyr/pot", RELEASE_SHA=SHA),
                mock.patch("subprocess.run", side_effect=run),
                mock.patch("subprocess.check_output", return_value=SHA),
                mock.patch("builtins.print"),
            ):
                if rejected:
                    with self.assertRaisesRegex((RuntimeError, ValueError), rejected):
                        publish(
                            "1.2.3",
                            root,
                            "server",
                            **({"release_id": selected} if selected is not None else {}),
                        )
                    self.assertFalse(any(w[0] in {"publish", "create"} for w in writes))
                else:
                    publish(
                        "1.2.3",
                        root,
                        "server",
                        **({"release_id": selected} if selected is not None else {}),
                    )
                    self.assertEqual([w for w in writes if w[0] == "publish"], [("publish", 1)])
                    count = len(writes)
                    publish(
                        "1.2.3",
                        root,
                        "server",
                        **({"release_id": selected} if selected is not None else {}),
                    )
                    self.assertEqual(len(writes), count)
                    if len(releases) > 1:
                        self.assertEqual(releases[1]["assets"], [])
                        self.assertTrue(releases[1]["draft"])

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


class MacOSPublication(AssetsFixture, unittest.TestCase):
    def test_both_modes_sign_feed_bind_metadata_and_resume_partial_upload(self):
        for mode in ["unsigned", "signed"]:
            with self.subTest(mode=mode):
                self.exercise(mode)

    def exercise(self, mode):
        publish = runpy.run_path(str(ROOT / "scripts/publish-release"))["publish"]
        real_output = subprocess.check_output
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            self.fixture(root)
            key = root / "fixture.pem"
            public = real_output(
                ["go", "run", "./scripts/sign-release", "keygen", str(key)],
                cwd=ROOT,
                text=True,
            ).strip()
            environment = dict(
                POT_BUILD_REVISION=SHA,
                POT_UPDATE_PUBLIC_KEY=public,
                POT_UPDATE_PRIVATE_KEY=key.read_text(),
                GITHUB_REPOSITORY="jjyr/pot",
                RELEASE_SHA=SHA,
            )
            with mock.patch.dict(os.environ, environment):
                prepare("1.2.3", root, sign_feed=True, channel="macos", macos_mode=mode)
            notes = (root / "MACOS-RELEASE.txt").read_text()
            self.assertEqual(notes, macos_release_notes("1.2.3", SHA, mode))
            envelope = json.loads((root / "update.json").read_text())
            self.assertEqual(len(base64.b64decode(envelope["signature"])), 64)
            payload = json.loads(base64.b64decode(envelope["payload"]))
            self.assertEqual(len(payload["artifacts"]), 4)
            for artifact in payload["artifacts"]:
                path = root / artifact["url"].split("/")[-1]
                self.assertEqual(artifact["sha256"], checksum(path))
                self.assertEqual(artifact["size"], path.stat().st_size)
            release = {"draft": False, "assets": []}
            uploads = []
            fail = True

            def run(arguments, **options):
                nonlocal fail
                if arguments[:2] == ["gh", "api"]:
                    return subprocess.CompletedProcess(arguments, 0, json.dumps(release), "")
                self.assertEqual(arguments[:3], ["gh", "release", "upload"])
                path = Path(arguments[-1])
                if fail and len(uploads) == 2:
                    fail = False
                    return subprocess.CompletedProcess(arguments, 1, "", "fixture network failure")
                self.assertNotIn(path.name, uploads)
                uploads.append(path.name)
                release["assets"].append(
                    {
                        "name": path.name,
                        "size": path.stat().st_size,
                        "digest": "sha256:" + checksum(path),
                    }
                )
                return subprocess.CompletedProcess(arguments, 0, "", "")

            with (
                mock.patch.dict(os.environ, environment),
                mock.patch("subprocess.check_output", return_value=SHA),
                mock.patch("subprocess.run", side_effect=run),
                mock.patch("builtins.print"),
            ):
                with self.assertRaisesRegex(RuntimeError, "fixture network failure"):
                    publish("1.2.3", root, "macos", mode)
                publish("1.2.3", root, "macos", mode)
                count = len(uploads)
                publish("1.2.3", root, "macos", mode)
                self.assertEqual(len(uploads), count)
                # A later Apple enrollment cannot replace the same version's bytes.
                other_mode = "signed" if mode == "unsigned" else "unsigned"
                (root / "MACOS-RELEASE.txt").write_text(
                    macos_release_notes("1.2.3", SHA, other_mode)
                )
                with self.assertRaisesRegex(ValueError, "metadata"):
                    publish("1.2.3", root, "macos", mode)
                (root / "MACOS-RELEASE.txt").write_text(notes)
                manifest = root / "SHA256SUMS-macos"
                original = manifest.read_text()
                manifest.write_text(original.splitlines()[0] + "\n")
                with self.assertRaisesRegex(ValueError, "complete asset set"):
                    publish("1.2.3", root, "macos", mode)
                manifest.write_text(original)
                (root / "pot.rb").unlink()
                publish("1.2.3", root, "macos", mode)
                self.assertNotIn("pot.rb", uploads)
                self.assertNotIn("pot.rb", original)
                # Even a newly valid manifest cannot override existing assets.
                with mock.patch("subprocess.run"):
                    prepare("1.2.3", root, sign_feed=True, channel="macos", macos_mode=other_mode)
                with self.assertRaisesRegex(ValueError, "Refusing to replace"):
                    publish("1.2.3", root, "macos", other_mode)
                self.assertEqual(len(uploads), count)


class MacOSSealing(unittest.TestCase):
    def test_unsigned_entrypoint_does_not_read_apple_credentials(self):
        main = runpy.run_path(str(ROOT / "scripts/sign-macos-release"))["main"]
        with (
            mock.patch.dict(os.environ, {}, clear=True),
            mock.patch.object(
                sys, "argv", ["sign-macos-release", "1.2.3", ".", "--mode", "unsigned"]
            ),
            mock.patch.dict(main.__globals__, seal_archives=mock.Mock()) as namespace,
        ):
            main()
            self.assertEqual(namespace["seal_archives"].call_args.args[3], "-")

    def test_extracted_signature_check_rejects_wrong_identity_and_damage(self):
        from macos_release import verify_signatures

        with tempfile.TemporaryDirectory() as temporary:
            tree = Path(temporary)
            (tree / "pot").write_bytes(bytes.fromhex("cffaedfe") + b"fixture")
            for mode, details, accepted in [
                ("unsigned", "Signature=adhoc\n", True),
                ("unsigned", "Authority=Developer ID Application: fixture\n", False),
                ("signed", "Signature=adhoc\n", False),
                ("signed", "Authority=Developer ID Application: fixture\n", True),
            ]:
                with (
                    self.subTest(mode=mode, details=details),
                    mock.patch(
                        "subprocess.run",
                        return_value=subprocess.CompletedProcess([], 0, "", details),
                    ),
                ):
                    if accepted:
                        verify_signatures(tree, mode)
                    else:
                        with self.assertRaisesRegex(ValueError, "expected"):
                            verify_signatures(tree, mode)
            with (
                mock.patch(
                    "subprocess.run", side_effect=subprocess.CalledProcessError(1, "codesign")
                ),
                self.assertRaises(subprocess.CalledProcessError),
            ):
                verify_signatures(tree, "unsigned")
            (tree / "pot").unlink()
            with self.assertRaisesRegex(ValueError, "no Mach-O"):
                verify_signatures(tree, "unsigned")

    def test_unsigned_only_uses_adhoc_and_signed_notary_failure_is_fatal(self):
        sealing = runpy.run_path(str(ROOT / "scripts/sign-macos-release"))
        seal = sealing["seal_archives"]
        for mode in ["unsigned", "signed"]:
            with self.subTest(mode=mode), tempfile.TemporaryDirectory() as temporary:
                root = Path(temporary)
                output = root / "dist"
                output.mkdir()
                stage = root / "stage"
                stage.mkdir()
                app = root / "Pot.app"
                (app / "Contents/MacOS").mkdir(parents=True)
                (app / "Contents/MacOS/pot-app").write_bytes(bytes.fromhex("cffaedfe") + b"fixture")
                with zipfile.ZipFile(output / "Pot-1.2.3-arm64.zip", "w") as packed:
                    packed.write(app / "Contents/MacOS/pot-app", "Pot.app/Contents/MacOS/pot-app")
                cli = root / "pot-go-darwin-arm64"
                cli.mkdir()
                (cli / "pot").write_bytes(bytes.fromhex("cffaedfe") + b"fixture")
                with tarfile.open(output / "pot-go-1.2.3-darwin-arm64.tar.gz", "w:gz") as packed:
                    packed.add(cli, arcname=cli.name)
                for path in output.iterdir():
                    write_checksum(path)
                commands = []

                def call(*args):
                    commands.append(args)
                    if args[:4] == ("ditto", "-x", "-k", str(output / "Pot-1.2.3-arm64.zip")):
                        with zipfile.ZipFile(args[3]) as packed:
                            packed.extractall(args[4])
                    elif args[:3] == ("ditto", "-c", "-k"):
                        with zipfile.ZipFile(args[-1], "w") as packed:
                            tree = Path(args[-2])
                            for path in tree.rglob("*"):
                                if path.is_file():
                                    packed.write(path, str(path.relative_to(tree.parent)))

                with (
                    mock.patch.dict(seal.__globals__, call=call),
                    mock.patch.dict(
                        seal.__globals__,
                        notarize=mock.Mock(side_effect=RuntimeError("notary refused")),
                    ),
                ):
                    if mode == "signed":
                        with self.assertRaisesRegex(RuntimeError, "notary refused"):
                            seal(
                                "1.2.3",
                                output,
                                stage,
                                "Developer ID Application: fixture",
                                root / "keychain",
                            )
                    else:
                        seal("1.2.3", output, stage, "-")
                        self.assertTrue(
                            all(cmd[0] not in {"xcrun", "security", "spctl"} for cmd in commands)
                        )
                        for cmd in commands:
                            if cmd[0] == "codesign" and "--sign" in cmd:
                                self.assertEqual(cmd[cmd.index("--sign") + 1], "-")
                        for path in output.iterdir():
                            if not path.name.endswith(".sha256"):
                                self.assertEqual(
                                    path.with_name(path.name + ".sha256").read_text(),
                                    f"{checksum(path)}  {path.name}\n",
                                )


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
