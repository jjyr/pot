#!/usr/bin/env python3
"""Run the complete standard browser smoke with explicit failure and timing output."""

import argparse
import json
from pathlib import Path
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
CASES = (("http", "pot-ui-http-integration.py"), ("source", "ui-regressions.mjs"))


def browser_environment():
    # Resolve only existing cached tools; never install or change user profiles.
    import os

    env = dict(os.environ)
    if not env.get("POT_PLAYWRIGHT_MODULE"):
        cache = Path(env.get("npm_config_cache", Path.home() / ".npm"))
        modules = list(cache.glob("_npx/*/node_modules/playwright/index.mjs"))
        if len(modules) != 1:
            raise RuntimeError("Set POT_PLAYWRIGHT_MODULE to the existing cached Playwright module")
        env["POT_PLAYWRIGHT_MODULE"] = str(modules[0])
    if not env.get("POT_CHROMIUM_EXECUTABLE") and env.get("PLAYWRIGHT_MCP_CONFIG"):
        config = json.loads(Path(env["PLAYWRIGHT_MCP_CONFIG"]).read_text())
        executable = config.get("browser", {}).get("launchOptions", {}).get("executablePath")
        if executable:
            env["POT_CHROMIUM_EXECUTABLE"] = executable
    return env


def run_suite():
    started = time.monotonic()
    completed = 0
    status = 0
    try:
        for name, script in CASES:
            print(f"RUN standard HTTP smoke: {name}", flush=True)
            result = subprocess.run(
                [
                    "node" if script.endswith(".mjs") else sys.executable,
                    str(ROOT / "scripts/tests" / script),
                ],
                cwd=ROOT,
                env=browser_environment() if script.endswith(".mjs") else None,
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
