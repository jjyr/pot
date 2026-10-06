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

`bundle` preinstalls dependencies in a separate VM and builds the complete Sandbox OCI image. Software downloads happen only during build; Helper starts the Host backend immediately and boots an environment on demand. See Sandbox image for caches, fonts, and sizes.

The development bundle is `.local/d1/Pot.app`. `scripts/pot open` uses `.local/d1/local-runtime` (overridable with `POT_LOCAL_ROOT`); double-clicking defaults to `~/Library/Application Support/pot`. These use separate data.

Configure the Main Agent in Chat or Settings → AI Configuration. pot-agent uses saved API connections; Codex uses official Host browser authorization. pot-agent is built into Server; Node and Codex are bundled. Saving a connection does not send a model request, and no Sandbox must be started for configuration or chat. Execution limits are stored separately in `product/execution-limits.json`.

## Standalone Server (development entry point)

Build and start the backend without App/Helper. Paths belong to the Server machine. Use a separate data directory rather than opening active App/VM data.

```sh
mise exec -- cargo build --locked -p pot-server
POT_DATA_DIR=/tmp/pot-server/product POT_WORKSPACE=/tmp/pot-server/workspace POT_RUNTIME_BIND=127.0.0.1:17700 mise run server
```

Server writes private bearer/boot/port data to `runtime.json` in its data directory. HTTP/SSE/MCP/PTY retain the existing Control API. Without a configured Agent, main chat shows an AI Configuration entry; Sandbox remains available. Pot no longer has a Terminal page. Shutdown stops background coordinators, sessions, PTYs, Codex hosts, and SSE.

App retains local Helper/VM discovery without remote endpoint settings. Cross-machine deployment still needs TLS, identity, and artifact transfer; this change establishes only independent backend execution boundaries.

## Validation

```sh
mise run fmt           # Explicit formatting fixes
mise run check         # Formatting, scripts, Rust workspace, Swift Helper build
mise run test          # Backend / protocol, no real model calls
mise run test:app
mise run licenses      # Enforce Rust license policy and generate dependency notices
mise run test:licenses # Check approved, disallowed and unknown license cases
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
| `app/` | GPUI UI, Swift native bridge, configuration |
| `helper/` | Swift Containerization, VM lifecycle |
| `server/` | Standalone startup, HTTP/SSE/MCP/PTY routes, listener, shutdown |
| `runtime/` | Backend library, product commands, tasks/schedules, execution coordination/recovery |
| `core/` | Immutable Agent models, execution contracts, shared tool schema |
| `storage/` | SQLite, binding/usage/file-request ledgers, transactions |
| `agent/`, `driver/` | In-process pot-agent and Host Codex App Server |
| `tool-executor/` | Guest command sessions and patch implementation; explicit local fixtures for eval/tests |
| `gateway/` | Provider HTTP transport and model protocol codecs |
| `protocol/` | Shared message and control types |
| `image/` | Guest desktop, browser, broker/viewer resources |
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
