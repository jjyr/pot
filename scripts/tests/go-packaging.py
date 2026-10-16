#!/usr/bin/env python3
"""Exercise immutable Go resource exports and safe retained-runtime packaging."""

import contextlib
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock
import hashlib

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import go_packaging  # noqa: E402


def exporter():
    loader = __import__("importlib.machinery").machinery.SourceFileLoader(
        "export_go_resources", str(ROOT / "scripts/export-go-sandbox-resources")
    )
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    loader.exec_module(module)
    return module


class GoPackaging(unittest.TestCase):
    def test_tray_patch_refuses_source_or_patch_drift(self):
        patch = (ROOT / "patches/wails-v3.0.0-beta.30-tray-aspect.patch").read_bytes()
        lines = patch.decode().splitlines(keepends=True)
        start = next(i for i, line in enumerate(lines) if line.startswith("@@ ")) + 1
        source = "".join(line[1:] for line in lines[start:] if line.startswith((" ", "-"))).encode()
        with mock.patch.object(
            go_packaging, "WAILS_TRAY_SOURCE_HASH", hashlib.sha256(source).hexdigest()
        ):
            patched = go_packaging.apply_tray_patch(source, patch)
            self.assertIn(b"originalSize.width / originalSize.height", patched)
            self.assertNotIn(b"NSMakeSize(thickness, thickness)", patched)
            with self.assertRaisesRegex(ValueError, "source changed"):
                go_packaging.apply_tray_patch(source + b"changed", patch)
            with self.assertRaisesRegex(ValueError, "patch changed"):
                go_packaging.apply_tray_patch(source, patch + b"changed")

    def test_resource_exports_are_deterministic_and_reject_catalog_drift(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source, output, catalog = root / "source", root / "output", root / "catalog.json"
            source.mkdir()
            (source / "vmlinux").write_bytes(b"pinned-kernel")
            (source / "sandbox-image.json").write_text(
                json.dumps({"reference": "pot.local/sandbox:go-test"})
            )
            for name in ("init-image", "sandbox-image"):
                (source / name).mkdir()
                (source / name / "index.json").write_text('{"schemaVersion":2}')
            export = exporter().export
            with contextlib.redirect_stdout(io.StringIO()):
                export(source, output, "sandbox-go-test-v1", catalog, True)
                first = {path.name: path.read_bytes() for path in output.iterdir()}
                export(source, output, "sandbox-go-test-v1", catalog, False)
                second = {path.name: path.read_bytes() for path in output.iterdir()}
            self.assertEqual(first, second)
            metadata = json.loads(catalog.read_text())
            self.assertEqual(len(metadata["resources"]), 3)
            for descriptor in metadata["resources"].values():
                self.assertEqual(
                    go_packaging.digest(output / descriptor["filename"]), descriptor["sha256"]
                )
            (source / "sandbox-image/index.json").write_text("changed")
            with self.assertRaisesRegex(ValueError, "differ from embedded"):
                export(source, output, "sandbox-go-test-v1", catalog, False)

    def test_download_cache_and_tar_extraction_fail_closed(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "unsafe.tar"
            with tarfile.open(archive, "w") as target:
                member = tarfile.TarInfo("../escaped")
                member.size = 1
                target.addfile(member, io.BytesIO(b"x"))
            with self.assertRaises(tarfile.FilterError):
                go_packaging.extract(archive, root / "destination")
            self.assertFalse((root / "escaped").exists())
            cached = root / "download"
            cached.write_bytes(b"corrupt")
            with self.assertRaisesRegex(ValueError, "Checksum mismatch"):
                go_packaging.download("https://invalid.example/never-requested", cached, "0" * 64)

    def test_service_render_is_reviewable_and_never_installs(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary = root / "package/bin/pot"
            binary.parent.mkdir(parents=True)
            binary.write_bytes(b"fixture")
            output = root / "artifacts/pot-go-runtime.service"
            subprocess.run(
                [
                    sys.executable,
                    ROOT / "scripts/render-go-service",
                    "--binary",
                    binary,
                    "--root",
                    root / "fresh-go-data",
                    "--output",
                    output,
                ],
                check=True,
                capture_output=True,
            )
            unit = output.read_text()
            self.assertIn(str(binary), unit)
            self.assertIn(str(root / "fresh-go-data"), unit)
            self.assertIn("UMask=0077", unit)
            self.assertFalse((root / "fresh-go-data").exists())
            self.assertFalse((root / ".config/systemd/user").exists())


if __name__ == "__main__":
    unittest.main()
