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
counts and the read-only fixture before writing the two full-app images.

The website uses a separate component-composition fixture for its homepage
illustrations. Run `node scripts/tests/product-overview-browser.mjs --website-only`
to refresh those assets without changing the full-app documentation images.
It uses production replies, resource previews, assistant identities and Task rows
with authoritative synthetic catalogue addresses. See the
[website illustration contract](../../website/README.md#product-illustrations).
These are behavior illustrations, not evidence that installation checks ran on a
user's computer.
