#!/usr/bin/env python3
"""Qualify shared Assistant settings DOM with synthetic state in real browsers."""

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
    session = "pot-settings-" + uuid.uuid4().hex[:10]
    env = {**os.environ, "npm_config_offline": "true", "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD": "1"}
    prefix = [
        "npx",
        "--offline",
        "--yes",
        "--package",
        "@playwright/cli",
        "playwright-cli",
        "--session",
        session,
    ]

    def cli(*args):
        result = subprocess.run(
            [*prefix, *args], env=env, text=True, capture_output=True, timeout=90
        )
        output = result.stdout.split("### Ran Playwright code", 1)[0]
        if result.returncode or "### Error" in output:
            raise RuntimeError(output[:3000] + result.stderr[:1000])
        return output

    try:
        origin = f"http://127.0.0.1:{server.server_port}"
        cli("open", "about:blank")
        images = ROOT / "output/playwright"
        images.mkdir(parents=True, exist_ok=True)
        for width, height, locale, theme in [
            (1200, 820, "en", "light"),
            (1200, 820, "zh-CN", "dark"),
            (390, 844, "en", "light"),
            (390, 844, "zh-CN", "dark"),
        ]:
            cli("resize", str(width), str(height))
            cli(
                "goto",
                f"{origin}/scripts/tests/fixtures/assistant-settings.html?locale={locale}&theme={theme}&capture=1",
            )
            cli(
                "run-code",
                "async page => { await page.waitForFunction(() => !!window.assistantSettingsResults); const result = await page.evaluate(() => window.assistantSettingsResults); if (!result.passed) throw Error(JSON.stringify(result)); return result; }",
            )
            cli(
                "screenshot",
                "--filename",
                str(images / f"assistant-settings-{width}-{locale}-after.png"),
            )
            print(
                f"Assistant settings browser: {width}x{height}, {locale}, {theme} passed",
                flush=True,
            )
    finally:
        cli("close")
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


if __name__ == "__main__":
    main()
