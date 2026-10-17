#!/usr/bin/env python3
"""Guest normalization has no authorization state or write endpoint."""

import importlib.util
from pathlib import Path
import tempfile
import unittest

SOURCE = Path(__file__).resolve().parents[2] / "providers/podman/image/operation_prepare.py"
spec = importlib.util.spec_from_file_location("operation_prepare", SOURCE)
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class Preparation(unittest.TestCase):
    def test_guest_paths_and_shell_defaults_without_permission_storage(self):
        with tempfile.TemporaryDirectory() as directory:
            home = Path(directory).resolve()
            reply = module.prepare(
                {
                    "tool": "exec_command",
                    "args": {"cmd": "true"},
                    "policy": {
                        "executable": "/bin/sh",
                        "allow_login_shell": False,
                        "default_timeout_ms": 123,
                    },
                },
                home,
            )
            self.assertEqual(reply["args"]["workdir"], str(home))
            self.assertEqual(reply["args"]["timeout_ms"], 123)
            self.assertEqual(set(reply), {"args"})
            self.assertEqual(list(home.iterdir()), [])
            with self.assertRaises(ValueError):
                module.prepare({"tool": "exec_command", "args": {"workdir": "../escape"}}, home)

    def test_no_guest_authorization_endpoint(self):
        for relative in ["podman/image", "apple-vm/bootstrap"]:
            source = SOURCE.parents[2] / relative / "executor.py"
            text = source.read_text()
            self.assertNotIn('"/permissions"', text)
            self.assertNotIn("PermissionState", text)
            self.assertIn('"/prepare"', text)


if __name__ == "__main__":
    unittest.main()
