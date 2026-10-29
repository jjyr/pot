#!/usr/bin/env python3
"""Exercise public API build/runtime budgets without compiling or sleeping."""

import contextlib
import importlib.util
import io
from pathlib import Path
import signal
import subprocess
import tempfile
import unittest
from unittest import mock

SPEC = importlib.util.spec_from_file_location(
    "go_public_api", Path(__file__).with_name("go-public-api.py")
)
HARNESS = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(HARNESS)
TEST_COMMAND = ("go", "test", "-mod=mod", "-race", "-count=1", "-timeout=90s", "./...")


class FakeProcess:
    def __init__(self, owner, arguments, options):
        self.owner = owner
        self.arguments = arguments
        self.options = options
        self.pid = 1000 + len(owner.processes)
        self.returncode = None
        self.stdout = io.StringIO() if options.get("stdout") == subprocess.PIPE else None
        self.stderr = None
        self.timeouts = []
        self.waits = []

    def communicate(self, timeout):
        self.timeouts.append(timeout)
        if self.owner.interrupt:
            raise KeyboardInterrupt
        compiling = "-c" in self.arguments
        testing = self.arguments[:2] == ("go", "test")
        cost = 0
        if compiling:
            cost = self.owner.compile_cost
        elif testing:
            cost = self.owner.runtime_cost + (0 if self.owner.warm else self.owner.compile_cost)
        if cost > timeout:
            raise subprocess.TimeoutExpired(self.arguments, timeout)
        self.returncode = self.owner.compile_exit if compiling else self.owner.runtime_exit
        if compiling and self.returncode == 0:
            self.owner.warm = True
        return (self.owner.output if self.stdout is not None else None, None)

    def wait(self, timeout):
        self.waits.append(timeout)
        if self.owner.reap_failure:
            raise subprocess.TimeoutExpired(self.arguments, timeout)
        self.returncode = -signal.SIGKILL
        return self.returncode


class FakeGo:
    def __init__(self):
        self.processes = []
        self.compile_cost = 300
        self.runtime_cost = 5
        self.compile_exit = 0
        self.runtime_exit = 0
        self.warm = False
        self.interrupt = False
        self.reap_failure = False
        self.output = "github.com/jjyr/pot/pkg/agent\n"

    def __call__(self, arguments, **options):
        process = FakeProcess(self, arguments, options)
        self.processes.append(process)
        return process


