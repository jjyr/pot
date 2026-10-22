#!/usr/bin/env python3
"""Exercise the container supervisor with real disposable child processes."""

import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest

ROOT = Path(__file__).resolve().parents[2]


class EntrypointTest(unittest.TestCase):
    def run_service(self, exit_role=None):
        temporary = tempfile.TemporaryDirectory(prefix="pot-entrypoint-")
        self.addCleanup(temporary.cleanup)
        home = Path(temporary.name)
        pot = home / "pot"
        pot.write_text(
            f"#!{sys.executable}\n"
            "import os, pathlib, signal, sys, time\n"
            "home=pathlib.Path(os.environ['FAKE_HOME']); role=sys.argv[1]\n"
            "if role=='status': sys.exit(0 if (home/'serve.pid').exists() else 1)\n"
            "(home/(role+'.pid')).write_text(str(os.getpid()))\n"
            "if role==os.environ.get('FAKE_EXIT'): sys.exit(0)\n"
            "signal.signal(signal.SIGTERM, lambda *_: sys.exit(0))\n"
            "while True: time.sleep(0.05)\n"
        )
        pot.chmod(0o700)
        bootstrap = (
            "import importlib.util,sys; from pathlib import Path; "
            "spec=importlib.util.spec_from_file_location('entry',sys.argv[1]); "
            "entry=importlib.util.module_from_spec(spec); spec.loader.exec_module(entry); "
            "home=Path(sys.argv[2]); entry.POT=str(home/'pot'); entry.HOME=str(home); "
            "entry.ROOT=str(home/'runtime'); entry.BROWSER=str(home/'browser'); "
            "entry.WORKSPACE=str(home/'work'); sys.argv=['entry','server']; "
            "sys.exit(entry.main())"
        )
        process = subprocess.Popen(
            [sys.executable, "-c", bootstrap, str(ROOT / "docker/entrypoint.py"), str(home)],
            env={
                **os.environ,
                "FAKE_HOME": str(home),
                "FAKE_EXIT": exit_role or "",
                "POT_WEB_ORIGIN": "http://localhost:17703",
            },
            stdout=subprocess.DEVNULL,
            stderr=subprocess.PIPE,
        )

        def cleanup():
            if process.poll() is None:
                process.terminate()
                process.wait(timeout=40)
            process.stderr.close()

        self.addCleanup(cleanup)
        return process, home

    def assert_children_gone(self, home):
        for file in home.glob("*.pid"):
            with self.assertRaises(ProcessLookupError):
                os.kill(int(file.read_text()), 0)

    def test_term_stops_both_children_cleanly(self):
        process, home = self.run_service()
        deadline = time.monotonic() + 8
        while not (home / "web-ui-server.pid").exists():
            self.assertIsNone(process.poll())
            self.assertLess(time.monotonic(), deadline)
            time.sleep(0.05)
        process.send_signal(signal.SIGTERM)
        self.assertEqual(process.wait(timeout=8), 0)
        self.assert_children_gone(home)

    def test_unexpected_browser_exit_stops_runtime(self):
        process, home = self.run_service("web-ui-server")
        self.assertEqual(process.wait(timeout=8), 1)
        self.assert_children_gone(home)

    def test_runtime_startup_exit_does_not_start_browser(self):
        process, home = self.run_service("serve")
        self.assertEqual(process.wait(timeout=8), 1)
        self.assertFalse((home / "web-ui-server.pid").exists())
        self.assert_children_gone(home)


if __name__ == "__main__":
    unittest.main()
