#!/usr/bin/env python3
"""Exercise real hook commands, failures and concurrent timing writes."""

import concurrent.futures
import json
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import unittest

WRAPPER = Path(__file__).resolve().parents[1] / "timed-check"


class HookTiming(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        self.root = Path(directory.name)

    def run_check(self, group, *command):
        return subprocess.run(
            [sys.executable, WRAPPER, group, *command],
            cwd=self.root,
            text=True,
            capture_output=True,
        )

    def records(self):
        return [
            json.loads(line)
            for line in (self.root / ".local/hooks/timings.jsonl").read_text().splitlines()
        ]

    def test_success_preserves_output_and_records_elapsed_time(self):
        result = self.run_check(
            "success", sys.executable, "-c", "import time; print('child output'); time.sleep(.02)"
        )
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("child output", result.stdout)
        self.assertIn("[hook] success: passed in ", result.stdout)
        record = self.records()[0]
        self.assertEqual(record["group"], "success")
        self.assertEqual(record["exit_code"], 0)
        self.assertGreaterEqual(record["elapsed_seconds"], 0.02)
        self.assertLessEqual(record["started_at"], record["finished_at"])

    def test_failure_keeps_exit_status_and_short_circuits_shell_chain(self):
        result = self.run_check("failure", "sh", "-c", "sh -c 'exit 23' && touch should-not-run")
        self.assertEqual(result.returncode, 23)
        self.assertFalse((self.root / "should-not-run").exists())
        self.assertIn("failed in ", result.stdout)
        self.assertEqual(self.records()[0]["exit_code"], 23)

    def test_missing_command_is_reported_and_recorded(self):
        result = self.run_check("missing", "/no-such-hook-command")
        self.assertEqual(result.returncode, 127)
        self.assertEqual(self.records()[0]["exit_code"], 127)
        self.assertIn("/no-such-hook-command", result.stderr)

    def test_signaled_child_uses_shell_exit_status(self):
        result = self.run_check(
            "signal",
            sys.executable,
            "-c",
            "import os, signal; os.kill(os.getpid(), signal.SIGTERM)",
        )
        self.assertEqual(result.returncode, 128 + signal.SIGTERM)
        self.assertEqual(self.records()[0]["exit_code"], 128 + signal.SIGTERM)

    def test_log_failure_does_not_change_command_result(self):
        (self.root / ".local").write_text("not a directory")
        for code in (0, 7):
            result = self.run_check("logging", sys.executable, "-c", f"raise SystemExit({code})")
            self.assertEqual(result.returncode, code)
            self.assertIn("Could not write timing log", result.stderr)

    def test_parallel_groups_append_complete_records(self):
        with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
            results = list(
                pool.map(
                    lambda group: self.run_check(str(group), sys.executable, "-c", "pass"),
                    range(8),
                )
            )
        self.assertTrue(all(result.returncode == 0 for result in results))
        self.assertEqual({record["group"] for record in self.records()}, {str(i) for i in range(8)})


if __name__ == "__main__":
    unittest.main()