class HarnessTests(unittest.TestCase):
    def setUp(self):
        self.temporary = Path(self.enterContext(tempfile.TemporaryDirectory()))
        self.consumer = self.temporary / "consumer"
        self.consumer.mkdir()
        self.env = {"GOWORK": "off", "GOTOOLCHAIN": "local"}
        self.fake = FakeGo()
        self.enterContext(mock.patch.object(HARNESS.subprocess, "Popen", self.fake))
        self.killpg = self.enterContext(mock.patch.object(HARNESS.os, "killpg"))
        self.log = self.enterContext(contextlib.redirect_stderr(io.StringIO()))

    def run_tests(self):
        HARNESS.run_consumer_tests(self.consumer, self.temporary, self.env)

    def assert_stopped(self, process):
        self.killpg.assert_called_once_with(process.pid, signal.SIGKILL)
        self.assertEqual(process.waits, [10])
        self.assertEqual(len(process.timeouts), 1)

    def test_cold_build_exceeds_old_budget_but_split_preserves_runtime(self):
        # Simulated compilation costs 300s; no real wall-clock delay is needed.
        with self.assertRaisesRegex(RuntimeError, "old combined.*240s"):
            HARNESS.run(*TEST_COMMAND, cwd=self.consumer, env=self.env, phase="old combined")
        self.assert_stopped(self.fake.processes[0])
        self.killpg.reset_mock()
        self.run_tests()
        compile_process, runtime = self.fake.processes[1:]
        self.assertEqual(compile_process.timeouts, [900])
        self.assertEqual(runtime.timeouts, [240])
        self.assertEqual(runtime.arguments, TEST_COMMAND)
        self.assertEqual(
            compile_process.arguments,
            (
                "go",
                "test",
                "-c",
                "-mod=mod",
                "-race",
                "-o",
                str(self.temporary / "compiled-tests"),
                "./...",
            ),
        )
        self.assertTrue((self.temporary / "compiled-tests").is_dir())
        for process in (compile_process, runtime):
            self.assertTrue(process.options["start_new_session"])
            self.assertEqual(process.options["cwd"], self.consumer)
            self.assertEqual(process.options["env"], self.env)
        self.killpg.assert_not_called()
        self.assertIn("phase complete: 'race compilation'", self.log.getvalue())
        self.assertIn("phase complete: 'race tests'", self.log.getvalue())

    def test_main_gives_both_builds_separate_budget(self):
        with contextlib.redirect_stdout(io.StringIO()):
            HARNESS.main()
        self.assertEqual(len(self.fake.processes), 5)
        self.assertEqual(
            [p.timeouts for p in self.fake.processes], [[900], [240], [240], [900], [240]]
        )
        self.assertEqual(self.fake.processes[0].arguments[:2], ("go", "build"))
        self.assertEqual(self.fake.processes[-1].arguments, TEST_COMMAND)

    def test_compile_failure_prevents_execution(self):
        self.fake.compile_exit = 2
        with self.assertRaisesRegex(RuntimeError, "race compilation.*exit code 2"):
            self.run_tests()
        self.assertEqual(len(self.fake.processes), 1)
        self.assertNotIn("phase complete:", self.log.getvalue())

    def test_compile_timeout_stops_group_and_prevents_execution(self):
        self.fake.compile_cost = 901
        with self.assertRaisesRegex(RuntimeError, "race compilation.*900s"):
            self.run_tests()
        self.assertEqual(len(self.fake.processes), 1)
        self.assert_stopped(self.fake.processes[0])

    def test_runtime_timeout_remains_bounded(self):
        self.fake.runtime_cost = 241
        with self.assertRaisesRegex(RuntimeError, "race tests.*240s"):
            self.run_tests()
        self.assertEqual(len(self.fake.processes), 2)
        self.assert_stopped(self.fake.processes[1])

    def test_runtime_failure_is_not_success(self):
        self.fake.runtime_exit = 1
        with self.assertRaisesRegex(RuntimeError, "race tests.*exit code 1"):
            self.run_tests()
        self.assertNotIn("phase complete: 'race tests'", self.log.getvalue())

    def test_dependency_stdout_is_preserved_and_pipe_closed(self):
        output = HARNESS.run(
            "go",
            "list",
            "-deps",
            "./...",
            cwd=self.consumer,
            env=self.env,
            phase="dependency inspection",
            capture=True,
        )
        self.assertEqual(output, self.fake.output)
        self.assertTrue(self.fake.processes[0].stdout.closed)
        self.killpg.assert_not_called()

    def test_interrupt_stops_group_and_closes_pipe(self):
        self.fake.interrupt = True
        with self.assertRaises(KeyboardInterrupt):
            HARNESS.run(
                *TEST_COMMAND, cwd=self.consumer, env=self.env, phase="race tests", capture=True
            )
        self.assert_stopped(self.fake.processes[0])
        self.assertTrue(self.fake.processes[0].stdout.closed)

    def test_reap_failure_is_reported(self):
        self.fake.compile_cost = 901
        self.fake.reap_failure = True
        with self.assertRaisesRegex(RuntimeError, "race compilation.*failed to reap after 10s"):
            self.run_tests()
        self.assert_stopped(self.fake.processes[0])

    def test_disappeared_group_still_reaps_parent(self):
        self.fake.compile_cost = 901
        self.killpg.side_effect = ProcessLookupError
        with self.assertRaisesRegex(RuntimeError, "race compilation.*900s"):
            self.run_tests()
        self.assert_stopped(self.fake.processes[0])

    def test_group_cleanup_denial_is_reported(self):
        self.fake.compile_cost = 901
        self.killpg.side_effect = PermissionError("denied")
        with self.assertRaisesRegex(RuntimeError, "race compilation.*process-group cleanup failed"):
            self.run_tests()


if __name__ == "__main__":
    unittest.main()
