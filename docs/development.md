# Development guide

## Development and running

Requires Apple Silicon, macOS 26+, Xcode/Swift 6.2+, and [mise](https://mise.jdx.dev/). `mise.toml` pins Rust, Python, Zig, cargo-zigbuild, and check tools; Swift uses the selected Xcode.

```sh
mise trust
mise install
mise run setup        # Rust components, Linux target, repository hk hooks, environment checks
mise run prepare
mise run build
mise run bundle
mise run open
```

`bundle` preinstalls dependencies in a separate VM and builds the complete Sandbox OCI image. Software downloads happen only during build; App embeds the Host backend and boots an environment on demand. See Sandbox image for caches, fonts, and sizes.

The development bundle is `.local/d1/Pot.app`. `scripts/pot open` uses `.local/d1/local-runtime` (overridable with `POT_LOCAL_ROOT`); double-clicking defaults to `~/Library/Application Support/pot`. These use separate data.

Configure the Main Agent in Chat or Settings → AI Configuration. pot-agent uses saved API connections; Codex uses official Host browser authorization. pot-agent is built into Server; Node and Codex are bundled. Saving a connection does not send a model request, and no Sandbox must be started for configuration or chat. Execution limits are stored separately in `product/execution-limits.json`.

## Releases and Homebrew

Public macOS releases contain the complete Apple Silicon app, Helper, Host runtime,
and Sandbox image. Install through `brew install --cask jjyr/tap/pot`.

Build a release locally on Apple Silicon macOS 26+:

```sh
mise run release 0.1.0
```

This uses release-mode Rust binaries, creates a fresh `.local/d1/Pot.app` with the
requested bundle version, and writes `Pot-0.1.0-arm64.zip`, its `.sha256` file, and
`pot.rb` to `.local/releases/`. `--output PATH` selects a different output directory.
The Helper retains its ad-hoc signature and virtualization entitlement. The Cask
template lives in `packaging/homebrew/pot.rb.in`; packaging fills in the version
and ZIP checksum. Its postflight hook removes only `com.apple.quarantine` from
the installed app, respecting Homebrew's configured app directory.

The Release workflow runs for numeric `vMAJOR.MINOR.PATCH` tags. Manual dispatch
can retry an existing tag. Before the first release, configure:

- A native Apple Silicon macOS 26+ self-hosted runner with labels `macOS`, `ARM64`,
  and `pot-release`, Xcode/Swift 6.2+, mise, and GitHub CLI. Sandbox image construction
  requires Apple Virtualization support; use a dedicated runner checkout.
- Repository secret `HOMEBREW_TAP_TOKEN` with contents read/write access to
  `jjyr/homebrew-tap` to publish `Casks/pot.rb` automatically.

The workflow publishes the ZIP, checksum, and generated Cask to GitHub Releases,
then commits the Cask to the existing tap. If the tap token is absent, copy the
release's `pot.rb` to `Casks/pot.rb` in `jjyr/homebrew-tap` and commit it manually.
The installation command becomes available after the first release and tap update.
Application user data is retained on uninstall.

## Standalone Server (development entry point)

Build and start the backend without App/Helper. Paths belong to the Server machine. Use a separate data directory rather than opening active App/VM data.

```sh
mise exec -- cargo build --locked -p pot-server
POT_LOCAL_ROOT=/tmp/pot-server POT_RUNTIME_BIND=127.0.0.1:17700 mise run server
```

Server writes private bearer/boot/port data to `runtime.json` in its data directory. HTTP/SSE/MCP/PTY retain the existing Control API. Without a configured Agent, main chat shows an AI Configuration entry; Sandbox remains available. Pot no longer has a Terminal page. Shutdown stops background coordinators, sessions, PTYs, Codex hosts, and SSE.

App embeds Server and uses in-memory dispatch for both Rust and Swift clients. Helper runs with `--sandbox-only`. See [Standalone Server](standalone-server.md) for CLI installation, local OS authority and packaging.

## Mobile browser pilot

See [Mobile access](mobile-access.md) for the optional owner-controlled HTTPS listener, QR/pairing administration and `mise run build:mobile`. Native Runtime remains loopback-only when mobile access is configured. Desktop broker/control and physical-device qualification are separate gates.

## Validation

```sh
mise run fmt           # Explicit formatting fixes
mise run check         # Formatting, scripts, Rust workspace, Swift Helper build
mise run test          # Backend / protocol, no real model calls
mise run test:app
mise run test:ui      # Portable shared chat/page state and localization
mise run licenses      # Enforce Rust license policy and generate dependency notices
mise run test:licenses # Check approved, disallowed and unknown license cases
mise run test:release   # ZIP metadata, permissions, symlinks and Cask checksum
mise run test:i18n     # Catalogue parity, native switching and embedded web assets
```

Current real CLI/VM acceptance (build and bundle first; no paid model calls):

```sh
mise exec -- python scripts/tests/sandbox-runtime.py --helper .local/d1/Pot.app/Contents/Helpers/PotHelper --resources .local/d1/Pot.app/Contents/Resources/runtime --output /tmp/pot-sandbox-acceptance.json
```

Apple VM networking requires Virtualization privileges. This starts two isolated Linux VMs with pinned Host Pot/Codex and local mock models, validates lazy startup, worker scope, environment independence, cancellation, desktop and native resume, then cleans up. Dated results and limits are in [decoupling acceptance](validation/host-pot-agent-20261006.md).

Application account authorization uses official Host Codex App Server APIs. pot-agent supports API connections. The official Host Codex process handles browser callbacks directly; account credentials remain with the native CLI. Real-provider account/inference acceptance is separate from the credential-free Sandbox routing contract.

### Git hooks

`mise run setup` installs repository-local hk pre-commit/pre-push hooks without changing global hooks. Run it once per clone. Git hooks load tools through mise without shell activation; mise itself must be on PATH.

- pre-commit: checks formatting/scripts for changed files; Rust formatting covers the workspace, and rule/check-entry changes trigger all lightweight checks. Also checks staged whitespace.
- pre-push: relevant Rust/Swift changes trigger `check:rust` and core tests; Helper changes additionally trigger `check:swift`.
- Hooks only check; they neither fix nor stage. Source checks read the working tree, so partially staged content must also be formatted. Image/VM/paid model tests are excluded. A new machine may compile slowly; later runs reuse caches.

Manual hook check: `mise exec -- hk run pre-commit --all`. Use `mise run check` for full local checks and `mise run fmt` for fixes. Base rules live in `ruff.toml`, `.swiftformat`, and `scripts/dev-check`; hooks select and invoke them.

## Code entry points

| Directory | Responsibility |
|---|---|
| `app/` | Native GPUI shell, Swift bridge, configuration |
| `ui/` | Shared Chat/Tasks/Sandbox/Usage components, page/chat state, Markdown, tokens and localization |
| `mobile/` | WASM/browser shell, authenticated transport, PWA and push |
| `helper/` | Swift Containerization, VM lifecycle |
| `server/` | Standalone startup, HTTP/SSE/MCP/PTY routes, listener, shutdown |
| `runtime/` | Backend library, product commands, tasks/schedules, execution coordination/recovery |
| `core/` | Immutable Agent models, execution contracts, shared tool schema |
| `storage/` | SQLite, binding/usage/file-request ledgers, transactions |
| `agent/`, `driver/` | In-process pot-agent and Host Codex App Server |
| `tool-executor/` | Guest command sessions and patch implementation; explicit local fixtures for eval/tests |
| `gateway/` | Provider HTTP transport and model protocol codecs |
| `protocol/` | Shared message and control types |
| `sandbox-images/linux/default/` | Guest desktop, browser, broker/viewer resources |
| `scripts/` | Build, bundle, icon generation |

`poc/` is removed; experiment code/evidence remain in Git history. `.local/poc/` is a retained local build/download cache path without dependencies on old experiment sources.

Machine connection/deployment notes belong in `~/Workspace/Machines`. Do not commit credentials, user sessions, or build artifacts. Inspect live state through Runtime APIs.

### Supported integrations

Codex uses native official authorization. Claude, Gemini, DeepSeek and OpenCode use API connections through pot-agent. Direct Host tools and macOS/Windows Sandbox providers are outside this increment. Old guest deployment has no compatibility migration. Historical data is not deleted; use a fresh Host root for the new design.

## About and dependency licenses

The native Pot menu includes About Pot with version, developer, MIT, project link,
and Rust dependency notices. Bundling enforces the cargo-about policy and embeds
its report. See [About and license policy](licenses-and-about.md) for scope,
reviewed MPL dependencies, and CI checks.

macOS prepared-instance setup and qualification limits: [macOS Sandbox](macos-sandbox.md). Non-VM checks: `mise exec -- python scripts/tests/macos-provider.py` and `mise exec -- python scripts/tests/macos-helper.py --helper .local/d1/helper-build/release/PotMacOSHelper`.

## Separate Sandbox resources

Client bundling does not build or embed Linux Sandbox resources. Use `mise run build:sandbox-image`, then `mise exec -- python scripts/export-sandbox-resources --update-catalog` before client compilation. `mise run export:sandbox-resources` checks an export against the embedded catalog. Offline VM fixtures also require `mise exec -- cargo build -p pot-sandbox-images` to provide the verified importer. See [resource delivery](sandbox-resources.md) for tag publication, cache/approval behavior and pinned-instance compatibility.
