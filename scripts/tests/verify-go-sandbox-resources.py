#!/usr/bin/env python3
"""Check reviewed-resource selection, byte verification and atomic staging."""

import hashlib
import importlib.machinery
import importlib.util
import json
from pathlib import Path
import tempfile
import unittest
from unittest import mock


ROOT = Path(__file__).resolve().parents[2]
loader = importlib.machinery.SourceFileLoader(
    "verify_resources", str(ROOT / "plugins/podman/verify-resources")
)
spec = importlib.util.spec_from_loader(loader.name, loader)
verifier = importlib.util.module_from_spec(spec)
loader.exec_module(verifier)
OLD = "sandbox-go-resources-v0.2.0"
NEW = "sandbox-go-resources-v0.3.0"


class ReviewedResources(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.source = self.root / "reviewed"
        self.source.mkdir()
        self.output = self.root / "published"
        self.catalog = self.root / "catalog.json"
        self.resources = {}
        self.asset("old-arm64-image", b"retained image", OLD)
        self.asset("a-arm64-image", b"reviewed new image", NEW)
        self.asset("b-amd64-image", b"reviewed amd64 image", NEW)
        (self.source / "unreviewed-extra").write_bytes(b"never publish")

    def asset(self, name, data, tag):
        (self.source / name).write_bytes(data)
        self.resources[name] = {
            "release_tag": tag,
            "filename": name,
            "sha256": hashlib.sha256(data).hexdigest(),
            "size_bytes": len(data),
        }

    def stage(self, tag=NEW, source=None):
        self.catalog.write_text(json.dumps({"schema_version": 3, "resources": self.resources}))
        return verifier.stage_assets(source or self.source, self.output, tag, self.catalog)

    def assert_no_partial_output(self):
        self.assertFalse(self.output.exists())
        self.assertEqual(list(self.root.glob(".pot-resources-*")), [])

    def test_only_requested_tag_files_are_staged_and_returned(self):
        for tag, names in ((NEW, ["a-arm64-image", "b-amd64-image"]), (OLD, ["old-arm64-image"])):
            with self.subTest(tag=tag):
                result = self.stage(tag)
                self.assertEqual(
                    result, {"tag": tag, "files": [str(self.output / name) for name in names]}
                )
                self.assertEqual(sorted(path.name for path in self.output.iterdir()), names)
                for name in names:
                    self.assertEqual(
                        (self.output / name).read_bytes(), (self.source / name).read_bytes()
                    )
                    (self.output / name).unlink()
                self.output.rmdir()

    def test_missing_asset_cleans_already_staged_files(self):
        (self.source / "b-amd64-image").unlink()
        with self.assertRaises(FileNotFoundError):
            self.stage()
        self.assert_no_partial_output()

    def test_same_size_corruption_cleans_already_staged_files(self):
        path = self.source / "b-amd64-image"
        path.write_bytes(b"x" * path.stat().st_size)
        with self.assertRaisesRegex(ValueError, "checksum"):
            self.stage()
        self.assert_no_partial_output()

    def test_wrong_size_asset_is_rejected(self):
        (self.source / "b-amd64-image").write_bytes(b"truncated")
        with self.assertRaisesRegex(ValueError, "size"):
            self.stage()
        self.assert_no_partial_output()

    def test_symlink_files_and_source_directory_are_rejected(self):
        path = self.source / "b-amd64-image"
        real = self.root / "real-image"
        path.rename(real)
        path.symlink_to(real)
        with self.assertRaises(OSError):
            self.stage()
        self.assert_no_partial_output()
        linked = self.root / "linked-source"
        linked.symlink_to(self.source, target_is_directory=True)
        with self.assertRaises(OSError):
            self.stage(source=linked)
        self.assert_no_partial_output()

    def test_nonregular_asset_is_rejected_without_blocking(self):
        path = self.source / "b-amd64-image"
        path.unlink()
        verifier.os.mkfifo(path)
        with self.assertRaisesRegex(ValueError, "type"):
            self.stage()
        self.assert_no_partial_output()

    def test_unsafe_and_duplicate_catalog_names_are_rejected(self):
        original = self.resources["b-amd64-image"]["filename"]
        for name in ("../outside", "/absolute", ".", "name#label", "a-arm64-image"):
            with self.subTest(name=name):
                self.resources["b-amd64-image"]["filename"] = name
                with self.assertRaisesRegex(ValueError, "catalog asset"):
                    self.stage()
                self.assert_no_partial_output()
        self.resources["b-amd64-image"]["filename"] = original

    def test_existing_output_and_output_symlink_are_preserved(self):
        self.output.mkdir()
        marker = self.output / "existing"
        marker.write_bytes(b"keep")
        with self.assertRaisesRegex(ValueError, "already exist"):
            self.stage()
        self.assertEqual(marker.read_bytes(), b"keep")
        marker.unlink()
        self.output.rmdir()
        self.output.symlink_to(self.root / "absent")
        with self.assertRaisesRegex(ValueError, "already exist"):
            self.stage()
        self.assertTrue(self.output.is_symlink())

    def test_unknown_tag_and_relative_paths_are_rejected(self):
        for tag in ("sandbox-go-resources-v9.0.0", "../tag"):
            with self.subTest(tag=tag), self.assertRaises(ValueError):
                self.stage(tag)
        with self.assertRaisesRegex(ValueError, "absolute"):
            self.stage(source=Path("relative"))
        self.assert_no_partial_output()

    def test_publish_failure_removes_staging_and_leaves_output_absent(self):
        with mock.patch.object(Path, "rename", side_effect=OSError("injected publish failure")):
            with self.assertRaisesRegex(OSError, "injected publish failure"):
                self.stage()
        self.assert_no_partial_output()


if __name__ == "__main__":
    unittest.main()
