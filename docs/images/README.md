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

The website's `assets/product-work.png` and `assets/product-agent-link.png` are
copies for its local result gallery. Refresh those copies when these images
change. These are product illustrations using synthetic content, not evidence
that the illustrated installation checks ran on a user's computer.
