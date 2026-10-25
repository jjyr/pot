#!/usr/bin/env python3
"""Verify recent usage bars alongside annual charts with synthetic API data."""

import argparse
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import subprocess
import threading
import uuid

ROOT = Path(__file__).resolve().parents[2]


class FixtureServer(ThreadingHTTPServer):
    request_queue_size = 128


class QuietHandler(SimpleHTTPRequestHandler):
    def log_message(self, *_args):
        pass


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--case", choices=["trend", "empty", "complete", "unknown", "partial"])
    parser.add_argument("--width", type=int, choices=[1200, 390, 320])
    args = parser.parse_args()
    server = FixtureServer(("127.0.0.1", 0), partial(QuietHandler, directory=str(ROOT)))
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    prefix = [
        "npx",
        "--offline",
        "--yes",
        "--package",
        "@playwright/cli",
        "playwright-cli",
        "--session",
        "pot-usage-" + uuid.uuid4().hex[:10],
    ]
    env = {**os.environ, "npm_config_offline": "true", "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD": "1"}

    def cli(*args):
        result = subprocess.run(
            [*prefix, *args], env=env, text=True, capture_output=True, timeout=90
        )
        output = result.stdout.split("### Ran Playwright code", 1)[0]
        if result.returncode or "### Error" in output:
            raise RuntimeError(output[:3000] + result.stderr[:1000])
        return output

    try:
        cli("open", "about:blank")
        for width, height, locale, theme in [
            (1200, 900, "en", "light"),
            (1200, 900, "zh-CN", "dark"),
            (390, 844, "en", "dark"),
            (320, 844, "zh-CN", "light"),
        ]:
            if args.width and width != args.width:
                continue
            for scenario in (
                [args.case] if args.case else ["trend", "empty", "complete", "unknown", "partial"]
            ):
                conversation = "&conversation=1" if scenario == "trend" else ""
                url = f"http://127.0.0.1:{server.server_port}/scripts/tests/fixtures/usage-compaction.html?locale={locale}&theme={theme}&case={scenario}{conversation}"
                print(
                    f"Usage scenario: {width}x{height}, {locale}, {theme}, {scenario}", flush=True
                )
                capture_code = ""
                if scenario == "trend":
                    images = ROOT / "output/playwright"
                    images.mkdir(parents=True, exist_ok=True)
                    capture = images / f"usage-periods-{width}-{locale}-after.png"
                    capture_code = (
                        "await page.waitForFunction(() => /^(PASS|FAIL)/.test(document.querySelector('#result').textContent));"
                        "const result = await page.locator('#result').textContent();"
                        "if (!result.startsWith('PASS')) throw Error(result);"
                        "await page.evaluate(() => document.body.classList.add('capture'));"
                        + "await page.screenshot({path:"
                        + json.dumps(str(capture))
                        + ",fullPage:true});"
                        + "await page.evaluate(() => document.body.classList.remove('capture'));"
                    )
                interactions = (ROOT / "scripts/tests/pot-ui-usage-interactions.mjs").read_text()
                cli(
                    "run-code",
                    "async parent => {const context=await parent.context().browser().newContext("
                    + json.dumps(
                        {
                            "viewport": {"width": width, "height": height},
                            "locale": "en-US",
                            "serviceWorkers": "block",
                        }
                    )
                    + ");try{const page=await context.newPage();await page.goto("
                    + json.dumps(url)
                    + ");"
                    + capture_code
                    + "return await ("
                    + interactions
                    + ")(page);"
                    + "}finally{await context.close();}}",
                )
                print(
                    f"Usage browser: {width}x{height}, {locale}, {theme}, {scenario} passed",
                    flush=True,
                )
    finally:
        cli("close")
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


if __name__ == "__main__":
    main()
