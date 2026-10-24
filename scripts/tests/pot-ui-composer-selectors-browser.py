#!/usr/bin/env python3
"""Qualify chat selectors with synthetic directories and receipts in real browsers."""

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


class FixtureHandler(SimpleHTTPRequestHandler):
    def log_message(self, *_):
        pass


def main():
    server = FixtureServer(("127.0.0.1", 0), partial(FixtureHandler, directory=str(ROOT)))
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    session = "pot-selectors-" + uuid.uuid4().hex[:10]
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
        cli("open", "about:blank", "--browser", os.environ.get("POT_COMPOSER_BROWSER", "chrome"))
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
                f"{origin}/scripts/tests/fixtures/composer-selectors.html?conversation=alpha&locale={locale}&theme={theme}&capture=assistants{'&mobile=1' if width < 719 else ''}",
            )
            cli(
                "run-code",
                "async page => { await page.waitForFunction(() => !!window.composerSelectorResults); const result = await page.evaluate(() => window.composerSelectorResults); if (!result.passed) throw Error(JSON.stringify(result)); return result; }",
            )
            cli(
                "screenshot",
                "--filename",
                str(images / f"composer-selectors-{width}-{locale}-after.png"),
            )
            cli(
                "run-code",
                "async page => await page.locator('.composer').screenshot({path: "
                + json.dumps(str(images / f"composer-inline-{width}-{locale}-after.png"))
                + "})",
            )
            cli(
                "run-code",
                "async page => { await page.locator('.composer-selector-trigger[data-trigger=\"/\"]').click(); await page.getByRole('option', {name:'code-review 检查代码质量、潜在问题与测试覆盖'}).waitFor(); }",
            )
            cli(
                "screenshot",
                "--filename",
                str(images / f"composer-skills-{width}-{locale}-after.png"),
            )
            print(
                f"Composer selector browser: {width}x{height}, {locale}, {theme} passed", flush=True
            )
        (ROOT / "output/pr-images/mobile-enter").mkdir(parents=True, exist_ok=True)
        enter_checks = (ROOT / "scripts/tests/pot-ui-mobile-enter-browser.js").read_text()
        cli(
            "run-code",
            "async page => { return await ("
            + enter_checks
            + ")(page, {base: "
            + json.dumps(origin + "/web/src")
            + "}); }",
        )
        print("Desktop/mobile Enter, IME, main/task composer regression checks passed", flush=True)
        native_checks = (ROOT / "scripts/tests/pot-ui-composer-mentions-browser.js").read_text()
        cli("run-code", native_checks)
        print(
            "Native mention typing, clipboard, undo/redo, deletion and scrolling passed", flush=True
        )
    finally:
        cli("close")
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


if __name__ == "__main__":
    main()
