# Pot website

The standalone website introduces Pot as an open-source assistant that connects
other agents. A retro selection screen makes Agent Link the visual focus; short
sections explain the assistant philosophy, MCP entry points, model connections,
background work and project status. Security, installation and release history
have their own views. Static HTML, CSS and JavaScript need no product runtime or
Node packages.

## Build and preview

From the repository root:

```sh
python3 website/build-site.py
python3 -m http.server 4173 --bind 127.0.0.1
```

Open `http://127.0.0.1:4173/website/`. The generator validates catalog structure and
registered template text, then writes the committed root and six locale entries.
It also generates the ignored, self-contained `website/pot-preview.html` for
offline viewing. `build-preview.py` runs the same generator.

| Language | Static path | HTML lang |
| --- | --- | --- |
| English | `/en/` | `en` |
| Simplified Chinese | `/zh-CN/` | `zh-CN` |
| Traditional Chinese | `/zh-TW/` | `zh-TW` |
| Japanese | `/ja/` | `ja` |
| Korean | `/ko/` | `ko` |
| Spanish | `/es/` | `es` |

Paths in this table are relative to the deployed website root. Each directory has
its own complete localized body, title, description and alternate-language links.
An ordinary static server can serve direct links and refreshes without rewrites.

The root entry selects saved preference, browser language, then English. Explicit
locale paths stay fixed. Chinese script subtags take precedence over regions:
`zh-Hant`, `zh-TW`, `zh-HK`, `zh-MO` select Traditional Chinese; `zh-Hans` selects
Simplified Chinese. Storage failures do not prevent use. Language switching retains
the current view, reading section and installation channel. Without JavaScript,
all views, installation choices and language links remain readable.

## Sources and behavior

