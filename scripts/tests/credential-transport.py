#!/usr/bin/env python3
"""Exercise guest request journaling without starting a VM or reading secrets."""

import ast
import json
from pathlib import Path
import sqlite3
import tempfile
import threading
import time
import unittest


ROOT = Path(__file__).resolve().parents[2]


class GuestCredentialTransport(unittest.TestCase):
    def test_transient_environment_is_delivered_but_not_journaled(self):
        for relative in ["podman/image", "apple-vm/bootstrap"]:
            with self.subTest(guest=relative), tempfile.TemporaryDirectory() as directory:
                source = ROOT / "plugins" / relative / "executor.py"
                tree = ast.parse(source.read_text())
                functions = ast.Module(
                    body=[
                        item
                        for item in tree.body
                        if isinstance(item, ast.FunctionDef)
                        and item.name in ("execute", "valid_id")
                    ],
                    type_ignores=[],
                )
                calls = []

                class Runner:
                    def __init__(self, scope, cwd, policy, role):
                        self.cwd, self.policy, self.role = cwd, policy, role

                    def execute(self, request):
                        calls.append(request)
                        return {"result": {"stdout": "authenticated"}, "events": []}

                database = sqlite3.connect(":memory:")
                self.addCleanup(database.close)
                database.execute(
                    "CREATE TABLE operations(id TEXT PRIMARY KEY,input TEXT,result TEXT)"
                )
                namespace = {
                    "json": json,
                    "time": time,
                    "DB": database,
                    "LOCK": threading.Lock(),
                    "RUNNERS": {},
                    "CANCELLED": set(),
                    "workspace": lambda cwd, role: Path(cwd),
                    "Runner": Runner,
                }
                exec(compile(functions, str(source), "exec"), namespace)
                request = {
                    "id": "operation",
                    "scope": "scope",
                    "cwd": directory,
                    "role": "task",
                    "policy": {},
                    "operation": "exec_command",
                    "args": {"cmd": "gh pr list"},
                    "environment": {"GH_TOKEN": "fixture-transient-credential"},
                }
                original = namespace["execute"](request)
                self.assertEqual(
                    calls[0]["environment"]["GH_TOKEN"], "fixture-transient-credential"
                )
                saved = database.execute("SELECT input FROM operations").fetchone()[0]
                self.assertNotIn("fixture-transient-credential", saved)
                self.assertNotIn("environment", json.loads(saved))
                request["environment"] = {"GH_TOKEN": "replacement"}
                self.assertEqual(namespace["execute"](request), original)
                self.assertEqual(len(calls), 1, "token changes must not replay effects")


if __name__ == "__main__":
    unittest.main()
