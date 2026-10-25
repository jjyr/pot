#!/usr/bin/env python3
"""The standard entry must retain every stage and propagate the first failure."""

from contextlib import redirect_stdout
import importlib.util
from io import StringIO
import json
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "ui_http_suite", Path(__file__).with_name("ui-http-suite.py")
)
suite = importlib.util.module_from_spec(spec)
spec.loader.exec_module(suite)


class SmokeRunnerContract(unittest.TestCase):
    def test_complete_plan_builds_http_first_and_runs_every_original_stage(self):
        output = StringIO()
        with patch.object(
            suite.subprocess, "run", return_value=SimpleNamespace(returncode=0)
        ) as run:
            with redirect_stdout(output):
                self.assertEqual(suite.run_suite(), 0)
        scripts = [Path(call.args[0][1]).name for call in run.call_args_list]
        self.assertEqual(
            scripts,
            [
                "pot-ui-http-integration.py",
                "pot-ui-assistant-start-browser.py",
                "pot-ui-assistant-settings-browser.py",
                "pot-ui-linked-approvals-browser.py",
                "pot-ui-composer-selectors-browser.py",
                "pot-ui-usage-browser.py",
            ],
        )
        self.assertTrue(
            all((suite.ROOT / "scripts/tests" / script).is_file() for script in scripts)
        )
        summary = json.loads(output.getvalue().splitlines()[-1])
        self.assertEqual(
            (summary["completed"], summary["total"], summary["status"]), (6, 6, "passed")
        )
        self.assertGreaterEqual(summary["seconds"], 0)

    def test_failure_stops_remaining_stages_and_cannot_report_passed(self):
        output = StringIO()
        with patch.object(
            suite.subprocess,
            "run",
            side_effect=[SimpleNamespace(returncode=0), SimpleNamespace(returncode=7)],
        ) as run:
            with redirect_stdout(output):
                self.assertEqual(suite.run_suite(), 7)
        self.assertEqual(run.call_count, 2)
        summary = json.loads(output.getvalue().splitlines()[-1])
        self.assertEqual((summary["completed"], summary["status"]), (1, "incomplete"))


if __name__ == "__main__":
    unittest.main()
