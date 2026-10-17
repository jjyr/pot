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
bundled Node; browser tools also need Chrome/Chromium on the execution machine.
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

The explicit paid permission qualification uses the configured DeepSeek Flash from
`~/.pi/agent/models.json` (or `POT_PI_MODELS`) without modifying Pi configuration:

```sh
POT_DEEPSEEK_LIVE=1 mise exec -- go test -v ./internal/control \
  -run '^TestDeepSeekUnsandboxLive$' -count=1 -timeout=12m
```

Prepare the pinned SDK first with `mise run test:sandbox`; override its location
with `POT_SANDBOX_SDK` if necessary. This uses the real Agent, Control/Gateway,
approval store and OS sandbox with synthetic files only. Four isolated scenarios
are capped at six model requests each, 768 output tokens per response and 150
seconds per scenario. They cover ordinary private-temp/workspace execution,
permanent exact-command approval, owner denial and recovery without repeating a
partial write. Default tests skip paid calls. Credentials are never logged.
