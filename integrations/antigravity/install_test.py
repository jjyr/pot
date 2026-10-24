"""Isolated installation tests; the real Antigravity configuration is never read."""

import importlib.util
import json
from pathlib import Path
import tempfile
import unittest

spec = importlib.util.spec_from_file_location(
    "pot_agy_install", Path(__file__).with_name("install.py")
)
installer = importlib.util.module_from_spec(spec)
spec.loader.exec_module(installer)


class InstallTests(unittest.TestCase):
    def test_native_package_repeat_and_local_edit_protection(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            binary = root / "binary"
            binary.write_bytes(b"executable fixture")
            install = lambda: installer.install(
                root / "home with space", binary, build=False, activate=False
            )
            destination = install()
            config = json.loads((destination / "mcp_config.json").read_text())
            self.assertTrue(Path(config["mcpServers"]["pot"]["command"]).is_absolute())
            self.assertIn(
                "pot-execution-scope", json.loads((destination / "hooks.json").read_text())
            )
            self.assertEqual(install(), destination)
            (destination / "plugin.json").write_text("local edit")
            with self.assertRaisesRegex(ValueError, "local change"):
                install()
            self.assertEqual((destination / "plugin.json").read_text(), "local edit")

    def test_symlink_receipt_is_rejected_before_writes(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            binary = root / "binary"
            binary.write_bytes(b"fixture")
            destination = root / "home/.config/pot-antigravity/plugin"
            destination.mkdir(parents=True)
            outside = root / "outside.json"
            outside.write_text("{}")
            (destination / ".pot-install.json").symlink_to(outside)
            with self.assertRaisesRegex(ValueError, "symlink"):
                installer.install(root / "home", binary, build=False, activate=False)
            self.assertEqual(outside.read_text(), "{}")

    def test_symlink_cannot_replace_external_files(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            binary = root / "binary"
            binary.write_bytes(b"fixture")
            destination = root / "home/.config/pot-antigravity/plugin"
            destination.parent.mkdir(parents=True)
            destination.symlink_to(root, target_is_directory=True)
            with self.assertRaisesRegex(ValueError, "symlink"):
                installer.install(root / "home", binary, build=False, activate=False)


if __name__ == "__main__":
    unittest.main()
