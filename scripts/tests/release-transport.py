#!/usr/bin/env python3
"""Exercise transient reads, ambiguous writes and partial artifact retries."""

import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import release_candidate as candidate  # noqa: E402
import release_transport as transport  # noqa: E402


class Transport(unittest.TestCase):
    def result(self, status=0, stderr=""):
        return subprocess.CompletedProcess(
            [], int(bool(status)), json.dumps({"status": status}), stderr
        )

    def test_only_transient_reads_retry(self):
        for status in (429, 500, 502, 503, 504, 401, 403, 404, 422):
            with (
                self.subTest(status=status),
                mock.patch.object(
                    transport.subprocess, "run", side_effect=[self.result(status), self.result()]
                ) as run,
                mock.patch.object(transport.time, "sleep"),
            ):
                result = transport.run(["gh"], read_only=True)
                expected = 2 if status in transport.RETRY_STATUSES else 1
                self.assertEqual(run.call_count, expected)
                self.assertEqual(result.returncode, 0 if expected == 2 else 1)

    def test_write_errors_and_timeouts_are_not_retried(self):
        for failure in (self.result(503), subprocess.TimeoutExpired(["gh"], 30)):
            with mock.patch.object(transport.subprocess, "run", side_effect=[failure]) as run:
                if isinstance(failure, Exception):
                    with self.assertRaises(subprocess.TimeoutExpired):
                        transport.run(["gh"])
                else:
                    self.assertEqual(transport.run(["gh"]).returncode, 1)
                self.assertEqual(run.call_count, 1)

    def test_attempts_are_bounded_and_permission_beats_timeout_text(self):
        with (
            mock.patch.object(transport.subprocess, "run", return_value=self.result(503)) as run,
            mock.patch.object(transport.time, "sleep") as sleep,
        ):
            self.assertEqual(transport.run(["gh"], read_only=True).returncode, 1)
            self.assertEqual(run.call_count, 3)
            self.assertEqual(sleep.call_args_list, [mock.call(1), mock.call(2)])
        with mock.patch.object(
            transport.subprocess, "run", return_value=self.result(403, "timeout denied")
        ) as run:
            transport.run(["gh"], read_only=True)
            self.assertEqual(run.call_count, 1)

    def test_http_stderr_and_timeout_reads_retry(self):
        for failure in (
            subprocess.CompletedProcess([], 1, "", "HTTP 502: Bad Gateway"),
            subprocess.TimeoutExpired(["gh"], 30),
        ):
            with (
                mock.patch.object(
                    transport.subprocess, "run", side_effect=[failure, self.result()]
                ) as run,
                mock.patch.object(transport.time, "sleep"),
            ):
                self.assertEqual(transport.run(["gh"], read_only=True).returncode, 0)
                self.assertEqual(run.call_count, 2)

    def test_partial_download_is_replaced_and_integrity_is_not_retried(self):
        blob = b"complete archive"
        artifact = {
            "id": 4,
            "size_in_bytes": len(blob),
            "digest": "sha256:" + hashlib.sha256(blob).hexdigest(),
        }
        calls = []

        def download(command, **kwargs):
            calls.append(command)
            kwargs["stdout"].write(b"partial partial partial" if len(calls) == 1 else blob)
            return subprocess.CompletedProcess(
                command, int(len(calls) == 1), stderr=b"HTTP 503" if len(calls) == 1 else b""
            )

        with (
            tempfile.TemporaryDirectory() as root,
            mock.patch.object(transport.subprocess, "run", side_effect=download),
            mock.patch.object(transport.time, "sleep"),
        ):
            path = Path(root) / "archive"
            candidate.download(artifact, path)
            self.assertEqual(path.read_bytes(), blob)
            self.assertEqual(len(calls), 2)
        with (
            tempfile.TemporaryDirectory() as root,
            mock.patch.object(transport.subprocess, "run", side_effect=download),
        ):
            artifact["digest"] = "sha256:" + "0" * 64
            with self.assertRaisesRegex(candidate.Error, "digest mismatch"):
                candidate.download(artifact, Path(root) / "archive")
            self.assertEqual(len(calls), 3)

    def test_diagnostics_keep_status_but_remove_credentials(self):
        result = subprocess.CompletedProcess(
            [],
            1,
            '{"status":502,"message":"gateway failure"}',
            "HTTP 502 https://signed.example/asset?sig=private ghp_abcdef Authorization: bearer-secret",
        )
        detail = transport.diagnostic(result)
        self.assertIn("502", detail)
        self.assertIn("gateway failure", detail)
        for secret in ("signed.example", "private", "ghp_abcdef", "bearer-secret"):
            self.assertNotIn(secret, detail)
        self.assertNotIn(
            "secret-command",
            transport.diagnostic(subprocess.TimeoutExpired(["secret-command"], 30)),
        )
        for header in (
            "Authorization: Bearer opaque-token",
            "Authorization: Basic opaque-basic",
            "Authorization: Custom opaque-custom",
        ):
            result.stderr = (
                "HTTP 502\r\n" + header + "\r\nredirect https://host/path?sig=opaque-signed"
            )
            detail = transport.diagnostic(result)
            self.assertNotIn("opaque-", detail)
            self.assertIn("502", detail)


if __name__ == "__main__":
    unittest.main()
