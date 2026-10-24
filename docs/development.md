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
`build`/`bundle` create `.local/go/PotGoPreview.app`, an independent development
bundle with ID `dev.pot.go-preview`. Release packaging
uses the production identity. Do not replace a running product's files implicitly.

The macOS builder downloads the pinned Wails module before applying its
checksum-verified patches to a disposable copy. A fresh module cache is supported;
the original cache and repository module files remain unchanged.

## Builds and packaging

| Command | Output |
|---|---|
| `mise run build:go` | CLI under `.local/go/bin` |
| `mise run build:linux` | CGO-free Linux amd64/arm64 CLI and tool packages |
| `mise run build:cli` | Target CLI archive with Node, tools and Skills |
| `mise run build` | Complete Wails macOS bundle |
| `mise run build:web-ui` | Embedded shared HTML/CSS/JS frontend |
| `mise run build:plugins` | Tool manifests and declared sources |
| `mise run release -- VERSION` | App/CLI archives, checksums, cask and configured signed update feed; publishing is separate |

`scripts/build-go-app`, `scripts/build-go-cli` and `scripts/go_packaging.py` own
layout and checksums. Standalone and embedded Runtime locate trusted tool assets
independently of workspace preparation.

Local packaging defaults to development version `0.0.0`, matching CI builds from
unversioned refs. Set `POT_APP_VERSION=MAJOR.MINOR.PATCH` to build a specific version;
`scripts/release VERSION` and official CI set it explicitly. The CLI, App bundle,
package metadata and container labels use that version. Plain `go build` without
the packaging linker flags reports `development`.

For cross-platform tool packages:

```sh
mise exec -- python scripts/build-go-plugins --platform linux-amd64 \
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
Automatic updates require matching `selfupdate.StorageFormat` markers and
centralized migration fingerprints. Schema changes automatically block package-only
rollback; non-database persisted-format changes must also change the marker.

## Checks and isolated previews

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

`test:i18n` checks shared catalog key parity, nonempty translations and matching
ordered `{parameter}` markers. It does not scan for hardcoded UI text or verify
runtime language changes; follow the [i18n rules](internationalization.md) for
those checks.

`test:sandbox` runs real file, socket, network, terminal, plugin and cleanup tests with the pinned SDK. `test:sandbox-linux` uses a disposable non-root Docker host with namespace restrictions relaxed only on the outer test container; only test assets are mounted read-only.

`check:go` verifies formatting, vet and architectural imports. `test:go` runs all
Go tests without race instrumentation. `test` runs the normal and focused race
suites independently, alongside UI/packaging/API checks. `test:race` no longer
implicitly repeats the normal suite. Both paths use a configurable 20-minute
per-package timeout (`POT_GO_TEST_TIMEOUT`, or `scripts/test_go.py --timeout`);
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
./integrations/codex/agent -run '^TestCodexProfileBrowserFixture$' -count=1 -v
-timeout=20m`. Open the origin written to that file with the cached Playwright CLI,
then run `scripts/tests/go-ui-codex-profile-browser.js` or
`scripts/tests/go-ui-linked-assistant-delete-browser.js` using `run-code --filename`.
The latter retires the disposable remote assistant; restart the fixture before
repeating it. The fixture login grants only its temporary browser session; product
routes still pass through the real browser allowlist. Stop with `POST /fixture/stop`.

For an isolated long-chat browser fixture with real local history/SSE endpoints,
run `POT_HISTORY_FIXTURE_URL_FILE=/tmp/pot-history-url mise exec -- go test
./internal/transport -run '^TestHistoryBrowserFixture$' -v -count=1 -timeout=35m`.
Open the origin from that URL file at
`/scripts/tests/fixtures/chat-history.html`. Its desktop/mobile controls exercise
the shared DOM against synthetic messages and a deterministic loopback provider;
attachment bytes are synthetic local blobs. Stop it with `POST /fixture/stop` at
that private origin. This opt-in fixture adds no product data or native notifications.

