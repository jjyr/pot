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
            self.assertFalse((destination / "mcp_config.json").exists())
            self.assertFalse((destination / "hooks.json").exists())
            self.assertFalse((destination / "bin").exists())
            self.assertEqual(install(), destination)
            (destination / "plugin.json").write_text("local edit")
            with self.assertRaisesRegex(ValueError, "local change"):
                install()
            self.assertEqual((destination / "plugin.json").read_text(), "local edit")

    def test_receiver_support_is_independent_of_optional_helper(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary).resolve()
            binary = root / "binary"
            binary.write_bytes(b"fixture")
            home = root / "home with space"
            support = installer.install(home, binary, build=False, activate=False, receiver=True)
            helper = installer.install(home, activate=False)
            self.assertNotEqual(support, helper)
            manifest = json.loads((support / "plugin.json").read_text())
            self.assertEqual(manifest["name"], "pot-receiver")
            hooks = json.loads((support / "hooks.json").read_text())
            command = hooks["pot-execution-scope"]["PreToolUse"][0]["hooks"][0]["command"]
            self.assertIn("pot-antigravity-agent", command)
            self.assertNotIn("__POT_POLICY", command)
            config = json.loads((support / "mcp_config.json").read_text())
            self.assertEqual(config["mcpServers"]["pot_receiver"]["args"][0], "execution-tools")
            self.assertEqual((support / "bin/pot-antigravity-agent").read_bytes(), b"fixture")
            installer.install(home, binary, build=False, activate=False, receiver=True)
            (support / "hooks.json").write_text("owner edit")
            with self.assertRaisesRegex(ValueError, "local change"):
                installer.install(home, binary, build=False, activate=False, receiver=True)

    def test_helper_does_not_spawn_or_modify_environment(self):
        from unittest import mock
        import os

        keys = ("HOME", "CODEX_HOME", "POT_AGENT_JOB_ID", "POT_AGENT_RUN_ID")
        before = {key: os.environ.get(key) for key in keys}
        with (
            tempfile.TemporaryDirectory() as temporary,
            mock.patch.object(
                installer.subprocess, "run", side_effect=AssertionError("helper spawned process")
            ),
        ):
            installer.install(Path(temporary).resolve() / "home", activate=False)
        self.assertEqual({key: os.environ.get(key) for key in keys}, before)

    def test_helper_upgrade_retains_history_and_independent_configuration(self):
        with tempfile.TemporaryDirectory() as temporary:
            home = Path(temporary).resolve() / "home"
            helper = installer.install(home, activate=False)
            previous = json.loads((helper / ".pot-install.json").read_text())
            for name, data in {
                "hooks.json": b"old hook",
                "mcp_config.json": b"old stdio",
                "bin/pot-antigravity-agent": b"old executable",
            }.items():
                target = helper / name
                target.parent.mkdir(parents=True, exist_ok=True)
                target.write_bytes(data)
                previous[name] = installer.digest(data)
            (helper / ".pot-install.json").write_text(json.dumps(previous))
            history = home / ".config/pot-antigravity/agent/history"
            history.parent.mkdir(parents=True)
            history.write_text("retained")
            independent = home / "independent-mcp.json"
            independent.write_text('{"mcpServers":{"other":{"serverUrl":"https://other.invalid"}}}')
            before = independent.read_bytes()
            installer.install(home, activate=False)
            self.assertFalse((helper / "hooks.json").exists())
            self.assertFalse((helper / "mcp_config.json").exists())
            self.assertFalse((helper / "bin/pot-antigravity-agent").exists())
            self.assertEqual(history.read_text(), "retained")
            self.assertEqual(independent.read_bytes(), before)

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
