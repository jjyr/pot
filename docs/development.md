# Development guide

## Toolchains and workflow

`mise.toml` pins Go, Node, Python and source checks. macOS App development requires
Apple Silicon, macOS 26+ and selected Xcode. Headless Go packages build on Linux
without Wails or platform desktop libraries.

```sh
mise install
mise run setup
mise run prepare
mise run check
mise run test
mise run build
mise run open
```

`prepare` supplies pinned Node, tool plugins, Skills and license assets.
`build`/`bundle` create `.local/pot/PotPreview.app`, an independent development
bundle identified as `dev.pot.preview`. Release packaging uses the production identity. Do not replace a running product's files implicitly.

The macOS builder downloads the pinned Wails module before applying its
checksum-verified patches to a disposable copy. A fresh module cache is supported;
the original cache and repository module files remain unchanged.

## Builds and packaging

| Command | Output |
|---|---|
| `mise run build:go` | CLI under `.local/pot/bin` |
| `mise run build:linux` | CGO-free Linux amd64/arm64 CLI and tool packages |
| `mise run build:cli` | Target CLI archive with Node, tools and Skills |
| `mise run build` | Complete Wails macOS bundle |
| `mise run build:web-ui` | Embedded shared HTML/CSS/JS frontend |
| `mise run build:plugins` | Tool manifests and declared sources |
| `mise run release -- VERSION` | App/CLI archives, checksums, cask and configured signed update feed; publishing is separate |
| `mise run verify:release -- VERSION` | On-demand public version, signed feed and download verification; requires a trusted pinned key; [arguments and reports](releases.md#public-verification-after-publication) |

`scripts/build-pot-app`, `scripts/build-pot-cli` and `scripts/pot_packaging.py` own
layout and checksums. Standalone and embedded Runtime locate trusted tool assets
independently of workspace preparation.

Local packaging defaults to development version `0.0.0`, matching CI builds from
unversioned refs. Set `POT_APP_VERSION=MAJOR.MINOR.PATCH` to build a specific version;
`scripts/release VERSION` and official CI set it explicitly. The CLI, App bundle,
package metadata and container labels use that version. Plain `go build` without
the packaging linker flags reports `development`.

For cross-platform tool packages:

```sh
mise exec -- python scripts/build-pot-plugins --platform linux-amd64 \
  --output /absolute/plugin-packages
```

Stop Runtime before replacing complete installed packages. Node plugins use the
bundled Node and put its `bin` directory first on PATH for plugin child processes.
Ordinary command shells keep their own PATH and project toolchain; Pot does not
inject its bundled Node directory. Packaging preserves the distribution's npm/npx
symlinks so their relative modules remain usable after
relocation. Project commands select installed versions through their own toolchain
manager, such as `mise exec`. Browser tools also need Chrome/Chromium on the
execution machine.
Goja compositions run in a bounded process worker. Goja supplies no independent
per-runtime hard heap limit. See [Go libraries](go-libraries.md).

Signed self-update releases require the release key configuration in [Updates](updates.md).
Automatic updates require matching non-SQL `selfupdate.StorageFormat` markers,
update protocol and signed append-only migration lineage. Pending migrations
commit as one batch per database; committed schema changes require forward
recovery if readiness fails. No automatic database backup or Down migration is
performed. Non-SQL persisted-format changes must change the marker. See [Updates](updates.md).

## Checks and isolated previews

For native image preview checks, serve the repository on loopback and open
`/scripts/tests/fixtures/image-viewer.html`. It mounts the real saved-resource,
uploaded-attachment and Markdown image components with synthetic originals and
no backend writes. Use **Run component checks** for its in-page assertions.
`scripts/tests/pot-ui-image-viewer-capture.mjs` accepts the origin, an optional
before-tree URL prefix and an output directory. With the existing Playwright
module and Chromium configured through `POT_PLAYWRIGHT_MODULE` and
`POT_CHROMIUM_EXECUTABLE`, it exercises native mouse/wheel/keyboard input at
1280, 390 and 320 pixels in both locales/themes and exports paired screenshots.
Touch scrolling and native desktop WebViews still require device testing.

```sh
mise run lint
mise run check:go
mise run check:push
mise run test
mise run test:executor
mise run test:sandbox
mise run test:sandbox-linux
mise run test:release
mise run test:i18n
mise run licenses
mise run build:linux
```

`mise run test:pwa` runs the shared notification/source/transport contracts and
an isolated Chromium Service Worker scenario. The browser runner uses an existing
Playwright installation (`POT_PLAYWRIGHT_MODULE` may name its module) and optional
`POT_CHROME` executable. Its temporary origin/profile, fake PushManager subscription
and synthetic push messages never contact a user device or provider. The real
Service Worker displays the synthetic notification and exercises click recovery;
this does not establish physical iOS background/lock-screen behavior.

`test:i18n` checks shared catalog key parity, nonempty translations and matching
ordered `{parameter}` markers. It does not scan for hardcoded UI text or verify
runtime language changes; follow the [i18n rules](internationalization.md) for
those checks.

`test:sandbox` runs real file, socket, network, terminal, plugin and cleanup tests with the pinned SDK. `test:sandbox-linux` uses a disposable non-root Docker host with namespace restrictions relaxed only on the outer test container; only test assets are mounted read-only.

`check:go` verifies formatting, vet and architectural imports. `test:go` runs all
Go tests without race instrumentation. `test` runs the normal and focused race
suites independently, alongside UI/packaging/API checks. `test:race` no longer
implicitly repeats the normal suite. Both paths use a configurable 20-minute
per-package timeout (`POT_TEST_TIMEOUT`, or `scripts/test_go.py --timeout`);
CI bounds each product job to 60 minutes. `test:race:all` is the explicit complete race suite for changes
to concurrency, persistence or lifecycle architecture. Run it when changing
shared state or the scope of the focused plan.

The focused race plan defaults every package and new test to race. The exact
normal-only exceptions and reasons live in
[`scripts/go-test-normal-only.json`](../scripts/go-test-normal-only.json).
Selection uses active Go test files and exact function names; it does not infer
coverage from name fragments. An unlisted test in an otherwise serial package
or existing test file automatically enters race, and stale/duplicate exceptions
fail validation. `python scripts/test_go.py race --list` prints the actual plan.
Discovery uses the race build constraints, including tests in race-only files
and packages.

`scripts/test_go.py normal|race|race-all --shard INDEX/TOTAL` partitions packages
into disjoint groups. It balances active top-level test counts, with a stable
package-name tie break; package fixtures and `TestMain` remain intact. Every
package belongs to exactly one shard, including newly added packages. The race
selection is applied inside each shard without changing its exclusions or test
patterns. Empty shards exit successfully without falling back to `./...`.
Independent full-package and selected-package race commands run with at most two
workers, so Control does not wait behind all other packages.

Asynchronous integration-test waits use `internal/testutil.Timeout`. Local runs
retain their normal budgets; CI sets `POT_TEST_TIMEOUT_SCALE=4` so a five-second
wait gets twenty seconds and a fifteen-second end-to-end fixture gets a minute.
Set the same variable locally to reproduce a slow-runner budget. This scales
test harness waits only, never product deadlines, polling intervals, timeout
inputs under test or the Darwin cancellation grace. A fixture with an overall
context should pass that context to its wait helper instead of adding a shorter
independent deadline. All assertions and fail-on-error behavior remain enabled.

Control tests with independent roots, listeners and execution managers run in
parallel, bounded by eight active cases and the available Go parallelism.
Tests changing process-wide environment variables stay serial. The schedule
probe matrix has independent fixtures; a separate serial case checks ambient
secret isolation. Accepted probe tasks pause at preparation after their real run
receipts exist; lifecycle tests cover subsequent model execution and delivery.
The GitHub notification fixture holds the polling lock across its explicit clock
sequence so the automatic ticker cannot overwrite simulated debounce windows.
Override the bound with `go test -parallel=N` when diagnosing
contention. Parallelism does not share mutable fixtures, skip assertions or
change the normal/race selection.

Full-package race remains on task scheduling/cancellation/recovery, execution
and process supervision, assistant/inbox queues and compaction, concurrent storage,
transport, Agent Link, plugin hosting and mutable provider caches. Control keeps
these integration paths, including workspace preparation and device/inode
replacement tests, approval/effect fences, cancellation, peer delivery, catalog
refresh cancellation and credential rotation. Its explicitly listed serial
configuration, resource/text and request-count contracts run in the complete
normal suite. Pure value/encoding/rendering helpers also run normally. No normal
functional or security tests are skipped by this selection in the normal suite.
New tests need no race opt-in; adding a
normal-only exception requires an explicit rationale in the reviewed manifest.
The standalone `test:executor` remains available for local diagnosis; CI gets
that full package coverage through `test:race` and does not run it twice.
Any nonzero exit fails the check; no failing tests are retried.
Finished-session output tests release a fixture through stdin and await its actual
completion before reading retained output; a yield interval does not promise exit.
UI tests cover owner-scoped state, drafts, IME, navigation and
streamed output. Eval uses the production Agent with deterministic local fixtures;
paid model calls run only through explicit live commands. Public API tests build
an independent external consumer and a standalone composition worker.

The opt-in `TestConfiguredModelBackgroundUpdatesLive` and
`TestConfiguredModelTaskRecoveryLive` tests use production prompts and delivery
with isolated stores and coordination-only tools. Run them on the configured
machine with `POT_LIVE=1`, `POT_CONFIGURED_LIVE_ROOT` and its existing key selector.
Alternatively, select a local Pi DeepSeek configuration with `POT_PI_LIVE_ROOT`
(the directory containing `models.json`) and `POT_PI_LIVE_MODEL` (a configured
model ID), leaving `POT_CONFIGURED_LIVE_ROOT` unset. Pi command-backed keys are
not executed. Credentials stay on that machine and must not enter logs.

```sh
POT_LIVE=1 POT_PI_LIVE_ROOT=/path/to/pi/agent POT_PI_LIVE_MODEL=MODEL_ID \
  mise exec -- go test ./internal/control \
  -run '^TestConfiguredModel(BackgroundUpdates|TaskRecovery)Live$' -count=1 -v -timeout=10m
```

Each suite caps real requests at 32 and output at 2048 tokens per request.
`POT_LIVE_OUTPUT` optionally writes synthetic evidence outside the repository.
Failures in recovery scenarios are injected before Worker model invocation;
Main, successful Workers and notification handling call the real API. Live tests
assert behavior and may fail when a model emits redundant acknowledgments.

Use `mise run preview` for a disposable browser fixture on loopback port 17702,
or `mise exec -- go run ./cmd/pot-app -preview` for a native fixture. Neither uses
product data or model calls. Browser fixtures do not establish real authentication
or remote transport behavior.

For linked profile editing and deletion through the authenticated browser gateway
and a real isolated Agent Link peer, start
`POT_CODEX_PROFILE_FIXTURE_URL_FILE=/tmp/pot-profile-url mise exec -- go test
./internal/agenthost -run '^TestCodexProfileBrowserFixture$' -count=1 -v
-timeout=20m`. Open the origin written to that file with the cached Playwright CLI,
then run `scripts/tests/pot-ui-codex-profile-browser.js` or
`scripts/tests/pot-ui-linked-assistant-delete-browser.js` using `run-code --filename`.
The latter retires the disposable remote assistant; restart the fixture before
repeating it. The fixture login grants only its temporary browser session; product
routes still pass through the real browser allowlist. Stop with `POST /fixture/stop`.

For update discovery and Settings' version refresh icon, run
`POT_UPDATES_FIXTURE_URL_FILE=/tmp/pot-updates-url mise exec -- go test
./internal/transport -run '^TestUpdateAuthenticatedHTTPAndBrowserFixture$'
-count=1 -v -timeout=27m`. Open the written origin at
`/scripts/tests/fixtures/updates.html?http=1&locale=en`. It uses disposable Control
stores, the real authenticated browser/HTTP update route and synthetic discovery
results; it never installs a package or invokes a model. `POST /fixture/update`
sets the synthetic status, `/fixture/calls` shows received check commands, and
`POST /fixture/stop` closes it. For same-data screenshots, extract the base commit's
`web/src/` and `pkg/storage/migrations/ui-cache/` into ignored
`output/update-before/`; `?before=1` selects those actual baseline assets. Use
`locale=zh-CN` and a phone viewport for the narrow titlebar/menu.

For an isolated long-chat browser fixture with real local history/SSE endpoints,
run `POT_HISTORY_FIXTURE_URL_FILE=/tmp/pot-history-url mise exec -- go test
./internal/transport -run '^TestHistoryBrowserFixture$' -v -count=1 -timeout=35m`.
Open the origin from that URL file at
`/scripts/tests/fixtures/chat-history.html`. Its desktop/mobile controls exercise
the shared DOM against synthetic messages and a deterministic loopback provider;
attachment bytes are synthetic local blobs. Stop it with `POST /fixture/stop` at
that private origin. This opt-in fixture adds no product data or native notifications.

For Task output invalidation and live transcript checks, set
`POT_DIRECTORY_FREQUENCY_URL_FILE` to a private temporary filename and run
`go test ./internal/transport -run '^TestDirectoryFrequencyBrowserFixture$'
-count=1 -v -timeout=13m`. With the same URL-file variable and the existing
Playwright/Chromium overrides, run either
`node scripts/tests/directory-frequency-browser.mjs` or
`node scripts/tests/task-output-live-browser.mjs`. Each runner owns shutdown of
its fixture; start a new fixture for the other runner. `--keep-fixture` leaves it
available for inspection until `POST /fixture/stop` or its 12-minute limit.
The fixture uses temporary state, local deterministic model output and an
authenticated synthetic browser session. The frequency runner records HTTP,
SSE, IndexedDB and composer frames in `POT_DIRECTORY_FREQUENCY_OUTPUT`, compares
pure output with lifecycle/permission invalidation, and captures review images.
`--baseline` checks a fixture built from source before output invalidation was
separated. The live runner creates a real Task, reads its incremental transcript
past the resource TTL and cancels through the UI.

`mise run test:pwa-cold-start` covers local startup and reconnect authority with
isolated production HTTP/SSE, IndexedDB and Service Worker fixtures. Its reconnect
checks hold directory or session responses, revoke capabilities and release
retired connection/Control responses; no command is submitted.

For chat stream lifecycle checks, `node scripts/tests/ui-regressions.mjs --group
connection` runs the production browser adapter and shared UI with real
EventSource, IndexedDB and a controlling Service Worker against synthetic HTTP
faults. It uses the product watchdog durations and separate browser contexts.
Use the existing `POT_PLAYWRIGHT_MODULE` and `POT_CHROMIUM_EXECUTABLE` overrides.
For a real isolated Control and authenticated browser gateway, set
`POT_STREAM_FIXTURE_URL_FILE` to a private temporary filename and run
`go test ./internal/transport -run '^TestChatStreamBrowserFixture$' -count=1 -v
-timeout=12m`. POST `/fixture/login` at the written loopback origin establishes
its synthetic cookie; `/fixture/meta` returns the fixture assistant ID. The
normal UI at `/` uses synthetic SQLite history and no model calls. Stop with
POST `/fixture/stop`. These desktop browser checks do not qualify physical iOS
PWA suspension or a real tailnet path.

`mise run test:ui-http-integration` exercises real local HTTP/SSE and downloads
with a deterministic loopback model fixture; it needs cached Playwright CLI and
an installed browser.

Each synthetic browser contract gets a fresh context, including its own init
scripts, routes and transcript cache. It receives the fixture browser's disposable
authentication state for provider probes. The real HTTP contract keeps its two
independent authenticated clients. Set `TMPDIR` to a private directory when the
default temporary filesystem is quota constrained.

For Markdown resource previews, start `POT_MARKDOWN_RESOURCE_FIXTURE=1
POT_RESOURCE_FIXTURE_URL_FILE=/tmp/pot-markdown-url mise exec -- go test
./internal/transport -run '^TestResourceBrowserFixture$' -count=1 -v -timeout=30m`.
Open the written origin at `/scripts/tests/fixtures/markdown-resources.html` and
run `scripts/tests/pot-ui-markdown-resources-browser.js` with Playwright's
`run-code --filename`. This uses synthetic files, production registration and
presentation tools, and the authenticated resource APIs. It checks the reader,
source switch, copying, original download, safety boundaries and load failures.
For before/after captures, put the baseline's actual `web/src` tree at
`output/markdown-before/web/src` first; `?before=1` loads those modules and styles
against the same registered Markdown. `?capture=1` shows one resource; `theme` and
`locale` select the visual scenario. Stop with `POST /fixture/stop` at its origin.

For Tasks/Schedules layout and query-adapter checks, serve the repository on a
loopback static server and run `node scripts/tests/tasks-schedules-browser.mjs
ORIGIN`. Set `POT_PLAYWRIGHT_MODULE` and `POT_CHROMIUM_EXECUTABLE` when using an
existing browser installation. The fixture renders the actual shared shell/pages
with synthetic records and captures images under `output/pr-images/` by default.
For a before comparison, extract the chosen base commit's `web/` and
`pkg/storage/migrations/ui-cache/` into ignored `output/tasks-before/`; the same
fixture and data select that source tree with `?before=1`. It does not read product
state, call a model or create real Tasks. For computer-use checks, open
`/scripts/tests/fixtures/tasks-schedules-scroll.html?width=390&small=1&locale=zh-CN&theme=dark`.
The fixed-width iframe checks actual page/list scroll ranges, bottom reachability,
refresh/disclosure retention, pagination focus and page position restoration.
Use `width=320&long=1` for long titles or omit `small` for paginated collections.
These are narrow container checks inside the browser, not physical device tests.
The browser runner also exercises these checks at actual Chromium viewport sizes.
The backend catalogue contract is covered
separately by `TestCatalog*` and `TestTaskCatalogHTTPFiltersAndValidation`.
`node scripts/tests/pot-ui-query-resource-refresh.mjs` exercises the real shared
query and resource schedulers together, including event bursts, explicit force
waiting, pagination, multiple resources and epoch-fenced commits.

For assistant-switching scroll checks without a backend, serve the repository
root on loopback and open
`/scripts/tests/fixtures/chat-switching.html?conversation=alpha&locale=en`.
It uses the shared production DOM with synthetic conversations and independently
delayed subscriptions and background-task reads.
The checks sample visible frames during width reflow, page returns, cached assistant
switches, reference positioning and delayed lookups, including A → B → A navigation. Run
at desktop and phone widths; `locale=zh-CN&theme=dark` exercises the other locale
and theme. Add `capture-navigation=1` to pause at the multiline-draft return
scenario; `window.navigationCaptureContinue()` resumes the checks.

`mise run test:ui-chat-position` checks send positioning and assistant switches
using the production UI, HTTP adapter, SSE and IndexedDB with synthetic loopback
history. It samples visible and hidden frames at desktop and phone widths,
including delayed draft hydration, acceptance, fonts and images, history beyond
the data window, reconnects and the five-second preview slot fallback. Set
`POT_PLAYWRIGHT_MODULE` and `POT_CHROMIUM_EXECUTABLE` to existing installations.
`POT_CHAT_OUTPUT` selects the untracked evidence directory; `POT_CHAT_SOURCE`
selects an extracted source tree. Pass `--baseline` directly to
`node scripts/tests/chat-scroll-http-browser.mjs` to record the known send and
draft-layout regressions in that tree while retaining the other assertions.

`mise run test:ui-prepared-layout` exercises connected off-flow rendering with
the same existing Playwright/Chromium prerequisites. It covers the native and
fallback scroll-end branches, recursive readiness, stable component deadlines,
load admission, same-node resource transfer, streaming HTTP/SSE output, mixed
image/PDF/HTML/Markdown previews through real history pagination, cached initial
layout and virtual remount, native group aliases across pagination/cold IndexedDB/remount, and a trusted mobile
touch gesture during an incomplete Mermaid fence's completion. The fixtures
use synthetic loopback data and reject product writes. They do not qualify
physical iOS/Safari behavior. `POT_PREPARED_OUTPUT`, `POT_HISTORY_RESOURCES_OUTPUT`, `POT_ALIAS_OUTPUT` and
`POT_TOUCH_OUTPUT` select untracked evidence directories. The touch runner accepts
`--baseline` with `POT_TOUCH_SOURCE` pointing to an extracted source tree; that
mode requires the temporary-source/shrink failure rather than reporting it as a
passing fix.

`mise run test:ui-chat-activity` uses the same isolated browser prerequisites to
check lifecycle activity through reconnects, empty model/tool rounds, long
output, parallel inputs and terminal outcomes. It samples row/status identity,
nonterminal frames, Latest visibility and historical reading anchors against
synthetic HTTP/SSE and committed IndexedDB state. Its history diagnostic runner
also checks paged failed/cancelled messages without receiver lifecycles and
cached paging after reload. `POT_HISTORY_OUTPUT` and `POT_HISTORY_SOURCE`
select that runner's evidence directory and source tree; it accepts `--baseline`.
`POT_ACTIVITY_OUTPUT` selects
the untracked evidence directory; `POT_ACTIVITY_SOURCE` selects an extracted
source tree. Pass `--baseline` directly to `node scripts/tests/chat-activity-browser.mjs`
to record that tree without enforcing the new activity contract.

On the same loopback server, `/scripts/tests/fixtures/chat-cache.html` exercises
the actual IndexedDB schema, reopening, atomic rollback, history reads, version
fencing, multiple writers and retired boots. `/scripts/tests/fixtures/chat-cache-ui.html?conversation=alpha&locale=en`
holds a real database transaction to check commit-before-display, samples
assistant-switch frames, restores historical reading in a fresh mount, reads
Latest without the backend, and reloads the browser to verify persisted output.
`/scripts/tests/fixtures/chat-cache-narrow.html` runs the same UI fixture in a
390px frame with Chinese text and dark appearance.
Both fixtures use isolated synthetic databases. Repeat the UI fixture at phone
width and with `locale=zh-CN&theme=dark`.

For scrolling and disclosure regressions, open
`/scripts/tests/fixtures/chat-scroll.html?conversation=alpha&locale=en&theme=light`
on the same loopback server. Run scrolling checks to exercise rapid reversals,
sample viewport coverage on each animation frame and frame intervals, traverse beyond the data window,
prefetch newer history with delayed reads, and inspect deferred tool/raw details.
Run at desktop and phone widths, including `locale=zh-CN&theme=dark`. Capture
reading position leaves a synthetic conversation available for manual wheel,
touchpad, keyboard and scrollbar checks.

For composer resizing, run `scripts/tests/pot-ui-chat-composer-browser.js` with
Playwright CLI `run-code --filename` against that fixture, on desktop and in an
iPhone WebKit context. Trusted textarea edits cover empty, multiline, height-cap
overflow and clearing cycles. Per-frame assertions check Latest following,
message movement matching composer growth, historical reading anchors and the
single TanStack scroll writer. The navigation fixture separately checks cached
multiline drafts and their reading positions on page and assistant returns.

For long mobile touch regressions, open the scrolling fixture with Playwright
CLI in an iPhone device context and run
`scripts/tests/pot-ui-chat-touch-browser.js` via `run-code --filename`.
Chromium uses trusted touch gestures, including long swipes and reversals beyond
the 640-message data window and back through evicted history;
the initial reversals run without a quiet interval between gestures.
WebKit simulates drag, pointer cancellation and momentum with native scroll
position input. The checks assert progress, viewport coverage and a single
TanStack scroll writer without writes during finger contact or the moving
momentum tail (ending at `scrollend` or 150 ms of quiet). Neither mode
qualifies physical iPhone Safari momentum or installed home-screen behavior.
`scripts/tests/pot-ui-scroll-input.mjs`, included in `mise run test:ui`, separately
exercises contact before movement, rapid gesture handoff, cancellation, held
measurement corrections, explicit Latest priority and the quiet-time fallback
with a deterministic clock against the actual vendored virtualizer.

`scripts/tests/pot-ui-mobile-directory-browser.js`, included in
`test:ui-http-integration`, checks directory geometry on every visible frame at
320 and 390 px. Chromium CDP touch input covers taps and 12/13/14 px Back-button
drifts, including simultaneous click and gesture settling. Full swipe commit and
cancel use synthetic pointer input because Chromium may hand larger native
gestures to browser navigation. Short/long conversations, focused drafts, delayed
directory refresh, search/scroll retention and wide layout share the same fixture.
These checks do not qualify physical iPhone Safari or a real on-screen keyboard.

For incoming correspondence disclosure checks, open
`/scripts/tests/fixtures/correspondence.html?locale=en&theme=light` on the loopback
server and run `await window.correspondenceFixture.verify()` in the browser.
At phone widths, select Pot from the assistant list first. Repeat with
`locale=zh-CN`; `before=1` shows the previous private-correspondence presentation
for comparison. This fixture uses production Chat components and synthetic data.

For compact work records, run `mise run test:work-records`, serve the
repository on loopback and open `/scripts/tests/fixtures/work-records.html`.
The separate `/scripts/tests/fixtures/task-communication.html` fixture covers Task identities.
Add `?identity=1` for the matching communication/background identity fixture,
including linked addresses, missing mappings and normal UUID-shaped IDs.
Add `edge=1` separately for repeated prefixes and equal IDs across nodes; both
keep the same four-character label while full addresses retain detail routing.
Run `scripts/tests/task-identity-browser.mjs` against the same origin to exercise
the shared marks, authoritative detail navigation and keyed background updates.
The fixture mounts production Chat with synthetic records and rejects writes.
`scenario=collapsed` keeps the disclosure closed; `locale` and `theme` select the
shared presentation. With a disposable Playwright installation, run
`scripts/tests/task-communication-browser.mjs` against that origin using
`POT_PLAYWRIGHT_MODULE` and optional `POT_CHROMIUM_EXECUTABLE`. It covers grouping,
tool/message counts, identity, native keyboard disclosure, append/prepend, retained text
selection, failures and virtual recycling. No real Task or model is started.

For background Task elapsed-time checks, serve the repository on loopback and open
`/scripts/tests/fixtures/background-task-time.html?locale=zh-CN&theme=light`.
It mounts the production rows and shared clock with synthetic records, reports
its assertions on the page and includes 720/390/320px containers. Repeat with
`locale=en&theme=dark` or `theme=system`. For matched baseline screenshots, extract
`web/` from the chosen base commit into ignored `output/task-time-before/`; the
fixture selects that source with `before=1`. With the disposable Playwright
module described below, run `scripts/tests/background-task-time-capture.mjs ORIGIN`
to check 1280/390/320px browser viewports and export paired PNGs. These are
Chromium checks, not physical-device qualification. For an alignment comparison,
extract the pre-correction PR head's `web/` into ignored
`output/task-alignment-before/` and pass `ORIGIN OUTPUT_DIRECTORY alignment` to
the same capture command. It checks mixed-status column starts, missing/short/long
durations and timer updates, then exports matched 720/390/320px strip pairs.

For message-title identity, streaming-argument, disclosure and Task checks, open
`/scripts/tests/fixtures/message-titles.html` on the same loopback server. The
fixture runs shared DOM assertions with synthetic full addresses and includes a
phone-width iframe. `?locale=zh-CN` starts in Chinese; each run also switches the
locale of mounted titles. Add `verify=1` to the correspondence fixture above to
check Chat names, directory renames and draft preservation automatically.

For reproducible communication screenshots across two actual source versions,
`scripts/tests/pot-ui-communication-review.py --before COMMIT --after COMMIT`
archives each version's `web/` into ignored `output/pr-images/` and serves both
on loopback port 17723. It copies the **same** review fixture into each tree;
the fixture mounts production Chat without extra styling. Open
`/before/scripts/tests/fixtures/communication-review.html?locale=en&theme=light`
or `/after/scripts/tests/fixtures/communication-review.html?locale=en&theme=dark`.
Use `scenario=collapsed`, `send`, `receive` or `details` to select the native disclosure;
at phone widths, select Pot first. Pot and Code reviewer are synthetic directory
identities; the latter is explicitly a Codex peer using the product brand assets.
Accepted/received records and the connected peer state are simulated. Requests
are handled in memory, writes are rejected, and no actual message, Task, approval,
notification or model call is made. The script never checks out another worktree.

To export actual Chromium PNGs, install a disposable Playwright toolchain in the
workspace (no permanent dependency is required), keep the fixture server running,
then use its public `page.screenshot({path})` API:

```sh
npm install --no-save --package-lock=false --prefix output/pr-images/toolchain playwright@1.64.1
PLAYWRIGHT_BROWSERS_PATH="$PWD/output/pr-images/toolchain/browsers" \
  node output/pr-images/toolchain/node_modules/playwright/cli.js install chromium --only-shell
PLAYWRIGHT_BROWSERS_PATH="$PWD/output/pr-images/toolchain/browsers" \
  POT_PLAYWRIGHT_MODULE="$PWD/output/pr-images/toolchain/node_modules/playwright/index.mjs" \
  node scripts/tests/pot-ui-communication-review-capture.mjs
```

Run these Node commands through `mise exec --` when the pinned toolchain is not
already on PATH. The capture script accepts an origin and output directory as
positional arguments. `POT_CHROMIUM_EXECUTABLE` optionally selects an existing
Chromium executable without downloading another browser. It uses independent fresh pages with equal data, locale,
theme and dimensions for each pair: 1280×1000 collapsed/send/receive/dark, 1280×1400 expanded
details, and 390×1000 phone receive in light and dark themes, all at device scale 1. It checks safe Markdown,
send/receive direction, native disclosure state, loaded images and horizontal fit.
Draft PNGs go to `output/pr-images/communication-review/`; copy only reviewed
captures into the source branch's [PR image directory](pr-images/README.md).
Stop the fixture server with Ctrl-C. This export route is separate from the
computer-use API's inline observations and uses no tool browser session/profile.

For ordinary chat bubble review, use the same two-source server with
`--fixture chat-bubbles --before COMMIT --after COMMIT --port 17729`, then run
`scripts/tests/pot-ui-chat-bubbles-capture.mjs` with the Playwright module configured
as above. The server includes each version's UI cache migration modules, which
are imported by Chat. The identical in-memory fixture uses production Chat with
long Markdown/code, synthetic attachments/results, local send rejection and
partial output failure. It makes no model or transport calls. The capture script
exports desktop light and phone dark before/after pairs and checks copying,
keyboard disclosure, theme/language changes, width reflow, retained streaming
nodes, stop/retry, references and resource/Task entrances. Pass the origin and
output directory as positional arguments to select another server or destination.

For message-body, multi-source batching, acceptance, delegation and identity
presentation checks, open `/scripts/tests/fixtures/communication.html` on that
loopback server. It runs shared DOM assertions with synthetic complete addresses,
partial arguments and error receipts, including live locale/directory updates and
a 390 px iframe. Use `?locale=zh-CN&theme=dark`; `phone=1` constrains the fixture
itself to the phone width. No message or Task is sent.

For the resource tool and component fixture, run
`POT_RESOURCE_FIXTURE_URL_FILE=/tmp/pot-resource-url mise exec -- go test
./internal/transport -run '^TestResourceBrowserFixture$' -v -count=1 -timeout=35m`.
Open the written origin in the browser, or its
`/scripts/tests/fixtures/display-resources.html` shared-component showcase. The
fixture uses a disposable Control, authenticated browser proxy, synthetic media
and a deterministic local provider that invokes the real resource tools.
`scripts/tests/pot-ui-display-resources-browser.js` can be run with the cached
Playwright CLI's `run-code --filename` option against that origin. Stop the
fixture with `POST /fixture/stop`. No product data or paid model calls are used.

Set `POT_HTML_SCROLL_FIXTURE=1` on that fixture command for long, nested-list and
short HTML resources. Open `/scripts/tests/fixtures/html-scroll.html` to exercise
the real reply renderer, historical presentation and file-reference modal with
live scroll coordinates. The focused native-scroll regression runs with
`node scripts/tests/pot-ui-html-scroll-browser.mjs ORIGIN [BASELINE_ORIGIN]
[CAPTURE_DIRECTORY]`, using the existing `POT_PLAYWRIGHT_MODULE` and
`POT_CHROMIUM_EXECUTABLE` overrides. An optional independently running baseline
origin enables matched before/after captures; it must serve the same synthetic
fixture with the baseline production sources. Wheel input in Chromium does not
establish physical touchpad inertia, touchscreen or native WebView behavior.

For semantic tool activity, serve the repository root on loopback and open
`/scripts/tests/fixtures/tool-content.html?locale=en&theme=light`. The shared Chat/Task
disclosure runs behavioral checks for terminal copying/following, retained output,
file diffs, Task receipt versus execution state, schedules, resources, extension
results, bounded rendering, safe content, approvals and live locale changes.
Repeat at 390 px with `locale=zh-CN&theme=dark`. `review=1` leaves deterministic
receipts for screenshots; `scenario=terminal` opens the command and patch, while
`scenario=details` opens Task, schedule and registration results.
`scripts/tests/pot-ui-tool-content-capture.mjs` runs the four locale/theme/width
combinations, existing communication checks and exports component screenshots.
Its positional arguments are the repository-server origin, an optional URL prefix
for an archived baseline containing `web/` plus the same fixture, an output
directory, and an optional disposable preview origin for the existing Chat/Task
tool and process-session integration checks. Use the `POT_PLAYWRIGHT_MODULE` and
`POT_CHROMIUM_EXECUTABLE` overrides described above for a cached local toolchain.

Use private temporary data roots for service integration tests. Never open the
same root in App and standalone Runtime concurrently. Report checks and limitations
in the task/PR, not repository validation reports. Store only reviewed UI comparison
screenshots under [PR image conventions](pr-images/README.md).

`hk.pkl` owns pre-commit and pre-push checks. Run commands through `mise`; do not
bypass failed hooks. `mise run check:push` runs the same local gate without
pushing. It targets about 30 seconds with installed tools, dependencies and a
warm Go build cache; a first build or slower machine can take longer. It does not
terminate or skip unfinished checks to meet that target.

Pre-push retains full Go formatting, vet and architectural import checks, UI,
packaging, deterministic CLI, public API, hook and license checks, plus macOS
source/release checks. `test:core` replaces the complete normal/race suites in
this local gate. Its explicit plan in
[`scripts/go-test-core.json`](../scripts/go-test-core.json) runs the full Agent,
model protocol, execution, process supervision, plugin host, peer/Agent Link,
storage/migration, access, secrets, inbox and scheduler packages without race
instrumentation. Selected integration tests cover Control approvals and exact
command grants, task cancellation/idempotency/rollback/retry, conversation
recovery and owner isolation, authenticated HTTP receipts and Codex approval
transport. Independent integration groups run with at most four workers; missing,
renamed, duplicate or inactive selections fail rather than silently losing coverage.
The core task uses `-count=1` so every push executes its tests even with a warm
build cache.
`mise exec -- python scripts/test_go.py core --list` prints the plan. Keep the
core selection focused on these contracts when adding or replacing tests.

`mise run test` retains the complete normal suite and focused race suite; neither
depends on the local core selection. Full CI runs them on Linux and macOS. The separate
`test:executor` diagnostic is already covered by that race suite and is not repeated
by pre-push. Hook group timings are appended to `.local/hooks/timings.jsonl`.

## CI and manual PR checks

`Tests` (`.github/workflows/tests.yml`) automatically runs **Lightweight checks
(Linux)** on PRs and pushes to `main`. One runner executes `mise run ci:light`:
source formatting and import boundaries, shared translations, the existing core
Go plan, UI and packaging contracts, release/cask/Docker contracts and workflow
validation. The core runner validates both its selections and the normal-only
race manifest before executing. This detects deleted test entries and naming,
configuration and translation integration errors without claiming full product
coverage. It does not run the complete normal/race matrix, real OS sandbox suite,
native packages or containers.

Manual `Tests` runs and release/build callers use **Full product** checks. A
reusable call requires the resolved source SHA; it always selects the full matrix,
including for `publish=false` candidates. Publication intent never selects test
coverage. Automatic lightweight runs and full runs use separate concurrency
families; only superseded automatic lightweight runs are canceled.

```text
PR / main push -> one Linux lightweight job

manual Tests / resolved release or candidate SHA -> Linux + macOS
                    | normal + static/UI/API/sandbox
                    | race 1/4 + 2/4 + 3/4 + 4/4
                    + all product gates required
                                  |
                         native distribution gates
```

Run `gh workflow run tests.yml --ref main` for complete product checks without
packaging, or use `ci.yml` below for complete product and distribution validation.

The normal/checks job runs `mise run ci:normal`, which schedules the complete normal
suite, static/architecture/localization checks, UI/API/packaging contracts,
release-script contracts and real OS sandbox tests concurrently. This keeps the
matrix to five jobs per OS instead of creating a runner for every small check.
Normal and race CI tests use `-count=1`; build caches do not replace execution.
`mise run check:ci` runs the static and script contracts independently.

The target is about 200 seconds with warm tool, module and build caches. Runner
queues, first compilations and cache transfers can extend elapsed time; no gate
is skipped or killed to satisfy the target. The shared setup action installs
only each job's required tools. Module caches are shared independently of build
caches; normal/race/build cache families include source hashes so changed sources
can save refreshed build results instead of repeatedly restoring an old exact hit.

The release/build `CI` workflow runs automatically only on tag pushes. All tags
are checked, while exact stable version tags enter controlled per-channel release
paths after shared checks and the corresponding native package and smoke gates
succeed. Ordinary manual runs do not publish.

On an interactive Apple Silicon Mac, `mise run test:macos-update` builds the
current source twice with versions `0.1.0` and `0.2.0` and an ephemeral update key.
It runs the actual Wails Apps and App worker in disposable installations and
data roots, simulating an already verified/staged package through the production
install API. It checks successful upgrade, executable-launch failure and
startup-exit rollback, new
PID/boot, preserved root/identity/arguments/environment/working directory, and
bundle signatures. It neither publishes releases nor opens existing product
roots. Test builds use a temporary Go overlay to route only the updater's fixed
fixture feed/package URLs to a loopback HTTP server, with no public-network
fallback. Each host must pass the real check API and report verified discovery
and automatic installation eligibility before the prepared scenarios run.
Production builds have no fixture transport or new URL/configuration override.
Download verification remains covered by the updater Go tests. The native macOS
packaging job runs this simulation as well.
For existing test builds, run `mise exec -- python
scripts/tests/macos-update-smoke.py /absolute/Old.app /absolute/New.app --feed /absolute/update.json`.
These existing Apps must independently discover and verify that same target;
prepared files alone cannot authorize installation.

Branch pushes and PR events do not start the release/build workflow.
That workflow always calls the full `Tests` gate for its resolved source SHA. Headless
and cross-platform builds run afterward as a separate build gate, before native
packaging. Explicit ref/PR validation still disables shared caches.

```sh
gh workflow run ci.yml --ref main -f pr=123
gh workflow run ci.yml --ref main -f ref=refs/heads/my-branch
```

The optional `ref` and `pr` inputs are mutually exclusive. Leave both blank to
check the ref selected in Actions. The source job pins one SHA for all subsequent
jobs; review selected PR code before running it. Read-only validation receives no
release secrets. See [Releases and installation channels](releases.md) for the
native build matrix, artifact names, signing requirements, manual release gate,
Homebrew generation and GHCR permissions.

## Database changes

Every persisted schema or JSON format change must add a migration under
`pkg/storage/migrations/` in the same change. Follow [Database migrations](database-migrations.md)
for versioning, startup upgrades and required upgrade coverage.

## Source layout

| Directory | Responsibility |
|---|---|
| `cmd/` | CLI, App, Eval and isolated composition-worker entry points |
| `pkg/` | Public execution, Agent and embedding libraries |
| `internal/` | Product services and adapters |
| `web/` | Shared product frontend |
| `plugins/` | Tool manifests and Node implementations |
| `integrations/` | External application packages, local connectors and installation entry points |
| `skills/` | Built-in Skill bundles |
| `resources/` | Brand assets, model metadata and notices |
| `scripts/` | Build, release, source checks and fixtures |
| `eval/` | Deterministic and explicit live evaluation |

`resources/app/PotIcon.swift` is the vector artwork master for app icons and tray
atlases. `mise run icons` generates these assets; App packaging includes `Pot.icns`.

Paid tests require an explicit `POT_LIVE=1` in the calling process.

`TestConfiguredModelAgentLinksLive` qualifies two independent Controls with the
machine's configured model and production prompts. Set `POT_CONFIGURED_LIVE_ROOT`
and its existing `POT_SECRET_KEY_FILE`; configuration is read only and credentials
stay on that machine. Both Controls use disposable roots and expose only
`list_assistants` and `send_message` to the model. The test checks read-only
preview, symmetric pinned pairing, full node-address matching with equal display
names, model-generated messages and replies in both directions, processing and
provenance, and identity-preserving reconnection after restart. It caps the whole
run at 24 real requests, 2048 output tokens per request and five minutes. Run:

```sh
POT_LIVE=1 POT_CONFIGURED_LIVE_ROOT=/absolute/control/root \
  mise exec -- go test -v ./internal/control \
  -run '^TestConfiguredModelAgentLinksLive$' -count=1 -timeout=6m
```

`TestConfiguredModelAgentProfileLive` uses `POT_CONFIGURED_LIVE_ROOT` and the
machine's existing key-file selector to read its configured model credentials
locally. It runs the production Agent, executor and profile tool in disposable
Controls. The direct case draws a cow/horse PNG and saves the name and avatar in
user chat. The background case delegates a monkey/keyboard PNG to a Task, then
imports that Task artifact and updates the profile during result handling. It
verifies the persisted avatar against the actual Task image and the successful
notification tool receipt. Each case caps provider calls at 24 and output at 4096
tokens per call.
`POT_LIVE_OUTPUT` optionally saves the normalized PNG and non-secret evidence
outside the repository. Production conversations, profiles and services are not
modified. Run with `POT_LIVE=1 mise exec -- go test ./internal/control -run
'^TestConfiguredModelAgentProfileLive$' -count=1 -v -timeout=6m`.

For quiet task dispatch and model-directed automatic recovery, run
`TestConfiguredModelTaskRecoveryLive` with `POT_LIVE=1`,
`POT_CONFIGURED_LIVE_ROOT` pointing to the selected machine's existing Control
root, and its existing `POT_SECRET_KEY_FILE` when applicable. Configuration and
credentials are read on that machine; all Runtime writes use private test roots.
The test preserves the configured model, protocol and reasoning effort, caps
output at 2048 tokens and real requests at 32, and exposes only coordination
tools. Single/multiple dispatch must be silent; injected pre-execution failures
exercise recovery after single and repeated transient failures.

`POT_LIVE_OUTPUT` optionally retains private transcript evidence outside the repo.
`POT_BASELINE_MAIN_PROMPT_FILE` optionally adds a creation-confirmation comparison
using an explicitly supplied earlier prompt. For example:

```sh
POT_LIVE=1 POT_CONFIGURED_LIVE_ROOT=/absolute/control/root \
  mise exec -- go test -v ./internal/control \
  -run '^TestConfiguredModelTaskRecoveryLive$' -count=1 -timeout=9m
```

`TestConfiguredModelTaskResultsLive` uses the same configured-root/key selectors
to qualify final replies and artifact delivery with the real model. It covers a
plain Task, a scheduled report without a result-file schema, partial attachment
delivery with a symlink diagnostic, and optional worker-to-owner communication.
The model creates all files itself; owner resource previews are checked against
their actual contents. It retains production prompts, caps the whole run at 48
requests and 2048 output tokens per request, and uses disposable stores and Task
workspaces. Run it with `POT_LIVE=1`, `POT_CONFIGURED_LIVE_ROOT` and the machine's
existing key selector, using `-run '^TestConfiguredModelTaskResultsLive$' -count=1
-v -timeout=10m`. Optional `POT_LIVE_OUTPUT` saves non-secret result evidence
outside the repository. No installed service or production data is modified.

For a machine's existing configured model, the opt-in
`TestConfiguredModelMessageReferencesLive` test accepts
`POT_REFERENCE_LIVE_ROOT` pointing to that Control root and the existing
`POT_SECRET_KEY_FILE` when applicable. It reads provider metadata and encrypted
credentials with a read-only SQLite connection, then uses an isolated test
Control, conversation and accounting. It checks selected-excerpt interpretation,
references to native assistant replay, and direct whole-message reference content
with the original absent from retained model history. No message lookup tools are
available to the model. It makes
at most six model requests, with three per turn and 512 output tokens per request.
Credentials stay on the configured machine and are never logged. Run it with
`POT_LIVE=1 mise exec -- go test -v ./internal/control -run '^TestConfiguredModelMessageReferencesLive$' -count=1 -timeout=4m`
and the two explicit path variables above.

`TestConfiguredModelTaskLifecycleLive` uses `POT_LIVE=1`,
`POT_CONFIGURED_LIVE_ROOT` and the existing key selector to qualify batch archive
choices and continuation with the machine's configured model. It uses production
prompts without overrides and isolated stores with a synthetic Git remote/worktree.
It caps requests at 32 with 2048 output tokens each. It checks that delivered local
work can archive while work needing user input remains open, then continues the
archived Task with its original files and model context. Model policy failures
remain test failures rather than being hidden by prompt overrides.
`POT_LIVE_OUTPUT` optionally saves private evidence outside the repository.

For the configured model Task workspace qualification, use `POT_LIVE=1`,
`POT_WORKSPACE_LIVE_ROOT` and the existing `POT_SECRET_KEY_FILE` on that machine:

```sh
POT_LIVE=1 mise exec -- go test -v ./internal/control \
  -run '^TestConfiguredModelTaskWorkspacesLive$' -count=1 -timeout=9m
```

The fixture uses a private Control, local bare Git remote and synthetic project
files. It exercises managed worktree base refresh, direct-project and Runtime-created persistent
workspaces, real model-generated patches/commands, instruction discovery and file
delivery, capped at 18 model requests. Model/account credentials remain in memory
on that machine. `POT_WORKSPACE_LIVE_RESOURCES` points to installed package assets;
`POT_WORKSPACE_LIVE_PROTECTED=1` additionally selects Protected mode for worktree
and temporary cases and requires SRT host dependencies. The deterministic Linux
sandbox suite also exercises Runtime Git preparation and linked Git metadata.

For DeepSeek tests, copy `.env.example` to `.env` in the current repository root and set
`DEEPSEEK_API_KEY`; `DEEPSEEK_BASE_URL` and `DEEPSEEK_MODEL` default to
`https://api.deepseek.com` and `deepseek-v4-flash`. `.env` is Git-ignored.
Configuration is loaded only after opt-in; putting `POT_LIVE=1` in `.env` cannot
enable paid calls. Tests locate this checkout's root because `go test` changes
the working directory to the package directory. They never read Pi configuration
or export credentials to child commands. Values are literal `KEY=value`
assignments with optional single/double quotes or an `export` prefix, without
shell/variable expansion.

```sh
POT_LIVE=1 mise exec -- go test -v ./internal/control \
  -run '^TestDeepSeekUnsandboxLive$' -count=1 -timeout=16m
```

Prepare the pinned SDK first with `mise run test:sandbox`; override its location
with `POT_SANDBOX_SDK` if necessary. This uses the real Agent, Control/Gateway,
approval store and OS sandbox with synthetic files only. Six isolated scenarios
are capped at six model requests each, 768 output tokens per response and 150
seconds per scenario. They cover ordinary private-temp/workspace execution,
permanent exact-command approval, owner denial and recovery without repeating a
partial write. Two boundary scenarios give only an ordinary file-reading goal:
a workspace symlink produces a real sandbox read denial, after which the model
must autonomously request justified host access. The harness only approves
synthetic reads; refusal never releases host code. It checks that approval follows
the actual denial and
that an owner refusal stops further execution. Run just those cases with
`-run '^TestDeepSeekUnsandboxLive$/^boundary-'`. Default tests skip paid calls.
Credentials are never logged.

Task guidance can be qualified against real DeepSeek with:

```sh
POT_LIVE=1 mise exec -- go test -v ./internal/control \
  -run '^TestDeepSeekTaskGuidanceLive$' -count=1 -timeout=3m
```

This uses production prompts and tools in a private Runtime. Worker startup is
held until the real assistant forwards additional CI trigger rules to the existing
Task using `send_message({to, text})`. The test checks durable acceptance and
consumption by the original Worker, then verifies its real model response includes
the rules. It performs no repository or CI changes and is capped at 12 paid
requests, 768 output tokens per response and 150 seconds. Default tests skip it.

Category-specific message batching can be qualified against real DeepSeek with:

```sh
POT_LIVE=1 mise exec -- go test -v ./internal/control \
  -run '^TestDeepSeekCategoryBatchesLive$' -count=1 -timeout=6m
```

The test uses a private Runtime and a transparent recording proxy that forwards
real model responses. Holding the first foreground request lets normal send
entry points populate the production queues. It checks ordered user aggregation,
one summary for successful and failed Tasks, and peer grouping by full sender
across wire conversations. A one-round Worker budget creates the controlled
failure after a real model request. All other processing keeps the production
prompts and tools. The run is capped at 24 paid requests, 768 output tokens per
response and 240 seconds. `POT_LIVE_OUTPUT` may name a fresh private output
directory for the observation; request headers and credentials are never recorded.

The Add assistant and settings fixtures run in the `management` source group:
`mise exec -- node scripts/tests/ui-regressions.mjs --group management`.
A phone assistant/settings scenario and one scenario per Codex/Antigravity
connection preserve creation, draft restoration, invitation verification,
type mismatch, pairing retry, cancellation and connection reuse. Fixtures use
synthetic node directories and no model credentials. They avoid repeating each
workflow across the full locale/theme/width product.

For real HTTP authentication and signed Agent Link pairing, start
`POT_FIRST_CONNECTION_FIXTURE_URL_FILE=/tmp/pot-first-connection-urls mise exec --
go test ./internal/agenthost -run '^TestFirstConnectionBrowserFixture$' -count=1 -v
-timeout=30m`. The URL file contains independent zero-connection Controls for each
Agent and create/manage/observe intent. Open a URL with
`/scripts/tests/fixtures/first-connection-live.html?locale=en` (or `zh-CN`). The
fixture supplies a temporary browser login and disposable clipboard invitation;
product authentication, pairing and management remain real. Native CLIs return
mock metadata, no worker starts, and installation commands are only displayed.
`scripts/tests/pot-ui-first-connection-live-browser.js` checks create/manage
intents using an injected `STATE` with `origin`, `kind` and `intent`. POST
`/fixture/stop` to any fixture URL when finished. All databases are temporary.

Run composer selection, native mention editing and desktop/mobile Enter/IME with
`mise exec -- node scripts/tests/ui-regressions.mjs --group composer`.
The representative phone fixture covers freshness before sending, Skill/assistant
payloads, query boundaries, unavailable assistants, owner-scoped drafts and late
lookups. Native keyboard/clipboard/undo checks share that scenario; the separate
Enter contract owns its desktop and touch contexts. These are Chromium checks,
not physical keyboard or iPhone Safari qualification.

The Codex integration tests use a real Agent Link transport and a supervised
App Server protocol fixture. Run the task/approval and automatic-review boundary
coverage without model credentials:

```sh
mise exec -- go test -race ./internal/agenthost \
  -run 'Test(CodexTaskAndPeerApprovalFromPot|AutomaticReviewDoesNotPublishApprovalHistory|DirectTasksOwnIndependentPersistentSessions)'
```

The fixture exercises caller-owned manual decisions, independent task sessions,
same-task continuation and normal outcome delivery. Direct user chat is a Pot
adapter capability; Codex task and message adapters do not implement it.
`TestConfiguredModelAgentLinksLive` separately covers user-only Pot-to-Pot replies
using the machine's configured model, under its existing request/output caps.

## Frontend regression selection

Run `mise run build:web-ui`, `mise run lint`, `mise run check:go`,
`mise run test:i18n` and `mise run test:ui` before browser qualification.
Focused selection does not replace those checks. `test:ui` contains the cheap
reducer/domain, scope, command, timer and runner contracts. PR/main checks remain
lightweight; full browser/HTTP smoke is opt-in for manual/release qualification.

Browser runners use existing tools, start only private loopback fixtures and
close their browsers/listeners. They never install packages or read product data:

```sh
export POT_PLAYWRIGHT_MODULE=/absolute/path/to/playwright/index.mjs
export POT_CHROMIUM_EXECUTABLE=/absolute/path/to/chrome
mise run test:ui-scroll
mise run test:ui-nav
mise run test:ui-history
mise run test:ui-domain
mise run test:ui-storage
mise run test:ui-http-integration
```

The HTTP stage also needs cached `@playwright/cli`. Set `npm_config_cache` when
that cache lives outside the npm default. For a nondefault Chrome location, set
`PLAYWRIGHT_MCP_CONFIG` to an existing CLI JSON configuration whose
`browser.launchOptions.executablePath` names the installed executable (and
`browser.launchOptions.headless` is `true` for headless runs).
`POT_CHROMIUM_EXECUTABLE` selects the source runner's browser; it does not
configure the CLI. The standard runner can derive that source executable from
the CLI configuration, and the Playwright module from a cache containing exactly
one installation. Ambiguous or missing module discovery fails with an explicit
override instruction. No runner downloads a tool or browser.

| Group | Behavior and fixture boundary |
|---|---|
| `scroll` | Controller: held/reversed/repeated measurements and wheel/key/touch interleaving; phone Chat: real 36px wheel, every message 221–239 readable in both directions, same-ID screen displacement, complete ordered 80-row IDB page, async image growth, nested tools, reading/new output/Latest. Desktop Task: smaller mixed-height wheel witness 1189–1199 through its actual repository/page. One shared adapter contract covers focus during virtual retirement, prepend/append/end, reorder/trim, disposal and deferred tool/raw output. |
| `nav` | 320/390px CDP touch taps and 12/13/14px Back drift, every visible intermediate frame, search/scroll/draft retention, delayed refresh and commit/cancel. Cached owner/page navigation separately checks first-frame anchors, historical folding, search/list node and scroll retention, no folding backend reads, collapsed settings/phone returns, live menu locale/target, focus, reference identity and late IO. The feature drawer separately checks native cancellation, pointer commit and Reduced Motion settling before the first visible frame. |
| `history` | Visible top automatically reads 80 rows/one cursor, prepend anchor, short-window filling, Retry, single flight, same-cursor termination, new output while reading and Latest/run/dispose late-result isolation. |
| `domain` | Fast Node contracts plus real-IDB repository fixture: progressive sources, retired queries, captured owner/run, mutation idempotency, commit-before-notify, evicted backfill and run fencing. |
| `storage` | 2048/8192-record reads: incremental 1 row/0 scan, history 80 rows/1 cursor; namespace, reset/cursor/boot/version fences, abort/storage failure, cross-owner isolation, UI commit-before-display and cached-loading races. |
| `management`, `composer`, `approvals`, `usage` | Representative configurations retain live locale, creation/pairing, native mentions/IME, exact one-time decisions, all data states, calendar/range/owner scopes and held stale requests. `management --case machines` also checks policy CAS plus actual Task/tool/approval host labels and schedule machine selection → locale → captured-scope save. Full repeated theme/width matrices are omitted. |

`mise run test:ui-regressions` runs all source groups once. List or narrow them:

```sh
mise exec -- node scripts/tests/ui-regressions.mjs --list
mise exec -- node scripts/tests/ui-regressions.mjs --group scroll --case task-continuity
mise exec -- node scripts/tests/ui-regressions.mjs --group nav --case directory
mise exec -- node scripts/tests/ui-regressions.mjs --group usage --case usage-trend
```

Each source scenario gets a fresh context, explicit locale, isolated IDB names and
routes. Device matrices own their contexts directly. HTTP contracts consume the
outer runner's page and disposable authentication; the real transport smoke keeps
two independent clients and an anonymous client. Providers and connection probes
require that HTTP fixture's explicit provider/assistant preconditions; do not run
them as standalone static fixtures. A retained catalogue is not authoritative:
fixtures must resolve its fresh read before testing send payloads. Keep source
holds and release/flush steps explicit, with bounded queues and frame drains.

Wheel assertions compare the same stable message ID before/after each input,
using `min(delta, remaining)` or `max(delta, -scrollTop)` at boundaries. The 3px
budget covers fractional DOMRect geometry, integer extents and the existing 1px
Latest snap; it is not an observed residual jump. Output includes measured
`worstDrift`, raw wheel delta, boundary-adjusted expected movement and actual anchor
movement. Required sequence witnesses end the traversal, bounded by a maximum step
count. The adapter fixture's direct positioning does not qualify gestures. Large
mobile swipe commit/cancel uses synthetic pointers; physical Safari momentum and
on-screen keyboards require device qualification.

Failures include scenario, recent stable IDs/visible range, actual frames,
scroll/anchor geometry, cursor/cancellation and pending network paths where
available. Authentication and query strings stay out of logs. Narrow the failed
case before repeating the full suite. Fault probes are development checks, not a
default repeated matrix; they alter only the loopback response, never checkout:

```sh
mise exec -- node scripts/tests/ui-regressions.mjs --group scroll --case controller --fault controller-ownership
mise exec -- node scripts/tests/ui-regressions.mjs --group scroll --case continuity --fault scroll-flow
mise exec -- node scripts/tests/ui-regressions.mjs --group nav --case directory --fault directory-grid
mise exec -- node scripts/tests/ui-regressions.mjs --group history --case task-history-edges --fault history-cursor
mise exec -- node scripts/tests/ui-regressions.mjs --group domain --fault domain-progress
```

Each probe must exit nonzero at its corresponding behavior assertion; a tool
startup error is not evidence. Update fault locations when product fixes move.

The standard `test:ui-http-integration` entry builds the current CLI, runs one
private real HTTP/SSE/model/artifact fixture, then all source groups once. It
retains authentication/rotation, two clients/reconnect, wrong-owner/path rejection,
binary integrity and provider contracts. Task lifecycle integration keeps original
run/owner, menu/approval decisions, retry identity, drafts/disclosures, keyboard
focus and terminal-result deduplication. Source workflows no longer repeat through
five additional HTTP wrappers. `python scripts/tests/ui-http-suite.py --list`
prints both stages; any nonzero exit stops the suite and reports incomplete status
and elapsed time. Use cached `@playwright/cli` (`npm_config_cache`), an existing
Chrome (`PLAYWRIGHT_MCP_CONFIG`) and a private `TMPDIR` if needed.

Update SVG/spinning/restored state and ReleaseURL checks require the update UI in
the tested base; otherwise only the available update state/domain checks apply.
Do not copy product changes into a test-only branch. Optional
`db-driven-task.html?before=1` remains a manual visual comparison requiring an
explicit baseline under ignored `output/task-history-before/`; obsolete Load
earlier button behavior is excluded from current-product regression execution.
