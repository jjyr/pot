#!/usr/bin/env python3
"""Verify version/signature/download behavior using opaque bytes and real Ed25519."""

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
import urllib.error

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import public_release_verification as verify  # noqa: E402

VERSION = "1.2.3"
PUBLIC = base64.b64encode(
    bytes.fromhex("d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a")
).decode()
# Public RFC 8032 test key, never a product signing secret.
PRIVATE = (
    "-----BEGIN PRIVATE KEY-----\n"
    + base64.b64encode(
        bytes.fromhex(
            "302e020100300506032b6570042204209d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60"
        )
    ).decode()
    + "\n-----END PRIVATE KEY-----\n"
)
RELEASE_URL = f"https://api.github.com/repos/jjyr/pot/releases/tags/v{VERSION}"
PREFIX = f"https://github.com/jjyr/pot/releases/download/v{VERSION}/"
FEED_URL = PREFIX + "update.json"


class FakeNetwork(verify.Network):
    def __init__(self, fixture):
        super().__init__()
        self.fixture, self.requests = fixture, []

    def open(self, url):
        self.requests.append(url)
        if url not in self.fixture:
            raise verify.Incomplete("HTTP 404: fixture resource unavailable")
        return io.BytesIO(self.fixture[url])


class PublicRelease(unittest.TestCase):
    def setUp(self):
        self.output = tempfile.TemporaryDirectory()
        self.addCleanup(self.output.cleanup)
        # Intentionally not real archives or executable formats. No build/source,
        # schema, resource layout or native toolchain is needed for this check.
        self.packages = {
            PREFIX + "Pot-1.2.3-arm64.zip": b"opaque signed application package\n",
            PREFIX + "pot-1.2.3-linux-amd64.tar.gz": b"\x00opaque signed CLI package\n",
        }
        self.release = {"id": 123, "tag_name": "v" + VERSION, "draft": False, "prerelease": False}
        self.feed = {
            "version": VERSION,
            "artifacts": [
                {"url": url, "size": len(data), "sha256": hashlib.sha256(data).hexdigest()}
                for url, data in self.packages.items()
            ],
        }
        self.fixture = dict(self.packages)
        self.fixture[RELEASE_URL] = json.dumps(self.release).encode()
        self.sign_feed()

    def sign_feed(self):
        payload = json.dumps(self.feed).encode()
        signature = (
            subprocess.check_output(
                ["go", "run", "./scripts/sign-release", "sign"],
                cwd=ROOT,
                input=payload,
                env=dict(
                    os.environ,
                    POT_UPDATE_PUBLIC_KEY=PUBLIC,
                    POT_UPDATE_PRIVATE_KEY=PRIVATE,
                    CGO_ENABLED="0",
                ),
            )
            .strip()
            .decode()
        )
        self.fixture[FEED_URL] = json.dumps(
            {
                "payload": base64.b64encode(payload).decode(),
                "signature": signature,
                "public_key": PUBLIC,  # Untrusted extra field must never select the key.
            }
        ).encode()

    def run_verifier(self, *options):
        network = FakeNetwork(self.fixture)
        with mock.patch.object(verify, "Network", lambda: network):
            code = verify.main(
                [
                    VERSION,
                    "--public-key",
                    PUBLIC,
                    "--output",
                    self.output.name,
                    "--markdown",
                    *options,
                ]
            )
        return code, json.loads((Path(self.output.name) / "report.json").read_text()), network

    def test_version_key_and_opaque_signed_bytes_are_sufficient(self):
        code, report, network = self.run_verifier()
        self.assertEqual(code, 0, report)
        self.assertEqual(report["status"], "passed")
        self.assertEqual(report["scope"], "public_version_signed_feed_and_listed_downloads")
        self.assertEqual(set(report["downloads"]), set(self.packages))
        self.assertEqual(set(network.requests), {RELEASE_URL, FEED_URL, *self.packages})
        self.assertNotIn("expected_source", report)
        self.assertTrue((Path(self.output.name) / "report.md").exists())

    def test_version_or_feed_not_available_is_incomplete(self):
        for url in [RELEASE_URL, FEED_URL]:
            with self.subTest(url=url):
                data = self.fixture.pop(url)
                code, report, network = self.run_verifier()
                self.assertEqual(code, 2, report)
                self.assertEqual(report["status"], "incomplete")
                self.assertFalse(set(network.requests) & set(self.packages))
                self.fixture[url] = data

    def test_public_release_version_must_match(self):
        self.release["tag_name"] = "v9.9.9"
        self.fixture[RELEASE_URL] = json.dumps(self.release).encode()
        code, report, network = self.run_verifier()
        self.assertEqual(code, 1, report)
        self.assertEqual(network.requests, [RELEASE_URL])

    def test_draft_and_prerelease_are_not_a_public_stable_version(self):
        for field in ["draft", "prerelease"]:
            with self.subTest(field=field):
                release = dict(self.release, **{field: True})
                self.fixture[RELEASE_URL] = json.dumps(release).encode()
                self.assertEqual(self.run_verifier()[0], 2)

    def test_valid_signature_with_wrong_feed_version_fails(self):
        self.feed["version"] = "9.9.9"
        self.sign_feed()
        code, report, network = self.run_verifier()
        self.assertEqual(code, 1, report)
        self.assertIn("version mismatch", report["checks"][-2]["detail"])
        self.assertFalse(set(network.requests) & set(self.packages))

    def test_forged_signature_fails_before_package_downloads(self):
        envelope = json.loads(self.fixture[FEED_URL])
        envelope["signature"] = base64.b64encode(b"x" * 64).decode()
        self.fixture[FEED_URL] = json.dumps(envelope).encode()
        code, report, network = self.run_verifier()
        self.assertEqual(code, 1, report)
        self.assertFalse(set(network.requests) & set(self.packages))

    def test_wrong_trusted_key_cannot_be_replaced_by_feed_key(self):
        wrong = base64.b64encode(b"x" * 32).decode()
        code, report, network = self.run_verifier("--public-key", wrong)
        self.assertEqual(code, 1, report)
        self.assertFalse(set(network.requests) & set(self.packages))

    def test_missing_download_is_incomplete_and_preserves_other_receipts(self):
        url = next(iter(self.packages))
        self.fixture.pop(url)
        code, report, _ = self.run_verifier()
        self.assertEqual(code, 2, report)
        self.assertEqual(len(report["downloads"]), len(self.packages) - 1)
        self.assertEqual(report["download_failures"][0]["url"], url)

    def test_bad_hash_truncated_and_oversized_bytes_fail_without_retry(self):
        url = next(iter(self.packages))
        for data in [b"x" * len(self.packages[url]), b"short", self.packages[url] + b"extra"]:
            with self.subTest(data=data):
                self.fixture[url] = data
                code, report, network = self.run_verifier()
                self.assertEqual(code, 1, report)
                self.assertEqual(network.requests.count(url), 1)
                self.assertTrue(report["download_failures"])

    def test_signed_manifest_cannot_rebind_downloads(self):
        original = copy.deepcopy(self.feed)
        for field, value in [
            ("url", "https://example.org/package"),
            ("url", PREFIX + "../outside"),
            ("url", PREFIX + "file?query"),
            ("sha256", "not-a-digest"),
            ("size", 0),
            ("size", True),
        ]:
            with self.subTest(field=field, value=value):
                self.feed = copy.deepcopy(original)
                self.feed["artifacts"][0][field] = value
                self.sign_feed()
                code, report, network = self.run_verifier()
                self.assertEqual(code, 1, report)
                self.assertFalse(set(network.requests) & set(self.packages))
        # A correctly signed *different* digest still has to match downloaded bytes.
        self.feed = copy.deepcopy(original)
        self.feed["artifacts"][0]["sha256"] = "b" * 64
        self.sign_feed()
        self.assertEqual(self.run_verifier()[0], 1)

    def test_duplicate_and_empty_signed_download_lists_fail(self):
        original = copy.deepcopy(self.feed["artifacts"])
        for artifacts in [[], original + [original[0]]]:
            self.feed["artifacts"] = artifacts
            self.sign_feed()
            self.assertEqual(self.run_verifier()[0], 1)

    def test_preflight_requires_trusted_key_and_existing_go_helper(self):
        self.assertEqual(self.run_verifier("--public-key", "")[0], 1)
        with mock.patch.object(verify.shutil, "which", return_value=None):
            self.assertEqual(self.run_verifier()[0], 1)


