#!/usr/bin/env python3
"""Guest permission preparation and publication without root or a VM."""

import ast
import json
import importlib.util
from pathlib import Path
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parents[2] / "sandbox-images/linux/default/permission_state.py"
spec = importlib.util.spec_from_file_location("permission_state", SOURCE)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class Permissions(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve()
        self.home = self.root / "workspace"
        self.home.mkdir()
        self.state = module.PermissionState(self.root / ".pot")

    def test_guest_defaults_to_allow_and_explicit_ask_is_preserved(self):
        empty = self.state.load()
        self.assertEqual(empty["policy"], {"default": "allow"})
        strict = {"version": 1, "policy": {"default": "ask"}, "grants": []}
        self.state.request({"action": "write", "expected": empty, "config": strict}, self.home)
        self.assertEqual(self.state.load(), strict)
        for policy in [{"default": "invalid"}, {"default": "allow", "other": True}]:
            with self.assertRaises(ValueError):
                self.state.request(
                    {
                        "action": "write",
                        "expected": strict,
                        "config": {"version": 1, "policy": policy, "grants": []},
                    },
                    self.home,
                )
        self.assertEqual(self.state.load(), strict)

    def test_macos_policy_protects_raw_storage_and_home_view(self):
        source = SOURCE.parents[2] / "macos/xcode/executor.py"
        tree = ast.parse(source.read_text())
        tree.body = [
            node
            for node in tree.body
            if isinstance(node, ast.FunctionDef) and node.name == "permission_profile"
        ]
        namespace = {"json": json, "PERMISSIONS": self.state}
        exec(compile(tree, str(source), "exec"), namespace)
        profile = namespace["permission_profile"](self.home)
        self.assertIn(
            "(deny file-write* (subpath " + json.dumps(str(self.state.base.parent)) + "))", profile
        )
        self.assertIn(
            "(deny file-write* (subpath " + json.dumps(str(self.home / ".pot")) + "))", profile
        )

    def test_current_config_cas_and_restart(self):
        empty = self.state.request({"action": "read"}, self.home)["config"]
        saved = {"version": 1, "grants": [{"id": "one", "agent": "main"}]}
        self.state.request({"action": "write", "expected": empty, "config": saved}, self.home)
        with self.assertRaises(ValueError):
            self.state.request({"action": "write", "expected": empty, "config": empty}, self.home)
        restored = module.PermissionState(self.state.base)
        self.assertEqual(restored.load(), saved)
        restored.request({"action": "write", "expected": saved, "config": empty}, self.home)
        self.assertEqual(self.state.load(), empty)
        self.assertEqual(list(self.state.directory.glob(".permissions-*")), [])

    def prepare(self, tool, args):
        return self.state.request(
            {
                "action": "prepare",
                "tool": tool,
                "args": args,
                "policy": {
                    "executable": "/bin/bash",
                    "allow_login_shell": True,
                    "default_timeout_ms": 60000,
                },
            },
            self.home,
        )["args"]

    def test_guest_resolves_relative_workdir_and_rejects_traversal(self):
        target = self.home / "directory"
        target.mkdir()
        (self.home / "link").symlink_to(target)
        self.assertEqual(
            self.prepare("exec_command", {"cmd": "true", "workdir": "link"})["workdir"],
            str(target),
        )
        with self.assertRaises(ValueError):
            self.prepare("exec_command", {"cmd": "true", "workdir": "../secret"})

    def test_command_preparation_is_concrete_and_sessions_remain_unbounded(self):
        args = self.prepare("exec_command", {"cmd": "true"})
        self.assertEqual(
            args,
            {
                "cmd": "true",
                "workdir": str(self.home),
                "shell": "/bin/bash",
                "login": True,
                "tty": False,
                "timeout_ms": 60000,
            },
        )
        args = self.prepare("exec_command", {"cmd": "true", "yield_time_ms": 1})
        self.assertNotIn("timeout_ms", args)

    def test_backing_and_home_views_are_both_readonly(self):
        mounts = self.state.mounts(self.home)
        self.assertEqual(
            mounts,
            [
                (str(self.state.base), str(self.state.base), True),
                (str(self.state.base), str(self.home / ".pot"), True),
            ],
        )
        self.assertTrue(self.state.file.is_file())

    def test_symlinked_backing_directory_and_config_are_rejected(self):
        self.state.ensure()
        self.state.file.unlink()
        victim = self.root / "victim"
        victim.write_text("unchanged")
        self.state.file.symlink_to(victim)
        with self.assertRaises(ValueError):
            self.state.load()
        self.assertEqual(victim.read_text(), "unchanged")
        other = self.root / "other"
        other.symlink_to(self.state.base, target_is_directory=True)
        with self.assertRaises(ValueError):
            module.PermissionState(other).load()


if __name__ == "__main__":
    unittest.main()
