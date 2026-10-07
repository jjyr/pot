#!/usr/bin/env python3
"""Broker lifecycle contracts with isolated SQLite/cgroup doubles; no root/VM required."""

import ast
import json
from pathlib import Path
import sqlite3
import tempfile
import threading
import time
from types import SimpleNamespace
import unittest
from unittest.mock import MagicMock, patch

SOURCE = Path(__file__).resolve().parents[2] / "sandbox-images/linux/default/executor.py"


class LifecycleTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory(prefix="pot-executor-test-")
        self.db = sqlite3.connect(Path(self.directory.name) / "operations.sqlite")
        self.db.execute("CREATE TABLE operations (id TEXT PRIMARY KEY, input TEXT, result TEXT)")
        # Load production functions without privileged module startup side effects.
        names = {"valid_id", "cancel", "execute", "inspect", "expire_idle"}
        module = ast.parse(SOURCE.read_text())
        module.body = [
            node for node in module.body if isinstance(node, ast.FunctionDef) and node.name in names
        ]
        self.terminate = MagicMock()
        self.env = {
            "json": json,
            "LOCK": threading.RLock(),
            "RUNNERS": {},
            "CANCELLED": set(),
            "DB": self.db,
            "terminate": self.terminate,
            "workspace": lambda cwd, role: Path(cwd),
            "Runner": MagicMock(),
            "time": time,
            "os": SimpleNamespace(kill=MagicMock()),
            "signal": SimpleNamespace(SIGKILL=9),
        }
        exec(compile(module, str(SOURCE), "exec"), self.env)
        self.runner = MagicMock()
        self.runner.cwd = Path("/workspace")
        self.runner.policy = {"session_idle_timeout_ms": 1000}
        self.runner.role = "main"
        self.runner.busy = False
        self.runner.last_used = time.monotonic() - 10
        self.runner.process.pid = 99
        self.env["RUNNERS"]["scope"] = self.runner

    def tearDown(self):
        self.db.close()
        self.directory.cleanup()

    def test_receipts_do_not_reexecute_and_reject_conflicts_or_cancelled_scopes(self):
        request = {
            "id": "call",
            "scope": "scope",
            "role": "main",
            "cwd": "/workspace",
            "policy": self.runner.policy,
            "operation": "exec_command",
            "args": {"cmd": "true"},
        }
        result = {"result": {"exit_code": 0}, "events": []}
        self.runner.execute.return_value = result
        self.assertEqual(self.env["execute"](request), result)
        self.assertEqual(self.env["execute"](request), result)
        self.runner.execute.assert_called_once()
        with self.assertRaisesRegex(ValueError, "conflict"):
            self.env["execute"]({**request, "args": {"cmd": "false"}})
        self.env["cancel"]("scope")
        with self.assertRaisesRegex(ValueError, "ended"):
            self.env["execute"]({**request, "id": "next"})
        self.assertEqual(
            self.env["inspect"]("scope"), {"sessions": [], "active": False, "ended": True}
        )

    def test_failed_cleanup_retains_scope_for_reconciliation(self):
        self.terminate.side_effect = RuntimeError("cleanup failed")
        with self.assertRaisesRegex(RuntimeError, "cleanup failed"):
            self.env["cancel"]("scope")
        self.assertIn("scope", self.env["RUNNERS"])
        self.assertIn("scope", self.env["CANCELLED"])

    def test_idle_inspection_drains_orphans_before_releasing_lease(self):
        self.runner.execute.return_value = {
            "result": {"sessions": [], "active": False, "ended": False}
        }
        pids = self.runner.process.sandbox_cgroup.__truediv__.return_value
        pids.read_text.side_effect = ["99\n100\n", "99\n"]
        self.assertFalse(self.env["inspect"]("scope")["active"])
        self.env["os"].kill.assert_called_once_with(100, 9)
        self.terminate.assert_not_called()  # preserve runner's initialization cache

    def test_live_inspection_does_not_kill_other_processes(self):
        self.runner.execute.return_value = {"result": {"sessions": ["session"], "active": True}}
        self.assertTrue(self.env["inspect"]("scope")["active"])
        self.env["os"].kill.assert_not_called()

    def test_expiry_skips_busy_scopes_and_does_not_replay_commands(self):
        busy = MagicMock()
        busy.busy = True
        busy.last_used = 0
        busy.policy = self.runner.policy
        self.env["RUNNERS"]["busy"] = busy
        with patch.object(time, "sleep", side_effect=[None, InterruptedError]):
            with self.assertRaises(InterruptedError):
                self.env["expire_idle"]()
        self.assertNotIn("scope", self.env["RUNNERS"])
        self.assertIn("busy", self.env["RUNNERS"])
        self.terminate.assert_called_once_with(self.runner.process)
        self.runner.execute.assert_not_called()


if __name__ == "__main__":
    unittest.main()
