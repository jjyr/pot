"""Isolated installation contracts; never reads or changes the real Codex home."""

import contextlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest import mock

spec = importlib.util.spec_from_file_location(
    "pot_codex_install", Path(__file__).with_name("install.py")
)
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class InstallTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        root = Path(self.temp.name).resolve()
        self.home = root / "home"
        self.binary = root / "agent"
        self.binary.write_bytes(b"fixture executable")

    def install(self):
        with contextlib.redirect_stdout(io.StringIO()):
            return installer.install(self.home, self.binary, build=False)

    def test_repeat_preserves_owner_data_and_other_plugins(self):
        catalog = self.home / ".agents/plugins/marketplace.json"
        catalog.parent.mkdir(parents=True)
        catalog.write_text(
            json.dumps(
                {
                    "name": "personal",
                    "plugins": [{"name": "other", "source": {"source": "local", "path": "/other"}}],
                }
            )
        )
        destination = self.install()
        pair = self.home / ".config/pot-codex/local-pair.json"
        pair.parent.mkdir(parents=True, exist_ok=True)
        pair.write_text('{"token":"private-owner-value"}')
        before = catalog.read_text()
        self.install()
        self.assertEqual(before, catalog.read_text())
        self.assertIn("private-owner-value", pair.read_text())
        self.assertFalse((self.home / ".config/pot-codex/bridge.json").exists())
        manifest = json.loads((destination / ".codex-plugin/plugin.json").read_text())
        self.assertEqual(manifest["name"], "pot")
        mcp = json.loads((destination / manifest["mcpServers"]).read_text())["mcpServers"]["pot"]
        self.assertEqual(mcp["command"], str(destination / "bin/pot-codex-agent"))
        self.assertEqual(
            mcp["args"], ["host", "--root", str(self.home / ".config/pot-codex/agent")]
        )
        self.assertNotIn("skills", manifest)
        self.assertFalse((destination / "skills").exists())

    def test_local_edits_are_retained(self):
        destination = self.install()
        file = destination / ".mcp.json"
        file.write_text("owner changes")
        with self.assertRaisesRegex(ValueError, "local change"):
            self.install()
        self.assertEqual(file.read_text(), "owner changes")

    def test_missing_dependency_or_binary_does_not_install(self):
        with mock.patch.object(installer.shutil, "which", return_value=None):
            with self.assertRaisesRegex(ValueError, "Go is required"):
                installer.install(self.home, self.binary)
        self.binary.unlink()
        with self.assertRaisesRegex(ValueError, "binary missing"):
            self.install()
        self.assertFalse((self.home / ".agents/plugins/marketplace.json").exists())

    @unittest.skipUnless(
        os.environ.get("POT_CODEX_CLI_TEST"), "Optional real Codex CLI discovery test"
    )
    def test_official_cli_discovers_installs_and_repeats(self):
        self.install()
        for _ in range(2):
            self.assertTrue(
                installer.activate(
                    self.home, self.home / ".codex", os.environ["POT_CODEX_CLI_TEST"]
                )
            )
        version = installer.load(installer.SOURCE / ".codex-plugin/plugin.json")["version"]
        cache = self.home / ".codex/plugins/cache/personal/pot" / version
        self.assertTrue((cache / ".codex-plugin/plugin.json").is_file())
        self.assertFalse((cache / "local-pair.json").exists())
        self.assertFalse((cache / "bridge.json").exists())

    def test_binary_and_config_parent_symlinks_are_rejected_before_writes(self):
        for relative in [".codex/plugins/pot/bin", ".config/pot-codex"]:
            with self.subTest(relative=relative):
                with tempfile.TemporaryDirectory() as directory:
                    self.home = Path(directory) / "home"
                    outside = Path(directory) / "outside"
                    outside.mkdir()
                    link = self.home / relative
                    link.parent.mkdir(parents=True)
                    link.symlink_to(outside, target_is_directory=True)
                    with self.assertRaisesRegex(ValueError, "symlink"):
                        self.install()
                    self.assertEqual(list(outside.iterdir()), [])
                    self.assertFalse((self.home / ".agents/plugins/marketplace.json").exists())

    def test_unmanaged_package_and_symlinks_are_not_overwritten(self):
        destination = self.home / ".codex/plugins/pot"
        destination.mkdir(parents=True)
        (destination / ".mcp.json").write_text("unmanaged")
        with self.assertRaisesRegex(ValueError, "local change"):
            self.install()
        (destination / ".mcp.json").unlink()
        (destination / ".mcp.json").symlink_to(self.binary)
        with self.assertRaisesRegex(ValueError, "symlink"):
            self.install()


if __name__ == "__main__":
    unittest.main()
