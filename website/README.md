# Pot website

A standalone product website with equal emphasis on assistant tasks and Agent Link,
including the verified Codex integration, a website-design conversation and four installation paths. It uses static HTML, CSS and JavaScript;
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
workflow step, communication disclosure state and open result preview. Static pages
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
- `install.json` contains the single English agent prompt, same-repository Homebrew tap/install commands, image address and packaged server command; the generator inserts these without translation. Keep the prompt identical to the README and installation guide.
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

## Assistant section

Section 01 has two parts: a full product screenshot followed by a local
website-design conversation with Pot and Designer Tony. It has no playback,
stage navigation or repeated detail gallery. User messages have no identity row;
assistant messages keep their name and avatar. Tony uses a generic avatar.
Native `details` provide the communication disclosures and nested call details;
one short incoming message starts expanded, as in the final composition.
Native dialogs provide the original image and local design-result preview,
including Escape dismissal and focus restoration. These controls never send
messages, create tasks or invoke models.

The desktop image stays at 1280 CSS pixels and the mobile image at 390 CSS
pixels. At 720px and below the mobile source is shown; a keyboard-focusable image
region contains horizontal scrolling when necessary. Do not shrink text inside
these screenshots to fit the page, sharpen or redraw it. Keep screenshot copy
verbatim; the surrounding labels, story and dialogs use all six catalogs.
The offline builder embeds the same assets. Language switching preserves the
communication disclosures and open preview, alongside the other section states.

| Asset | Original source and size | SHA-256 |
| --- | --- | --- |
| `assets/assistant-desktop.png` | [PR #259, full desktop image](https://github.com/jjyr/pot/blob/7217ccd2d328e2837b29093fc6e42f31167fdba8/docs/pr-images/codex-warm-chat-bubbles/chat-rich-desktop-light-after.png), 1280 × 1400 | `79379d50a9f20b952d30a914880ab2247bbcf5f9101ac0d923f4a56a23c6dc4f` |
| `assets/assistant-mobile.png` | [PR #232, full phone image](https://github.com/jjyr/pot/blob/8702c69242eb3b8d7b92a3fbf3a82ce4bfc033c4/docs/pr-images/codex-task-a12b413b-70e6-4690-ab0d-d15ba64a6658/chat-mobile-light-receive-after.png), 390 × 1000 | `bd2b505bbe1dab928b0c29d7653b7568ed7981c7bd5d282115749e520e5ce545` |

These small lossless PNGs preserve the original bytes and full framing. They show
actual product code rendering isolated synthetic test records, not live customer
conversations, delivery receipts or proof of task completion. The Code reviewer
identity in the desktop image is unchanged. The Pot/Tony website story below it
is an HTML layout and interaction illustration, not a screenshot or a record of
actual communication. Keep this distinction in short visible labels and image
alternatives when refreshing the assets. Update the source links and hashes here
when replacing either image; do not reuse PR review captures as product assets.

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
Web UI as four parallel choices. During the unsigned release phase, macOS exposes
only the same-repository Homebrew tap/install commands and its installation/first-launch guide;
it has no App download button or generic Release link. Homebrew still downloads
the release App ZIP and preserves quarantine/Gatekeeper handling. Linux
contains only the Experimental native `.deb` desktop. Web UI groups direct server
release downloads with the Docker
[container package channel](https://github.com/users/jjyr/packages/container/package/pot),
both accessed through a browser. macOS is selected by
default; keyboard users can switch with arrows, Home and End. Without JavaScript,
all four sections remain visible. Language switches preserve the selected channel.
Linux desktop and Web UI server download buttons open the official Releases
selector, since the first packages are pending. Homebrew uses root `Casks/pot.rb` in this repository. The first tap includes
`https://github.com/jjyr/pot.git` explicitly; installation becomes available after
the first macOS release and its cask update PR are merged.
The linked installation guide explains macOS signing status,
application-specific Gatekeeper allowances and Homebrew quarantine. Keep those
instructions in [Desktop installation](../docs/desktop-installation.md#macos);
release notes and `MACOS-RELEASE.txt` disclose each version's selected mode.
The container address has no invented version or `latest` tag; its
package page awaits first publication. Platform requirements follow the
[release contract](../docs/releases.md). Keep these small availability notes in
sync when the channels publish. The English agent prompt is
intentionally identical in every locale. No installation or agent execution is
performed by the website. The website-design preview is a local layout illustration; the product images
use isolated synthetic records rendered by real shared UI code.
See [screenshot provenance](../docs/images/README.md).

The header's GitHub Invertocat links to the repository. It uses the unmodified
black SVG from the [GitHub Brand Toolkit](https://brand.github.com/foundations/logo),
served locally with its original 98:96 proportions. The image has an HTML height
of 16 pixels and an automatic width, so it stays compact even before the stylesheet
loads or when a cached stylesheet predates the image selector. Its dedicated CSS
class preserves that size; the link provides a 44-pixel minimum touch height.
Source and trademark terms
are recorded in `assets/GitHub-NOTICE.txt`. On static pages a single
unauthenticated request to `https://api.github.com/repos/jjyr/pot` can add the
public star count, cached in localStorage for six hours. Requests omit credentials
and referrers and time out after three seconds. Failed requests, rate limits and invalid counts retain the working link without
a count. If storage is unavailable, a successful network response can still show
the count.
Valid counts are nonnegative safe integers, including a genuine zero. Only known
counts show the separate bubble; loading or failure keeps the Star entry usable.
The warm-white 44px segments form one link and one keyboard focus target, including
the gap and the bubble's left-pointing tip. The official icon has an HTML height
of 16px as well as scoped CSS sizing, keeping it small before styles load. Desktop
counts use locale grouping; at 580px and below a truncated compact value is shown
(with k/m/b/t/q units), while the full count stays in the link label and title.
There is no server, token or build-time network dependency; the offline preview
makes no star-count request. Clicking the link does not perform a Star action.

## Brand image sources

Images are stored locally, preserving brand colors and proportions. Icons beside
brand text use empty `alt` text and hidden decorative containers to avoid duplicate
screen reader announcements.

| Brand | Local file | Source |
| --- | --- | --- |
| Pot | `assets/pot.png` | Existing Pot release PotCompanion character. |
| GitHub | `assets/github-invertocat-black.svg` | Unmodified `GitHub Logos/SVG/GitHub_Invertocat_Black.svg` from the [official logo download](https://brand.github.com/GitHub_Logos.zip), linked by the [GitHub Brand Toolkit](https://brand.github.com/foundations/logo) (the destination of `https://github.com/logos`). See `assets/GitHub-NOTICE.txt`. |
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
These checks cover six-language copy behavior, the assistant story and its nested
disclosures, original-size images, local result previews, 320/390px layouts,
keyboard controls, collaboration and Star fallbacks.
They use synthetic clipboard/API responses and never execute installation commands.

Use `node scripts/tests/website-browser.mjs --install-only` to check only installation
classification, links, copy behavior, keyboard navigation, responsive layouts and
static/offline language state without running the assistant-story and Star scenarios.

Run `node scripts/tests/website-star-browser.mjs` for the focused Star layout,
API fallback, cache and input checks. It uses the same browser environment
variables as the website checks. Set `POT_WEBSITE_OLD_CSS` to a stylesheet file
to also check new HTML against a previous stylesheet.