class TransportAndScope(unittest.TestCase):
    def test_retry_policy_is_bounded_and_does_not_retry_access_refusals(self):
        for status, attempts in [(503, 3), (429, 3), (404, 1), (401, 1), (403, 1)]:
            network = verify.Network()
            error = urllib.error.HTTPError(
                "https://example.org", status, "fixture", {"Retry-After": "0"}, None
            )
            with (
                mock.patch.object(network.opener, "open", side_effect=error) as request,
                mock.patch.object(verify.time, "sleep"),
            ):
                with self.assertRaises((ValueError, verify.Incomplete)):
                    network.open("https://example.org")
                self.assertEqual(request.call_count, attempts)

    def test_metadata_is_bounded(self):
        network = FakeNetwork({FEED_URL: b"x" * (verify.METADATA_LIMIT + 1)})
        with self.assertRaises(ValueError):
            network.data(FEED_URL)

    def test_public_verification_stays_out_of_workflows(self):
        self.assertFalse((ROOT / ".github/workflows/verify-release.yml").exists())
        for path in (ROOT / ".github/workflows").glob("*.yml"):
            with self.subTest(workflow=path.name):
                text = path.read_text()
                self.assertNotIn("scripts/verify-release", text)
                self.assertNotIn("mise run verify:release", text)
                self.assertNotIn("./.github/workflows/verify-release.yml", text)


if __name__ == "__main__":
    unittest.main()
