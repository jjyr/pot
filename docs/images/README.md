# Product screenshots

`conversation-tasks.png` and `agent-link.png` capture the real shared product UI
from `web/src`, at 1280 × 900 in the light theme. Both use isolated synthetic
project records, rather than user conversations or live agent execution. They
contain no user credentials, real machine endpoints or local absolute paths.
The Pot artwork belongs to this repository; the Codex identity follows the
[website asset notice](../../website/assets/Codex-NOTICE.txt).

To refresh them, serve the checkout locally and open
`scripts/tests/fixtures/product-overview.html?view=work` and `?view=link` in fresh
browser contexts. The fixture mounts the production shared UI with a read-only
client and rejects mutation attempts. Wait for the conversation and background
task strip to settle before capturing the viewport. Keep the same viewport and
theme for both images; do not add screenshot-only styling to the product.

The linked Development assistant includes a saved default avatar, as a real
Agent Link directory does. Its fixture PNG is generated with
`pkg/assistantavatar.ForName` and `Render`, using the synthetic assistant ID
`build`; the shared UI renders that profile image without screenshot overlays.
To regenerate the avatar and capture both views from the running local server:

```sh
mise exec -- go run scripts/tests/fixtures/product-overview-avatar.go
node scripts/tests/product-overview-browser.mjs
```

The capture script accepts `POT_PLAYWRIGHT_MODULE` and `POT_WEBSITE_ORIGIN`, like
the website browser checks. It verifies the loaded Development avatar, task
counts and read-only fixture before writing the two images and website copies.

The same capture script writes website assets: a focused conversation crop
(`product-work.png`), an assistant-list crop (`product-assistants.png`), a 390px
conversation capture (`product-work-mobile.png`), and the Agent Link copy
(`product-agent-link.png`). Refresh these alongside the full product images.
The website links each figure to its full image, with a native picture source
for the mobile conversation. These are product illustrations using synthetic content, not evidence
that the illustrated installation checks ran on a user's computer.
