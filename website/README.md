# Pot website

A standalone product website with equal emphasis on assistant tasks and Agent Link,
including the verified Codex integration. It uses static HTML, CSS and JavaScript;
Python's standard library generates all six language entries and an offline preview.
No product runtime, Node dependencies or build framework are required.

## Build and preview

From the repository root:

```sh
python3 website/build-site.py
python3 -m http.server 4173 --directory website --bind 127.0.0.1
```

Open `http://127.0.0.1:4173/`. The root entry uses the saved language preference,
then the browser language, then English. Explicit language paths keep their locale
regardless of saved or browser preferences.

| Language | Static path | HTML lang |
| --- | --- | --- |
| English | `/en/` | `en` |
| Simplified Chinese | `/zh-CN/` | `zh-CN` |
| Traditional Chinese | `/zh-TW/` | `zh-TW` |
| Japanese | `/ja/` | `ja` |
| Korean | `/ko/` | `ko` |
| Spanish | `/es/` | `es` |

Each entry is a directory with its own `index.html`, complete localized body,
title, description, `lang` and relative `hreflang` / `x-default` links. Direct
navigation and refresh work on an ordinary static server without rewrites. With
JavaScript disabled, the localized body and language links remain available.

Language switching preserves the current section, agent, direct/delegated mode,
task example, playback stage, workflow step and open result preview. Static pages
carry demo state in query parameters; the offline preview switches in place.
Chinese script subtags take precedence over region subtags: `zh-Hant`, `zh-TW`,
`zh-HK` and `zh-MO` select Traditional Chinese; `zh-Hans` selects Simplified Chinese.
Storage failures do not prevent use.

## Sources and generated files

- `templates/page.html` is the single page template. Edit it rather than generated HTML.
- `i18n/{locale}.json` is the website's shared catalog for static and dynamic text,
  SEO and accessibility labels in all six languages. The `page` section uses source
  phrases as keys; `demo` contains dialogue, task states and example reports. These
  catalogs are independent of the two-language application catalog; see the
  [internationalization rules](../docs/internationalization.md#standalone-website).
- `build-site.py` validates catalog structure and registered template text, then
  generates `index.html`, the six localized pages and `pot-preview.html`.
  `build-preview.py` runs the same generator.
- `app.js` provides shared interactions and localized report previews/downloads.
- `locale.js` normalizes languages, saves preferences and detects language only
  at the root entry.
- `style.css` provides responsive layout, keyboard focus and reduced-motion support.
- `assets/` contains local brand images and the Codex provenance notice.

The root and localized HTML files are committed so `website/` is ready for a static
server. Regenerate them whenever a source changes. The larger self-contained
`pot-preview.html` is generated locally and ignored by Git; it embeds all six
pages, catalogs, styles, scripts and brand images, supports offline language
switching and requires no network subresources.

## GitHub Pages

The [Website workflow](../.github/workflows/website.yml) builds and publishes the
static site to GitHub Pages when `website/**` or the workflow changes on `main`.
It also supports manual runs from the Actions tab; select `main`, since other
branches are skipped. Website deployment runs independently of product releases.

In the repository's **Settings > Pages**, set **Build and deployment > Source**
to **GitHub Actions** before the first deployment. The private `jjyr/pot`
repository requires a GitHub plan supporting Pages for private repositories,
such as GitHub Pro. The published website is publicly accessible; the repository
can remain private. The workflow uses the built-in `GITHUB_TOKEN` and does not
require a personal access token.

```text
main push / manual run
  -> generate six localized pages
  -> stage public files only
  -> upload Pages artifact
  -> deploy to https://jjyr.github.io/pot/
```

The deployment includes only `index.html`, `style.css`, `app.js`, `locale.js`,
`assets/` and the six locale directories. Templates, catalogs, build scripts,
the README and the offline preview are excluded. Relative resource and language
links work under the `/pot/` project path; for example, Simplified Chinese is
available at `https://jjyr.github.io/pot/zh-CN/`. No custom domain is configured.

## Product claims and links

The Pot/Codex connection, conversation and delegation capabilities are verified.
Claude, Grok bot and Devin are explicitly labeled ecosystem concepts, rather than
released native adapters. Nodes retain their own tools, credentials and approvals.
All website tasks, conversations, scheduled work and reports are local examples;
they do not call real agents or contain real connection information.

The repository is `https://github.com/jjyr/pot`. Its documentation index is
[`docs/README.md`](../docs/README.md), and Codex setup is documented in
[`integrations/codex/README.md`](../integrations/codex/README.md). The repository is
currently private, so these are contributor references rather than public install
or documentation links. The website retains its in-page guidance until public
entry points are available; it does not invent download commands.

## Brand image sources

Images are stored locally, preserving brand colors and proportions. Icons beside
brand text use empty `alt` text and hidden decorative containers to avoid duplicate
screen reader announcements.

| Brand | Local file | Source |
| --- | --- | --- |
| Pot | `assets/pot.png` | Existing Pot release PotCompanion character. |
| Codex | `assets/codex.png` | Existing product asset from the official ChatGPT.app distribution's `icon-codex-light.png`; see `assets/Codex-NOTICE.txt`. Product identity: [OpenAI Codex](https://openai.com/codex/). |
| Claude | `assets/claude.svg` | [Official Claude favicon](https://claude.ai/favicon.svg). |
| Grok | `assets/grok.svg` | [Official Grok favicon](https://grok.com/images/favicon.svg), identified by the [Grok website](https://grok.com/). Only an empty exported foreignObject blur layer was removed; brand paths, proportions and colors are retained. |
| Devin | `assets/devin.png` | [Official Devin PWA icon](https://app.devin.ai/assets/pwa/pwa-icon-192.png), identified by the [Devin application](https://app.devin.ai/) apple-touch-icon declaration. |

Trademarks and images belong to their respective owners. Their use identifies
connection targets and does not imply endorsement or additional integration support.
