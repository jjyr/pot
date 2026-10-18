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
| `mise run release -- VERSION` | Local archive/checksum/cask; publishing is separate |

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

Use `mise run preview` for a disposable browser fixture on loopback port 17702,
or `mise exec -- go run ./cmd/pot-app -preview` for a native fixture. Neither uses
product data or model calls. Browser fixtures do not establish real authentication
or remote transport behavior. `mise run test:ui-http-integration` exercises real
local HTTP/SSE and downloads with a deterministic loopback model fixture; it needs
cached Playwright CLI and an installed browser.

Use private temporary data roots for service integration tests. Never open the
same root in App and standalone Runtime concurrently. Report checks and limitations
in the task/PR, not repository validation reports. Store only reviewed UI comparison
screenshots under [PR image conventions](pr-images/README.md).

`hk.pkl` owns pre-commit and pre-push checks. Run commands through `mise`; do not
bypass failed hooks. CI separates portable product and macOS packaging checks.

## Source layout

| Directory | Responsibility |
|---|---|
| `cmd/` | CLI, App, Eval and isolated composition-worker entry points |
| `pkg/` | Public execution, Agent and embedding libraries |
| `internal/` | Product services and adapters |
| `web/` | Shared product frontend |
| `plugins/` | Tool manifests and Node implementations |
| `skills/` | Built-in Skill bundles |
| `resources/` | Brand assets, model metadata and notices |
| `scripts/` | Build, release, source checks and fixtures |
| `eval/` | Deterministic and explicit live evaluation |

`resources/app/PotIcon.swift` remains the vector artwork master used by icon and
tray generation; removing the VM Helper does not remove brand generation.

Paid tests require an explicit `POT_LIVE=1` in the calling process.

For a machine's existing configured model, the opt-in
`TestConfiguredModelMessageReferencesLive` test accepts
`POT_REFERENCE_LIVE_ROOT` pointing to that Control root and the existing
`POT_SECRET_KEY_FILE` when applicable. It reads provider metadata and encrypted
credentials with a read-only SQLite connection, then uses an isolated test
Control, conversation and accounting. It checks selected-excerpt interpretation,
generated-message IDs with native replay, and ID-only lookup with the original
absent from retained model history. It makes
at most six model requests, with three per turn and 512 output tokens per request.
Credentials stay on the configured machine and are never logged. Run it with
`POT_LIVE=1 mise exec -- go test -v ./internal/control -run '^TestConfiguredModelMessageReferencesLive$' -count=1 -timeout=4m`
and the two explicit path variables above.

For an isolated Responses metadata diagnostic, run
`TestConfiguredModelMetadataLive` with the same explicit opt-in and path variables.
It uses the first configured profile, its exact model/protocol/reasoning effort,
and synthetic history containing a side-effect-free accepted-tool result and a
selected-excerpt reference to the preceding assistant message. Each
of its native-replay and text-history cases permits one real request, capped at
1536 output tokens, with no retries. A transparent loopback recorder forwards the
serialized request to the configured endpoint and records bounded raw response
SSE, item phases, decoded deltas and the durable reply/checkpoint by request ID.
It records no headers, redacts the configured credential if present in a body,
and keeps captures only in an owner-only temporary test directory, automatically
removed when the test ends. There is no production logging switch or retained
trace output. The test checks decode/persistence consistency; a passing sample
does not establish that the upstream can never emit metadata.

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
