# Pot website

The standalone website reads as a short project handbook: why Pot exists, its
choices around ongoing conversations and background work, and an overview of the
open-source project. Security, installation and release history have their own
views. Static HTML, CSS and JavaScript need no product runtime or Node packages.

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
- `style.css`: warm paper palette, readable type, responsive layouts, keyboard
  focus and reduced-motion behavior. The website intentionally uses one light palette.
- `install.json`: the existing installation prompt and command source, inserted
  verbatim in all locales. Keep the agent prompt identical to the README and
  installation guide. Copying never executes these commands.
- `build-site.py`: validation, localized pages, the release summaries and offline preview.

The home view has anchors `#philosophy`, `#assistant`, `#assistants`, `#agent-link`,
`#models` and `#project`. Other views use `#view-security`, `#view-install` (also
`#start`) and `#view-changelog`. All remain in the HTML for progressive enhancement.
JavaScript reveals the targeted view, records navigation in history and restores
it on Back, Forward and refresh. The `install` query parameter retains the selected
channel. Installation tabs also support arrow keys, Home and End.

The homepage's latest version/date and short summary must match the GitHub release
and the source release list. Changelog summaries remain English in every locale,
with links on both version and title to the corresponding official release.
Review availability notes when packages publish; keep full commands and security
explanations in their existing views and linked documentation.

## Product illustrations

`assets/product-work.png` and `assets/product-assistants.png` are focused crops of
the real shared product UI, using the repository's read-only synthetic fixture.
`product-work-mobile.png` uses the same fixture in a 390px viewport, selected by a
native `picture` source on narrow screens. The assistant-list crop can scroll
horizontally on phones to preserve legible text. Both figures link to the full
image without requiring JavaScript. English fixture conversations remain verbatim;
localized captions explain them and identify synthetic data.

The screenshots show assistant avatars, the shared `icon('tasks')`, confirmed
four-character task identities, state and elapsed time. They do not show user
business data, credentials or real agent execution. Follow
[product screenshot instructions](../docs/images/README.md) to refresh them.

Agent Link links to the current Pot, Codex and Antigravity documentation. A model
provider connection (including xAI) is separate from an Agent adapter; do not
infer adapter support from a model logo. Connected environments retain their own
permissions, including the Antigravity headless approval limitation described in
its guide. The MIT license and author links point to the actual project sources.

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
node scripts/tests/website-star-browser.mjs
```

`POT_WEBSITE_ORIGIN` overrides the origin; `POT_PLAYWRIGHT_MODULE` can point to an
external Playwright module, and `POT_CHROMIUM_EXECUTABLE` can select Chromium.
`--install-only` restricts the first script to installation and shared behaviors.
`--capture` also writes review screenshots under
`docs/pr-images/redesign-website/`; it expects the previous PR website sources at
`output/website-before/website/`. Review captures use matching viewport and locale.
`--capture-only` refreshes those images without repeating interaction checks.
The checks use synthetic clipboard and API responses, and never run installation
commands or invoke agents.

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
