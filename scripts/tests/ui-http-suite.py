#!/usr/bin/env python3
"""Run the complete standard browser smoke with explicit failure and timing output."""

import argparse
import json
from pathlib import Path
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
CASES = (
    ("http", "pot-ui-http-integration.py"),
    ("assistant-start", "pot-ui-assistant-start-browser.py"),
    ("assistant-settings", "pot-ui-assistant-settings-browser.py"),
    ("linked-approvals", "pot-ui-linked-approvals-browser.py"),
    ("composer", "pot-ui-composer-selectors-browser.py"),
    ("usage", "pot-ui-usage-browser.py"),
)


def run_suite():
    started = time.monotonic()
    completed = 0
    status = 0
    try:
        for name, script in CASES:
            print(f"RUN standard HTTP smoke: {name}", flush=True)
            result = subprocess.run(
                [sys.executable, str(ROOT / "scripts/tests" / script)], cwd=ROOT
            )
            if result.returncode:
                status = result.returncode
                print(f"FAIL standard HTTP smoke: {name} (exit {status})", flush=True)
                return status
            completed += 1
        return 0
    finally:
        print(
            json.dumps(
                {
                    "suite": "standard-http",
                    "completed": completed,
                    "total": len(CASES),
                    "seconds": round(time.monotonic() - started, 2),
                    "status": "passed" if completed == len(CASES) else "incomplete",
                }
            ),
            flush=True,
        )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--list", action="store_true", help="Print the complete smoke plan without running it"
    )
    args = parser.parse_args()
    if args.list:
        print(json.dumps(CASES))
        return 0
    return run_suite()


if __name__ == "__main__":
    raise SystemExit(main())
