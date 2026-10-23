# Pot website

A standalone product website with equal emphasis on assistant tasks and Agent Link,
including the verified Codex integration, a parallel-task example and four installation paths. It uses static HTML, CSS and JavaScript;
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
playback stage, workflow step and open result preview. Static pages
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
- `install.json` contains the single English agent prompt, local Homebrew cask command, image address and packaged server command; the generator inserts these without translation. Keep the prompt identical to the README and installation guide.
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
available at `https://jjyr.github.io/pot/zh-CN/`. The production website is available at `https://pot.jjylab.com`.

## Product claims and links

The Pot/Codex connection, conversation and delegation capabilities are verified.
Claude, Grok bot and Devin are explicitly labeled ecosystem concepts, rather than
released native adapters. Nodes retain their own tools, credentials and approvals.
All website tasks, conversations, scheduled work and reports are local examples;
they do not call real agents or contain real connection information.

The repository is `https://github.com/jjyr/pot`. The installation section links
its [agent installation guide](../docs/install-with-agent.md), macOS, Linux and
Web UI as four parallel choices. macOS includes App downloads and Homebrew; Linux
contains only the Experimental native `.deb` desktop. Web UI groups direct server
release downloads with the Docker
[container package channel](https://github.com/users/jjyr/packages/container/package/pot),
both accessed through a browser. macOS is selected by
default; keyboard users can switch with arrows, Home and End. Without JavaScript,
all four sections remain visible. Language switches preserve the selected channel.
Download buttons open the official Releases selector, since the first packages
are pending. Homebrew uses the release's local `pot.rb` cask, not an unpublished
tap formula. The container address has no invented version or `latest` tag; its
package page awaits first publication. Platform requirements follow the
[release contract](../docs/releases.md). Keep these small availability notes in
sync when the channels publish. The English agent prompt is
intentionally identical in every locale. No installation or agent execution is
performed by the website. Result previews use synthetic notes, a documentation
change excerpt and real shared-UI screenshots with isolated synthetic records.
See [screenshot provenance](../docs/images/README.md).

The header's inline GitHub Mark links to the repository. It reuses the existing
product icon from [Primer Octicons](https://github.com/primer/octicons/blob/main/icons/mark-github-16.svg)
under the MIT license; the notice is included in `assets/Octicons-LICENSE.txt`. On static pages a single
unauthenticated request to `https://api.github.com/repos/jjyr/pot` can add the
public star count, cached in localStorage for six hours. Requests omit credentials
and referrers and time out after three seconds. Failed requests, rate limits,
unusable storage and invalid/zero counts retain the working link without a count.
There is no server, token or build-time network dependency; the offline preview
makes no star-count request. Clicking the link does not perform a Star action.

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

## Browser checks

After building, serve the **checkout root** (the product screenshot fixture imports
`web/src`), for example `python3 -m http.server 4173 --bind 127.0.0.1`.
With Playwright and Chromium available outside the website runtime, run
`node scripts/tests/website-browser.mjs`. `POT_WEBSITE_ORIGIN` overrides the server
origin; `POT_PLAYWRIGHT_MODULE` can point to an external Playwright module.
These checks cover six-language copy behavior, parallel playback and its terminal
state, deliverable previews, narrow layouts, keyboard controls and Star fallbacks.
They use synthetic clipboard/API responses and never execute installation commands.

Use `node scripts/tests/website-browser.mjs --install-only` to check only installation
classification, links, copy behavior, keyboard navigation, responsive layouts and
static/offline language state without running the task-demo and Star scenarios.
