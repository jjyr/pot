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
import go_packaging  # noqa: E402


class GoPackaging(unittest.TestCase):
    def test_frontend_diagrams_ship_with_their_complete_notice(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            node = root / "node"
            node.mkdir()
            destination = root / "Resources"
            with (
                mock.patch.object(go_packaging, "node", return_value=node),
                mock.patch.object(go_packaging, "prepare_sandbox"),
                mock.patch.object(go_packaging, "prepare_plugins"),
                mock.patch.object(go_packaging, "call"),
            ):
                go_packaging.prepare_resources(destination, "linux-amd64")
            self.assertEqual(
                (destination / "web/vendor/pele.mjs").read_bytes(),
                (ROOT / "web/src/vendor/pele.mjs").read_bytes(),
            )
            notice = (destination / "FrontendDiagramLicense.txt").read_text()
            self.assertEqual(notice, (ROOT / "web/src/vendor/pele-LICENSE.txt").read_text())
            self.assertIn("MIT License", notice)
            self.assertIn("ISC License", notice)

    def test_tool_packages_contain_only_declared_files(self):
        for platform in ("linux-amd64", "darwin-arm64"):
            with tempfile.TemporaryDirectory() as temporary:
                installed = Path(temporary)
                (installed / "podman").mkdir()
                (installed / "apple-vm").mkdir()
                with mock.patch.object(
                    go_packaging,
                    "go_build",
                    side_effect=AssertionError("tool packages must not build VM binaries"),
                ):
                    go_packaging.prepare_plugins(installed, platform)
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
            with mock.patch.object(go_packaging, "ROOT", root):
                go_packaging.prepare_plugins(root / "installed", "linux-amd64")
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
            go_packaging.copy_tree(source, installed, symlinks=True)
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
            go_packaging, "WAILS_TRAY_SOURCE_HASH", hashlib.sha256(source).hexdigest()
        ):
            patched = go_packaging.apply_tray_patch(source, patch)
            self.assertIn(b"originalSize.width / originalSize.height", patched)
            self.assertNotIn(b"NSMakeSize(thickness, thickness)", patched)
            with self.assertRaisesRegex(ValueError, "source changed"):
                go_packaging.apply_tray_patch(source + b"changed", patch)
            with self.assertRaisesRegex(ValueError, "patch changed"):
                go_packaging.apply_tray_patch(source, patch + b"changed")

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
