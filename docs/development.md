# Development guide

## Development and running

Requires Apple Silicon, macOS 26+, Xcode/Swift 6.2+, and [mise](https://mise.jdx.dev/). `mise.toml` pins Rust, Python, Zig, cargo-zigbuild, and check tools; Swift uses the selected Xcode.

```sh
mise trust
mise install
mise run setup        # Rust components, Linux target, repository hk hooks, environment checks
mise run prepare
mise exec -- scripts/configure-pi # Optional: Pi + DeepSeek only
mise run build
mise run bundle
mise run open
```

`bundle` preinstalls dependencies in a separate VM and builds the complete Sandbox OCI image. Software downloads happen only during build; Helper starts the Host backend immediately and boots an environment on demand. See [Sandbox image](sandbox-image.md) for caches, fonts, and sizes.

The development bundle is `.local/d1/Pot.app`. `scripts/pot open` uses `.local/d1/local-runtime` (overridable with `POT_LOCAL_ROOT`); double-clicking defaults to `~/Library/Application Support/pot`. These use separate data.

`configure-pi` imports host Pi DeepSeek credentials into private Host configuration without model calls or embedding keys in App. The model is `deepseek-v4-flash`; see [UI specification](ui-ux-design.md#allowance-configuration) for configuration, default allowances, and K/M token input rules. Existing configuration is not overwritten automatically; the usage ledger is not reset.

## Standalone Server (development entry point)

Build and start the backend without App/Helper. Paths belong to the Server machine. Use a separate data directory rather than opening active App/VM data.

```sh
mise exec -- cargo build --locked -p pot-server
POT_DATA_DIR=/tmp/pot-server/product POT_WORKSPACE=/tmp/pot-server/workspace POT_RUNTIME_BIND=127.0.0.1:17700 mise run server
```

`POT_PI_BIN` selects the Pi executable. Server writes private bearer/boot/port data to `runtime.json` in its data directory. HTTP/SSE/MCP/PTY retain the existing Control API. Without a configured Agent, main chat shows an AI Configuration entry; Sandbox remains available. Pot no longer has a Terminal page. Shutdown stops background coordinators, sessions, PTYs, Codex hosts, and SSE.

App retains local Helper/VM discovery without remote endpoint settings. Cross-machine deployment still needs TLS, identity, and artifact transfer; this change establishes only independent backend execution boundaries.

## Validation

```sh
mise run fmt           # Explicit formatting fixes
mise run check         # Formatting, scripts, Rust workspace, Swift Helper build
mise run test          # Backend / protocol, no real model calls
mise run test:app
```

Current real CLI/VM acceptance (build and bundle first; no paid model calls):

```sh
mise exec -- python scripts/tests/sandbox-runtime.py --helper .local/d1/Pot.app/Contents/Helpers/PotHelper --resources .local/d1/Pot.app/Contents/Resources/runtime --output /tmp/pot-sandbox-acceptance.json
```

Apple VM networking requires Virtualization privileges. This starts two isolated Linux VMs with pinned Host Pi/Codex and local mock models, validates lazy startup, worker scope, environment independence, cancellation, desktop and native resume, then cleans up. Dated results and limits are in [decoupling acceptance](validation/agent-sandbox-20261006.md). Older `codex-runtime.py`, guest CLI checks and ignored Pi native-tool fixtures document the old deployment and are not current acceptance commands.

Application account authorization uses official Host Codex App Server APIs. Pi supports API connections; Grok execution/authorization is excluded. The callback listener is ephemeral, and account credentials remain with the native CLI. Real-provider account/inference acceptance is separate from the credential-free Sandbox routing contract.

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
| `driver/` | Host Pi RPC and Codex App Server adapters |
| `gateway/` | Provider HTTP transport and model protocol codecs |
| `protocol/` | Shared message and control types |
| `image/` | Guest desktop, browser, broker/viewer resources |
| `scripts/` | Build, bundle, configuration import, icon generation |

`poc/` is removed; experiment code/evidence remain in Git history. `.local/poc/` is a retained local build/download cache path without dependencies on old experiment sources.

Machine connection/deployment notes belong in `~/Workspace/Machines`. Do not commit credentials, user sessions, or build artifacts. Inspect live state through Runtime APIs.

### Supported integrations

Codex uses native official authorization. Claude, Gemini, DeepSeek and OpenCode use API connections through Pi. Grok execution, direct Host tools and macOS/Windows Sandbox providers are outside this increment. Old guest deployment has no compatibility migration. Historical data is not deleted; use a fresh Host root for the new design.
