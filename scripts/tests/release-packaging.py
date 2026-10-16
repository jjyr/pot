#!/usr/bin/env python3
"""Exercise release ZIP metadata and checksum generation without a build or VM."""

import hashlib
from pathlib import Path
import platform
import plistlib
import runpy
import subprocess
import tempfile
import unittest
import zipfile

REPO = Path(__file__).resolve().parents[2]
package = runpy.run_path(str(REPO / "scripts/release"))["package"]


@unittest.skipUnless(platform.system() == "Darwin", "Packaging uses macOS ditto")
class ReleasePackaging(unittest.TestCase):
    def test_archive_preserves_bundle_and_matches_cask_checksum(self):
        with tempfile.TemporaryDirectory(prefix="pot-release-test-") as directory:
            root = Path(directory)
            app = root / "Pot.app"
            executable = app / "Contents/MacOS/pot-app"
            executable.parent.mkdir(parents=True)
            executable.write_text("#!/bin/sh\nexit 0\n")
            executable.chmod(0o755)
            symlink = executable.parent / "pot-link"
            symlink.symlink_to("pot-app")
            (app / "Contents/Info.plist").write_bytes(
                plistlib.dumps({"CFBundleShortVersionString": "0.1.0"})
            )
            output = root / "output"
            package(app, output, "0.1.0")
            archive = output / "Pot-0.1.0-arm64.zip"
            digest = hashlib.sha256(archive.read_bytes()).hexdigest()
            self.assertEqual(
                (output / f"{archive.name}.sha256").read_text(), f"{digest}  {archive.name}\n"
            )
            cask = (output / "pot.rb").read_text()
            self.assertIn(f'sha256 "{digest}"', cask)
            self.assertIn('version "0.1.0"', cask)
            self.assertNotIn("@VERSION@", cask)
            self.assertNotIn("@SHA256@", cask)
            with zipfile.ZipFile(archive) as zipped:
                self.assertIn("Pot.app/Contents/Info.plist", zipped.namelist())
            extracted = root / "extracted"
            subprocess.run(["ditto", "-x", "-k", str(archive), str(extracted)], check=True)
            restored = extracted / "Pot.app/Contents/MacOS"
            self.assertTrue((restored / "pot-link").is_symlink())
            self.assertEqual((restored / "pot-app").stat().st_mode & 0o777, 0o755)

    def test_embedded_sandbox_resources_are_rejected(self):
        with tempfile.TemporaryDirectory(prefix="pot-release-test-") as directory:
            root = Path(directory)
            app = root / "Pot.app"
            runtime = app / "Contents/Resources"
            runtime.mkdir(parents=True)
            (app / "Contents/Info.plist").write_bytes(
                plistlib.dumps({"CFBundleShortVersionString": "0.1.0"})
            )
            (runtime / "sandbox-image").mkdir()
            with self.assertRaisesRegex(SystemExit, "must not embed"):
                package(app, root / "output", "0.1.0")
            self.assertFalse((root / "output").exists())

    def test_version_mismatch_produces_no_artifacts(self):
        with tempfile.TemporaryDirectory(prefix="pot-release-test-") as directory:
            root = Path(directory)
            app = root / "Pot.app"
            (app / "Contents").mkdir(parents=True)
            (app / "Contents/Info.plist").write_bytes(
                plistlib.dumps({"CFBundleShortVersionString": "0.1.0"})
            )
            output = root / "output"
            with self.assertRaisesRegex(SystemExit, "version"):
                package(app, output, "0.2.0")
            self.assertFalse(output.exists())


if __name__ == "__main__":
    unittest.main()
