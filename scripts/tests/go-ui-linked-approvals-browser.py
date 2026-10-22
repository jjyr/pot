#!/usr/bin/env python3
"""Exercise production approval UI with synthetic data in real browsers."""

from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
import os
from pathlib import Path
import subprocess
import threading
import uuid

ROOT = Path(__file__).resolve().parents[2]


def main():
    class Handler(SimpleHTTPRequestHandler):
        def log_message(self, *args):
            pass

    ThreadingHTTPServer.request_queue_size = 128
    server = ThreadingHTTPServer(("127.0.0.1", 0), partial(Handler, directory=str(ROOT)))
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    session = "pot-review-" + uuid.uuid4().hex[:10]
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
    env = {**os.environ, "npm_config_offline": "true", "PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD": "1"}

    def cli(*args):
        result = subprocess.run(
            [*prefix, *args], env=env, text=True, capture_output=True, timeout=60
        )
        output = result.stdout.split("### Ran Playwright code", 1)[0]
        if result.returncode or "### Error" in output:
            raise RuntimeError(output[:3000] + result.stderr[:1000])
        return output

    try:
        cli("open", "about:blank")
        images = ROOT / "output/playwright"
        images.mkdir(parents=True, exist_ok=True)
        for width, locale in [(1200, "en"), (390, "zh-CN")]:
            cli("resize", str(width), "844")
            cli(
                "goto",
                f"http://127.0.0.1:{server.server_port}/scripts/tests/fixtures/linked-approvals.html?assistant_id=main&locale={locale}&theme=light",
            )
            cli(
                "run-code",
                """async page => {
              await page.locator('.approval-card summary').waitFor();
              await page.locator('.approval-card summary').click();
              if(await page.locator('.approval-actions button').count()!==2)throw Error('Approval is broader than one operation');
              if(!(await page.locator('.approval-arguments').first().innerText()).includes('ssh deployment-host hostname'))throw Error('Missing exact command');
            }""",
            )
            cli("screenshot", "--filename", str(images / f"linked-approval-{width}-{locale}.png"))
            cli(
                "run-code",
                """async page => {
              await page.evaluate(()=>window.linkedApprovalFixture.language('en'));
              await page.getByRole('button',{name:'Deny',exact:true}).click();
              await page.locator('.chat-approvals').waitFor({state:'hidden'});
              if(await page.locator('.approval-actions button').count())throw Error('Stale decision remains');
              await page.evaluate(()=>window.linkedApprovalFixture.reset({allowed:false}));
              await page.locator('.approval-card summary').click();
              if(!await page.getByRole('button',{name:'Allow once',exact:true}).isDisabled())throw Error('Out-of-scope operation can approve');
              await page.evaluate(()=>window.linkedApprovalFixture.reset());
              await page.getByRole('button',{name:'Allow once',exact:true}).click();
              await page.waitForFunction(()=>!window.linkedApprovalFixture.pending);
              const state=await page.evaluate(()=>({calls:window.linkedApprovalFixture.calls,errors:window.linkedApprovalFixture.errors}));
              if(JSON.stringify(state.calls.map(call=>call.action))!==JSON.stringify(['deny','allow_once'])||state.errors.length)throw Error(JSON.stringify(state));
              return state;
            }""",
            )
            print(
                f"Linked approvals: {width}, {locale}, live locale, resolved request removal, owner scope and one-shot approval passed",
                flush=True,
            )
    finally:
        cli("close")
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


if __name__ == "__main__":
    main()