- `templates/page.html`: the single shared template. Edit this instead of generated HTML.
- `i18n/{locale}.json`: complete phrases for SEO, body text, accessibility labels,
  copy feedback and GitHub counts in all six languages. The small `demo` group
  retains language-picker labels. See [website i18n](../docs/internationalization.md#standalone-website).
- `app.js`: progressive view navigation, browser history, installation selection,
  copying, language switching and the optional GitHub count.
- `locale.js`: language normalization and preference handling.
- `style.css`: warm charcoal, cream and amber arcade palette, hard-edged frames,
  responsive layouts, keyboard focus and reduced-motion behavior. Light content
  bands alternate with the dark console surface; the site has no theme switch.
- `install.json`: the existing installation prompt and command source, inserted
  verbatim in all locales. Keep the agent prompt identical to the README and
  installation guide. Copying never executes these commands.
- `build-site.py`: validation, localized pages, the release summaries and offline preview.

The home view has anchors `#agent-link`, `#philosophy`, `#workflow`, `#mcp`, `#models`,
`#assistant` and `#project`. Other views use `#view-security`, `#view-install` (also
`#start`) and `#view-changelog`. All remain in the HTML for progressive enhancement.
JavaScript reveals the targeted view, records navigation in history and restores
it on Back, Forward and refresh. The `install` query parameter retains the selected
channel. Installation tabs also support arrow keys, Home and End.

The homepage's latest version/date and short summary must match the GitHub release
and the source release list. Changelog summaries remain English in every locale,
with links on both version and title to the corresponding official release.
Review availability notes when packages publish; keep full commands and security
explanations in their existing views and linked documentation.

## Agent selection and content

The homepage uses an accessible HTML/CSS 3-by-3 selection screen. Pot sits in
the center; the eight surrounding choices are Codex, Antigravity, DeepSeek, Claude,
Gemini, Qwen, Grok and GLM. Each tile explicitly identifies an Agent or a
model. The selected description explains its role and links to its connection or
model configuration guide. The robot portraits are illustrative characters;
separate, unmodified brand marks identify the actual services.

Selection updates on pointer hover, focus, click or touch. The center Pot swaps
to a matching directional sprite. Arrow keys move spatially between tiles,
skipping the center; Home and End select the first and last choices. Enter and
Space activate the focused choice. A separate guide link opens documentation,
so tapping a tile does not navigate away. Selection persists until another tile
is chosen and survives language switches through the `character` query parameter
(or in-memory state in the offline preview).

With JavaScript enabled the tiles are buttons with pressed state, and only the
selected description is visible and focusable. All panels share a grid cell to
reserve the tallest translated description and avoid layout shifts on selection.
Without JavaScript, tiles remain ordinary anchors to eight visible descriptions.
There is no continuous animation, sound, connection attempt or inference call.
Reduced Motion disables transitions; forced colors keep a selection outline.

The hero explains what Pot is, how background execution and delegation work, and
how results return to the conversation. Three compact rows introduce ongoing
conversations, background work and collaboration. The philosophy remains short.

The portraits and their generation prompts are documented in
[arcade artwork](assets/Arcade-NOTICE.md). `press-start-2p.ttf` is bundled under
[the SIL Open Font License](assets/PressStart2P-OFL.txt). The offline generator
inlines local CSS image/font URLs so the stage remains usable without a server.

The philosophy is two short paragraphs with a link to the author's article.
Simplified and Traditional Chinese use the Chinese article; the other locales
use its English version. The task workflow figure follows an example CSV export request through a directing
assistant, a Codex implementation task and a DeepSeek-backed task for test cases
and usage documentation. The report collects their outputs and a decision for
the user. The MCP figure follows a copy-editing request from Codex, addressed directly to a
Gemini-powered assistant using the Pot plugin's MCP tools and Agent Link. The
receiving assistant edits the copy and replies to Codex through Agent Link.
Pot provides the connection and message transport; no intermediary Pot assistant
interprets the request or creates a second task in this example. Gemini is the
receiver's model identity, not a separate Agent Link adapter. DeepSeek in the
preceding example identifies the model used by a Pot task agent. Sending a request does not mean the
work has completed. Connected agents retain their own execution scope.

Both figures are explicitly illustrative, not live task monitors or product
screenshots. Each uses one quiet background surface. The task example reads
vertically from the goal through Pot, branches into two equal-width workers,
and rejoins at a short delivery summary. Workers stay side by side on mobile.
The messaging example aligns Codex and Gemini on one route with Pot's MCP and
Agent Link as transport labels, then compares original and revised copy below.
The comparison stacks on narrow screens while endpoints remain aligned.

Connector lines are decorative CSS; content and reading order are semantic HTML.
All examples remain readable without JavaScript. No inference, task creation or
message sending takes place on the website. Generous section spacing separates
the examples; their internal spacing keeps related steps together. The stage
selector retains its distinct arcade frames.

Models are separate from Agent integrations. The model grid names model families
and providers, with a link to configuration and a note that availability depends
on the service and configuration. Do not infer an Agent adapter from a model
logo or advertise every model in a provider catalog as inference-qualified.

The former product raster illustrations remain available in `assets/` and their
read-only component fixture remains in `scripts/tests/fixtures/website-product.*`.
The homepage no longer embeds those synthetic examples. The MIT license, author,
release, source and contribution links point to their actual project sources.

## GitHub count and local assets

The header links to the repository; it does not perform a Star action. One
unauthenticated API request may add a count, cached for six hours. Requests omit
credentials and referrers, time out after three seconds, and leave the link
working on failure, rate limiting or unavailable storage. Valid nonnegative safe
integers include zero. Narrow screens truncate compact counts without overstating
them; the full count remains in the accessible label. The offline preview makes
no API request.

The official unmodified GitHub Invertocat SVG is local, with its original 98:96
proportions and an HTML height of 16px. See `assets/GitHub-NOTICE.txt`. Pot artwork
comes from the project. Codex identity in product imagery follows
`assets/Codex-NOTICE.txt`. Existing brand assets and their notices remain available;
unused brand artwork does not imply integrations.

## Browser checks

Serve the checkout root as above. With Playwright available externally:

```sh
node scripts/tests/website-browser.mjs
POT_WEBSITE_BROWSER=webkit node scripts/tests/website-browser.mjs
node scripts/tests/website-star-browser.mjs
```

`POT_WEBSITE_ORIGIN` overrides the origin; `POT_PLAYWRIGHT_MODULE` can point to an
external Playwright module, and `POT_CHROMIUM_EXECUTABLE` can select Chromium.
Run the website checks with both Chromium (the default) and WebKit for iOS layout
coverage. The roster checks require visible portrait bounds and exercise every
choice by touch after opening directly at mobile width, in all six locales.
`--install-only` restricts the first script to installation and shared behaviors.
The full check covers six locales at desktop, tablet and narrow mobile widths, centered selection
geometry, eight directional sprites, stable selection height, workflow reading order,
visible text bounds, spatial keyboard selection, touch selection and guide links, navigation,
language switching, no-JavaScript content and the offline preview. API and
clipboard responses use fixtures; checks never run install commands or agents.

`--capture` also writes review screenshots under `output/pr-images/stage-select/`.
It expects the prior website sources at `output/website-before-stage-select/website/`.
`--capture-only` captures matching before/after views without running the suite.
Promote selected pairs to the branch directory according to
[PR image conventions](../docs/pr-images/README.md) when preparing a PR.

## GitHub Pages

The [Website workflow](../.github/workflows/website.yml) builds and publishes only
on `main` changes to the website/workflow, or a manual run selecting `main`.
PR work does not deploy. In repository Settings → Pages, the source is GitHub
Actions. Private-repository Pages requires an eligible GitHub plan; the published
site is public. The workflow uses the built-in token.

```text
main push / manual run
  -> generate six localized pages
  -> stage public assets and pages only
  -> upload Pages artifact
  -> deploy
```

Only `index.html`, locale directories, styles, scripts and `assets/` are deployed;
templates, catalogs, build scripts and offline preview are excluded. Relative
links work under `/pot/` as well as the custom domain `https://pot.jjylab.com`.
