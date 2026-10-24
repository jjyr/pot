#!/usr/bin/env python3
"""Verify recent usage bars alongside annual charts with synthetic API data."""

from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import os
from pathlib import Path
import subprocess
import threading
import uuid

ROOT = Path(__file__).resolve().parents[2]


class FixtureServer(ThreadingHTTPServer):
    request_queue_size = 128


def main():
    server = FixtureServer(("127.0.0.1", 0), partial(SimpleHTTPRequestHandler, directory=str(ROOT)))
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
            cli("resize", str(width), str(height))
            for scenario in ["trend", "empty", "complete", "unknown", "partial"]:
                conversation = "&conversation=1" if scenario == "trend" else ""
                url = f"http://127.0.0.1:{server.server_port}/scripts/tests/fixtures/usage-compaction.html?locale={locale}&theme={theme}&case={scenario}{conversation}"
                cli("goto", url)
                if scenario == "trend":
                    images = ROOT / "output/playwright"
                    images.mkdir(parents=True, exist_ok=True)
                    capture = images / f"usage-periods-{width}-{locale}-after.png"
                    cli(
                        "run-code",
                        f"""async page => {{
                        await page.waitForFunction(() => document.querySelector('#result').textContent.startsWith('PASS'));
                        await page.evaluate(() => document.body.classList.add('capture'));
                        await page.screenshot({{path: {str(capture)!r}, fullPage: true}});
                        await page.evaluate(() => document.body.classList.remove('capture'));
                    }}""",
                    )
                cli(
                    "run-code",
                    (ROOT / "scripts/tests/pot-ui-usage-interactions.mjs").read_text(),
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
