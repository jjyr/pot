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

Use private temporary data roots for service integration tests. Never open the
same root in App and standalone Runtime concurrently. Report checks and limitations
in the task/PR, not repository validation reports. Store only reviewed UI comparison
screenshots under [PR image conventions](pr-images/README.md).

`hk.pkl` owns pre-commit and pre-push checks. Run commands through `mise`; do not
bypass failed hooks. CI separates portable product and macOS packaging checks.

## CI and manual PR checks

CI runs automatically on every tag push, including tags outside the release
version pattern. Branch pushes (including `main`), PR events and GitHub Release
events do not start CI. The separate Release workflow still publishes only for
`v*.*.*` tag pushes or its explicit manual release input; it does not wait for CI.

To run CI manually, open **Actions → CI → Run workflow** and choose a trusted
branch or tag containing the workflow. Leave `pr` blank to check that selected
ref, or enter a PR number from this repository to check its latest head commit,
including a fork PR. For example:

```sh
gh workflow run ci.yml --ref main -f pr=123
```

The workflow resolves the PR head once at the start and pins both Linux and macOS
jobs to that SHA in the base repository, rather than the PR merge commit. Invalid
or inaccessible PR numbers fail before product jobs start. A later push to the PR
requires another manual run. Runs for different PR numbers do not cancel each
other; a new run for the same workflow ref and PR cancels the previous run.

Manual PR checks execute the PR's build scripts and tool configuration on GitHub
hosted runners. Review that code before starting the run and select a trusted
workflow ref. Only the source-resolution job is granted pull-request read
permission. Product jobs have read-only repository access, do not persist checkout
credentials, skip shared mise and Go caches, and receive no release secrets. They
use the selected workflow's check steps with the PR's source tree; changes to the
PR's workflow are not run.

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
locally. It runs the production Agent, executor and profile tool in a disposable
Control, asking the model to draw an original cow/horse PNG and set both its name
and avatar. The test caps provider calls at 12 and output at 4096 tokens per call.
`POT_LIVE_OUTPUT` optionally saves the normalized PNG and non-secret evidence
outside the repository. Production conversations, profiles and services are not
modified. Run with `POT_LIVE=1 mise exec -- go test ./internal/control -run
'^TestConfiguredModelAgentProfileLive$' -count=1 -v -timeout=6m`.

For quiet task dispatch and bounded automatic recovery, run
`TestConfiguredModelTaskRecoveryLive` with `POT_LIVE=1`,
`POT_CONFIGURED_LIVE_ROOT` pointing to the selected machine's existing Control
root, and its existing `POT_SECRET_KEY_FILE` when applicable. Configuration and
credentials are read on that machine; all Runtime writes use private test roots.
The test preserves the configured model, protocol and reasoning effort, caps
output at 2048 tokens and real requests at 32, and exposes only coordination
tools. Single/multiple dispatch must be silent; injected pre-execution failures
exercise successful recovery and exhaustion of the one-replacement budget.
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
