#!/usr/bin/env python3
"""Check that the real cargo-about policy rejects disallowed and unknown licenses."""

import json
from pathlib import Path
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]


class LicensePolicy(unittest.TestCase):
    def scan(self, license):
        with tempfile.TemporaryDirectory(prefix="pot-license-policy-") as directory:
            root = Path(directory)
            (root / "src").mkdir()
            (root / "src/lib.rs").write_text("pub fn fixture() {}\n")
            (root / "Cargo.toml").write_text(
                '[package]\nname = "pot-license-policy-fixture"\nversion = "0.1.0"\nedition = "2024"\n'
                + f"license = {json.dumps(license)}\n[workspace]\n"
            )
            subprocess.run(
                [
                    "cargo",
                    "generate-lockfile",
                    "--offline",
                    "--manifest-path",
                    str(root / "Cargo.toml"),
                ],
                check=True,
            )
            return subprocess.run(
                [
                    "cargo",
                    "about",
                    "generate",
                    "--offline",
                    "--locked",
                    "--fail",
                    "--manifest-path",
                    str(root / "Cargo.toml"),
                    "--config",
                    str(ROOT / "about.toml"),
                    "--format",
                    "json",
                ],
                capture_output=True,
                text=True,
            )

    def test_mit_is_accepted(self):
        result = self.scan("MIT")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(json.loads(result.stdout)["licenses"])

    def test_gpl_is_rejected(self):
        result = self.scan("GPL-3.0-only")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("failed to satisfy license requirements", result.stderr)

    def test_unknown_license_is_rejected(self):
        result = self.scan("LicenseRef-PotUnknown")
        self.assertNotEqual(result.returncode, 0)


if __name__ == "__main__":
    unittest.main()