`mise run test:ui-http-integration` exercises real local HTTP/SSE and downloads
with a deterministic loopback model fixture; it needs cached Playwright CLI and
an installed browser.

For Markdown resource previews, start `POT_MARKDOWN_RESOURCE_FIXTURE=1
POT_RESOURCE_FIXTURE_URL_FILE=/tmp/pot-markdown-url mise exec -- go test
./internal/transport -run '^TestResourceBrowserFixture$' -count=1 -v -timeout=30m`.
Open the written origin at `/scripts/tests/fixtures/markdown-resources.html` and
run `scripts/tests/go-ui-markdown-resources-browser.js` with Playwright's
`run-code --filename`. This uses synthetic files, production registration and
presentation tools, and the authenticated resource APIs. It checks the reader,
source switch, copying, original download, safety boundaries and load failures.
For before/after captures, put the baseline's actual `web/src` tree at
`output/markdown-before/web/src` first; `?before=1` loads those modules and styles
against the same registered Markdown. `?capture=1` shows one resource; `theme` and
`locale` select the visual scenario. Stop with `POST /fixture/stop` at its origin.

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

For composer resizing, run `scripts/tests/go-ui-chat-composer-browser.js` with
Playwright CLI `run-code --filename` against that fixture, on desktop and in an
iPhone WebKit context. Trusted textarea edits cover empty, multiline, height-cap
overflow and clearing cycles. Per-frame assertions check Latest following,
message movement matching composer growth, historical reading anchors and the
single TanStack scroll writer. The navigation fixture separately checks cached
multiline drafts and their reading positions on page and assistant returns.

For long mobile touch regressions, open the scrolling fixture with Playwright
CLI in an iPhone device context and run
`scripts/tests/go-ui-chat-touch-browser.js` via `run-code --filename`.
Chromium uses trusted touch gestures, including long swipes and reversals beyond
the 640-message data window and back through evicted history;
the initial reversals run without a quiet interval between gestures.
WebKit simulates drag, pointer cancellation and momentum with native scroll
position input. The checks assert progress, viewport coverage and a single
TanStack scroll writer without writes during finger contact or the moving
momentum tail (ending at `scrollend` or 150 ms of quiet). Neither mode
qualifies physical iPhone Safari momentum or installed home-screen behavior.
`scripts/tests/go-ui-scroll-input.mjs`, included in `mise run test:ui`, separately
exercises contact before movement, rapid gesture handoff, cancellation, held
measurement corrections, explicit Latest priority and the quiet-time fallback
with a deterministic clock against the actual vendored virtualizer.

For incoming correspondence disclosure checks, open
`/scripts/tests/fixtures/correspondence.html?locale=en&theme=light` on the loopback
server and run `await window.correspondenceFixture.verify()` in the browser.
At phone widths, select Pot from the assistant list first. Repeat with
`locale=zh-CN`; `before=1` shows the previous private-correspondence presentation
for comparison. This fixture uses production Chat components and synthetic data.

For adjacent Task communication, run `mise run test:task-communication`, serve the
repository on loopback and open `/scripts/tests/fixtures/task-communication.html`.
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
batch counts, identity, native keyboard disclosure, append/prepend, retained text
selection, failures and virtual recycling. No real Task or model is started.

For message-title identity, streaming-argument, disclosure and Task checks, open
`/scripts/tests/fixtures/message-titles.html` on the same loopback server. The
fixture runs shared DOM assertions with synthetic full addresses and includes a
phone-width iframe. `?locale=zh-CN` starts in Chinese; each run also switches the
locale of mounted titles. Add `verify=1` to the correspondence fixture above to
check Chat names, directory renames and draft preservation automatically.

