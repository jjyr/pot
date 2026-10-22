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

`prepare` supplies pinned Node, tool plugins, Skills and license assets. It starts
no execution environment. `build`/`bundle` create `.local/go/PotGoPreview.app`, an
independent development bundle with ID `dev.pot.go-preview`. Release packaging
uses the production identity. Do not replace a running product's files implicitly.

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
layout and checksums. Packages include no VM engines, images, guest bootstrap,
remote desktop viewer or guest execution subprocess. Standalone and embedded
Runtime locate trusted tool assets independently of workspace preparation.

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

`check:go` verifies formatting, vet and architectural imports. `test:go` uses the
race detector. UI tests cover owner-scoped state, drafts, IME, navigation and
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

Use private temporary data roots for service integration tests. Never open the
same root in App and standalone Runtime concurrently. Report checks and limitations
in the task/PR, not repository validation reports. Store only reviewed UI comparison
screenshots under [PR image conventions](pr-images/README.md).

`hk.pkl` owns pre-commit and pre-push checks. Run commands through `mise`; do not
bypass failed hooks. CI separates portable product and macOS packaging checks.

## CI and manual PR checks

CI runs automatically only on tag pushes. All tags are checked, while exact stable
version tags enter controlled per-channel release paths after shared checks and
the corresponding native package and smoke gates succeed. Ordinary manual runs do not publish.
Branch pushes and PR events do not start CI.

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

`resources/app/PotIcon.swift` remains the vector artwork master used by icon and
tray generation; removing the VM Helper does not remove brand generation.

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
