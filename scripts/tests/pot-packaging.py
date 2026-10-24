#!/usr/bin/env python3
"""Verify capability packaging, pinned runtime assets and service artifacts."""

import hashlib
import io
import json
import shutil
from pathlib import Path
import subprocess
import sys
import tarfile
import tempfile
import unittest
from unittest import mock

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "scripts"))
import pot_packaging  # noqa: E402


class GoPackaging(unittest.TestCase):
    def test_frontend_diagrams_ship_with_their_complete_notice(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            node = root / "node"
            node.mkdir()
            destination = root / "Resources"
            with (
                mock.patch.object(pot_packaging, "node", return_value=node),
                mock.patch.object(pot_packaging, "prepare_sandbox"),
                mock.patch.object(pot_packaging, "prepare_plugins"),
                mock.patch.object(pot_packaging, "call"),
            ):
                pot_packaging.prepare_resources(destination, "linux-amd64")
            self.assertEqual(
                (destination / "web/vendor/pele.mjs").read_bytes(),
                (ROOT / "web/src/vendor/pele.mjs").read_bytes(),
            )
            notice = (destination / "FrontendDiagramLicense.txt").read_text()
            self.assertEqual(notice, (ROOT / "web/src/vendor/pele-LICENSE.txt").read_text())
            self.assertIn("MIT License", notice)
            self.assertIn("ISC License", notice)

    def test_frontend_virtualizer_ships_offline_with_its_notice(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            node = root / "node"
            node.mkdir()
            destination = root / "Resources"
            with (
                mock.patch.object(pot_packaging, "node", return_value=node),
                mock.patch.object(pot_packaging, "prepare_sandbox"),
                mock.patch.object(pot_packaging, "prepare_plugins"),
                mock.patch.object(pot_packaging, "call"),
            ):
                pot_packaging.prepare_resources(destination, "linux-amd64")
            self.assertEqual(
                (destination / "web/vendor/tanstack-virtual-core.mjs").read_bytes(),
                (ROOT / "web/src/vendor/tanstack-virtual-core.mjs").read_bytes(),
            )
            notice = (destination / "FrontendVirtualizerLicense.txt").read_text()
            self.assertEqual(
                notice,
                (ROOT / "web/src/vendor/tanstack-virtual-core-LICENSE.txt").read_text(),
            )
            self.assertIn("MIT License", notice)

    def test_tool_packages_contain_only_declared_files(self):
        for platform in ("linux-amd64", "darwin-arm64"):
            with tempfile.TemporaryDirectory() as temporary:
                installed = Path(temporary)
                (installed / "retired-plugin").mkdir()
                (installed / "stale-file").write_text("generated")
                with mock.patch.object(
                    pot_packaging,
                    "go_build",
                    side_effect=AssertionError("Node plugin packages must not build Go binaries"),
                ):
                    pot_packaging.prepare_plugins(installed, platform)
                    current = {
                        str(p.relative_to(installed)): p.read_bytes()
                        for p in installed.rglob("*")
                        if p.is_file()
                    }
                    package = next(installed.iterdir())
                    (package / "undeclared-file").write_text("stale")
                    (installed / "another-retired-plugin").mkdir()
                    pot_packaging.prepare_plugins(installed, platform)
                    self.assertEqual(
                        current,
                        {
                            str(p.relative_to(installed)): p.read_bytes()
                            for p in installed.rglob("*")
                            if p.is_file()
                        },
                    )
                self.assertEqual(
                    sorted(p.name for p in installed.iterdir()),
                    sorted(p.parent.name for p in (ROOT / "plugins").glob("*/plugin.json")),
                )
                for package in installed.iterdir():
                    manifest = json.loads((package / "plugin.json").read_text())
                    tools = manifest["contributes"].get("tools")
                    if tools is not None:
                        self.assertEqual(tools["execution"], "runtime")
                    self.assertEqual(
                        sorted(p.name for p in package.iterdir()),
                        sorted(
                            {
                                "plugin.json",
                                *(
                                    name
                                    for name in (
                                        "icon.svg",
                                        "icon.png",
                                        "icon-NOTICE.txt",
                                        "icon-LICENSE.txt",
                                    )
                                    if (ROOT / "plugins" / package.name / name).is_file()
                                ),
                                *(
                                    name
                                    for key, capability in manifest["contributes"].items()
                                    for name in (
                                        capability.get("adapter", {}).get("files", [])
                                        if key == "model_providers"
                                        else capability["files"]
                                    )
                                ),
                            }
                        ),
                    )

    def test_plugin_sync_confines_cleanup_to_generated_output(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            outside = root / "user-installed"
            outside.mkdir()
            sentinel = outside / "keep.txt"
            sentinel.write_text("user data")
            output = root / "generated"
            output.symlink_to(outside, target_is_directory=True)
            with self.assertRaisesRegex(ValueError, "symlink"):
                pot_packaging.prepare_plugins(output, "linux-amd64")
            output.unlink()
            output.mkdir()
            # Both obsolete and current package links must be unlinked, not traversed.
            (output / "retired-plugin").symlink_to(outside, target_is_directory=True)
            current = next((ROOT / "plugins").glob("*/plugin.json")).parent.name
            (output / current).symlink_to(outside, target_is_directory=True)
            (output / "dangling").symlink_to(root / "missing")
            pot_packaging.prepare_plugins(output, "linux-amd64")
            self.assertFalse((output / "retired-plugin").exists())
            self.assertFalse((output / current).is_symlink())
            self.assertFalse((output / "dangling").is_symlink())
            self.assertEqual(sentinel.read_text(), "user data")
            self.assertEqual(list(outside.iterdir()), [sentinel])

    def test_plugin_sync_rejects_source_overlap(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "plugins/example"
            source.mkdir(parents=True)
            sentinel = source / "plugin.json"
            sentinel.write_text("source")
            with mock.patch.object(pot_packaging, "ROOT", root):
                for output in (root.parent, root, root / "plugins", source, source / "output"):
                    with self.subTest(output=output):
                        with self.assertRaisesRegex(ValueError, "overlap"):
                            pot_packaging.prepare_plugins(output, "linux-amd64")
                alias = root / "source-alias"
                alias.symlink_to(root / "plugins", target_is_directory=True)
                with self.assertRaisesRegex(ValueError, "overlap"):
                    pot_packaging.prepare_plugins(alias / "example", "linux-amd64")
            self.assertEqual(sentinel.read_text(), "source")

    def test_plugin_sync_preserves_output_when_source_is_unsafe(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "plugins/example"
            source.mkdir(parents=True)
            outside = root / "private"
            outside.mkdir()
            (outside / "entry.mjs").write_text("private")
            output = root / "generated"
            output.mkdir()
            sentinel = output / "previous-package"
            sentinel.write_text("previous output")
            declaration = {
                "runtime": "node",
                "execution": "runtime",
                "files": [],
            }
            (source / "linked").symlink_to(outside, target_is_directory=True)
            for name in ("../private/entry.mjs", str(outside / "entry.mjs"), "linked/entry.mjs"):
                declaration["files"] = [name]
                (source / "plugin.json").write_text(
                    json.dumps(
                        {
                            "schema_version": 3,
                            "name": "example",
                            "contributes": {"tools": declaration},
                        }
                    )
                )
                with self.subTest(file=name), mock.patch.object(pot_packaging, "ROOT", root):
                    with self.assertRaisesRegex(ValueError, "unsafe plugin file"):
                        pot_packaging.prepare_plugins(output, "linux-amd64")
                self.assertEqual(sentinel.read_text(), "previous output")
                self.assertEqual(list(output.iterdir()), [sentinel])
            self.assertEqual((outside / "entry.mjs").read_text(), "private")

    def test_model_provider_packages_preserve_only_adapter_files(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            plugins = root / "plugins"
            definition = {
                "id": "example",
                "name": "Example",
                "protocol": "openai_responses",
                "capabilities": {"catalog": "http"},
            }
            for name in ("declarative", "adapter"):
                source = plugins / name
                source.mkdir(parents=True)
                contribution = {"providers": [definition]}
                if name == "adapter":
                    contribution["adapter"] = {
                        "runtime": "node",
                        "execution": "control",
                        "entrypoint": "reader.mjs",
                        "files": ["reader.mjs"],
                    }
                    (source / "reader.mjs").write_text("export async function read() {}")
                (source / "private.txt").write_text("undeclared")
                (source / "plugin.json").write_text(
                    json.dumps(
                        {
                            "schema_version": 3,
                            "name": name,
                            "version": "1",
                            "contributes": {"model_providers": contribution},
                        }
                    )
                )
            with mock.patch.object(pot_packaging, "ROOT", root):
                pot_packaging.prepare_plugins(root / "installed", "linux-amd64")
            self.assertEqual(
                sorted(p.name for p in (root / "installed/declarative").iterdir()), ["plugin.json"]
            )
            self.assertEqual(
                sorted(p.name for p in (root / "installed/adapter").iterdir()),
                ["plugin.json", "reader.mjs"],
            )

    def test_node_distribution_preserves_relocatable_npm_links(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            source = root / "node"
            (source / "bin").mkdir(parents=True)
            (source / "lib/node_modules/npm/bin").mkdir(parents=True)
            (source / "lib/node_modules/npm/lib").mkdir()
            (source / "lib/node_modules/npm/bin/npm-cli.js").write_text(
                "console.log(require('../lib/cli.js'));"
            )
            (source / "lib/node_modules/npm/lib/cli.js").write_text(
                "module.exports = 'relocated-npm';"
            )
            (source / "bin/npm").symlink_to("../lib/node_modules/npm/bin/npm-cli.js")
            installed = root / "relocated/Resources/node"
            pot_packaging.copy_tree(source, installed, symlinks=True)
            shutil.rmtree(source)
            self.assertTrue((installed / "bin/npm").is_symlink())
            result = subprocess.check_output(
                ["node", installed / "bin/npm"],
                text=True,
            )
            self.assertEqual(result.strip(), "relocated-npm")

    def test_tray_patch_refuses_source_or_patch_drift(self):
        patch = (ROOT / "patches/wails-v3.0.0-beta.30-tray-aspect.patch").read_bytes()
        lines = patch.decode().splitlines(keepends=True)
        start = next(i for i, line in enumerate(lines) if line.startswith("@@ ")) + 1
        source = "".join(line[1:] for line in lines[start:] if line.startswith((" ", "-"))).encode()
        with mock.patch.object(
            pot_packaging, "WAILS_TRAY_SOURCE_HASH", hashlib.sha256(source).hexdigest()
        ):
            patched = pot_packaging.apply_tray_patch(source, patch)
            self.assertIn(b"originalSize.width / originalSize.height", patched)
            self.assertNotIn(b"NSMakeSize(thickness, thickness)", patched)
            with self.assertRaisesRegex(ValueError, "source changed"):
                pot_packaging.apply_tray_patch(source + b"changed", patch)
            with self.assertRaisesRegex(ValueError, "patch changed"):
                pot_packaging.apply_tray_patch(source, patch + b"changed")

    def test_download_cache_and_tar_extraction_fail_closed(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            archive = root / "unsafe.tar"
            with tarfile.open(archive, "w") as target:
                member = tarfile.TarInfo("../escaped")
                member.size = 1
                target.addfile(member, io.BytesIO(b"x"))
            with self.assertRaises(tarfile.FilterError):
                pot_packaging.extract(archive, root / "destination")
            self.assertFalse((root / "escaped").exists())
            cached = root / "download"
            cached.write_bytes(b"corrupt")
            with self.assertRaisesRegex(ValueError, "Checksum mismatch"):
                pot_packaging.download("https://invalid.example/never-requested", cached, "0" * 64)

    def test_service_render_is_reviewable_and_never_installs(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            binary = root / "package/bin/pot"
            binary.parent.mkdir(parents=True)
            binary.write_bytes(b"fixture")
            output = root / "artifacts/pot-runtime.service"
            subprocess.run(
                [
                    sys.executable,
                    ROOT / "scripts/render-pot-service",
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
            self.assertIn("unit named pot-runtime.service", unit)
            self.assertLess(
                unit.index(".config/pot-go/runtime.env"), unit.index(".config/pot/runtime.env")
            )
            self.assertFalse((root / "fresh-go-data").exists())
            self.assertFalse((root / ".config/systemd/user").exists())


if __name__ == "__main__":
    unittest.main()