For reproducible communication screenshots across two actual source versions,
`scripts/tests/go-ui-communication-review.py --before COMMIT --after COMMIT`
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
  node scripts/tests/go-ui-communication-review-capture.mjs
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
`scripts/tests/go-ui-chat-bubbles-capture.mjs` with the Playwright module configured
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
`scripts/tests/go-ui-display-resources-browser.js` can be run with the cached
Playwright CLI's `run-code --filename` option against that origin. Stop the
fixture with `POST /fixture/stop`. No product data or paid model calls are used.

For semantic tool activity, serve the repository root on loopback and open
`/scripts/tests/fixtures/tool-content.html?locale=en&theme=light`. The shared Chat/Task
disclosure runs behavioral checks for terminal copying/following, retained output,
file diffs, Task receipt versus execution state, schedules, resources, extension
results, bounded rendering, safe content, approvals and live locale changes.
Repeat at 390 px with `locale=zh-CN&theme=dark`. `review=1` leaves deterministic
receipts for screenshots; `scenario=terminal` opens the command and patch, while
`scenario=details` opens Task, schedule and registration results.
`scripts/tests/go-ui-tool-content-capture.mjs` runs the four locale/theme/width
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
depends on the local core selection. CI runs them on Linux and macOS. The separate
`test:executor` diagnostic is already covered by that race suite and is not repeated
by pre-push. Hook group timings are appended to `.local/hooks/timings.jsonl`.

## CI and manual PR checks

`Tests` (`.github/workflows/tests.yml`) runs full product tests on PRs and pushes
to `main`, with cancellation of superseded runs. It is also reusable by the
release/build workflow and can be run manually. Each OS starts five jobs:

```text
                          Tests (Linux + macOS)
                 +-----------+--------+--------+--------+
                 |           |        |        |        |
          normal + checks  race 1/4 race 2/4 race 3/4 race 4/4
          + UI/API/sandbox
                 |           |        |        |        |
                 +-----------+--------+--------+--------+
                                   |
                    all product gates must pass
```

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
Branch pushes and PR events do not start the release/build workflow.
That workflow calls the same `Tests` gate for its resolved source SHA. Headless
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
files. It exercises managed worktree base refresh, direct-project and OS temporary
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

The Add assistant browser fixture is included in `test:ui-http-integration` and can
also run independently with `mise exec -- python
scripts/tests/go-ui-assistant-start-browser.py`. It uses synthetic node directories
and no model credentials. Desktop/mobile, English/Chinese and light/dark cases
cover explicit API creation, conversation draft restoration, source-specific
connection instructions, remote default/explicit selection, empty-directory
refresh and directory-load failure recovery. Screenshots stay under ignored
`output/playwright/`.

The composer selector browser fixture is also part of `test:ui-http-integration`:
`mise exec -- python scripts/tests/go-ui-composer-selectors-browser.py`.
It exercises the shared Chat DOM with synthetic skill/assistant directories,
saved avatars and receipts at desktop/phone widths in both languages and themes.
It covers query boundaries, compact rows, keyboard/IME selection, unavailable
assistants, owner-scoped drafts, late lookups, failed sends and unchanged send
destinations. It makes no model calls and saves screenshots under ignored
`output/playwright/`.
The same fixture checks native mention typing, clipboard serialization, whole-label
deletion, undo/redo and highlight scrolling. Set `POT_COMPOSER_BROWSER=webkit` to
run it against the cached WebKit browser.

The Codex integration tests use a real Agent Link transport and a supervised
App Server protocol fixture. Run the task/approval and automatic-review boundary
coverage without model credentials:

```sh
mise exec -- go test -race ./integrations/codex/agent \
  -run 'Test(CodexTaskAndPeerApprovalFromPot|AutomaticReviewDoesNotPublishApprovalHistory|DirectTasksOwnIndependentPersistentSessions)'
```

The fixture exercises caller-owned manual decisions, independent task sessions,
same-task continuation and normal outcome delivery. Direct user chat is a Pot
adapter capability; Codex task and message adapters do not implement it.
`TestConfiguredModelAgentLinksLive` separately covers user-only Pot-to-Pot replies
using the machine's configured model, under its existing request/output caps.
