# Development guide

## Development and running

Requires Apple Silicon, macOS 26+, Xcode/Swift 6.2+, and [mise](https://mise.jdx.dev/) 2026.9.2 or newer. `mise.toml` pins Rust/Cargo, mr-boxington, Python, Zig, cargo-zigbuild, and check tools; Swift uses the selected Xcode.

```sh
mise trust
mise install
mise run setup        # Rust components, Linux target, repository hk hooks, environment checks
mise run prepare
mise run build
mise run bundle
mise run open
```

`build:sandbox-image` preinstalls guest dependencies in a separate VM and builds the Sandbox OCI image for resource publication. `bundle` packages Host binaries. End users authorize downloads of the pinned kernel, boot and OCI assets before the first Sandbox startup. See Sandbox image for caches, fonts, and sizes.

The development bundle is `.local/d1/Pot.app`. `scripts/pot open` uses `.local/d1/local-runtime` (overridable with `POT_LOCAL_ROOT`); double-clicking defaults to `~/Library/Application Support/pot`. These use separate data.

Configure the Main Agent in Chat or Settings → AI Configuration. pot-agent uses saved API connections. pot-agent is built into Server; Node is bundled for JavaScript tools and plugins. Saving a connection does not send a model request, and no Sandbox must be started for configuration or chat. Execution limits are stored separately in `product/execution-limits.json`.

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
  and `pot-release`, Xcode/Swift 6.2+, mise (including its pinned Brotli tool), and GitHub CLI. Sandbox image construction
  requires Apple Virtualization support; use a dedicated runner checkout.
- Repository secret `HOMEBREW_TAP_TOKEN` with contents read/write access to
  `jjyr/homebrew-tap` to publish `Casks/pot.rb` automatically.

The workflow publishes the ZIP, checksum, and generated Cask to GitHub Releases,
then commits the Cask to the existing tap. If the tap token is absent, copy the
release's `pot.rb` to `Casks/pot.rb` in `jjyr/homebrew-tap` and commit it manually.
The installation command becomes available after the first release and tap update.
Application user data is retained on uninstall.

## Standalone Server (development entry point)

For end-user installation with `cargo install --git` and the Control/Runner
setup procedures, see [Server installation](deployment.md#install-server).

Build and start the backend without App/Helper. Paths belong to the Server machine. Use a separate data directory rather than opening active App/VM data.

```sh
mise exec -- cargo build --locked -p pot-server
POT_LOCAL_ROOT=/tmp/pot-server POT_RUNTIME_BIND=127.0.0.1:17700 mise run server
```

Server writes private bearer/boot/port data to `runtime.json` in its data directory. HTTP/SSE/MCP/PTY retain the existing Control API. Fresh standalone roots have no product resources. Create assistants, enable Host execution and define sandboxes through the resource API; enable Web UI explicitly with its fixed Control address. Enabling Control exposes AI Configuration before registration. Fresh App registries
are empty; configure AI, then explicitly create the first assistant in Chat. Pot no longer has a Terminal page. Shutdown stops background coordinators, sessions, PTYs, and SSE.

App embeds Server and uses in-memory dispatch for both Rust and Swift clients. Helper runs with `--sandbox-only`. See [Standalone Server](deployment.md) for CLI installation, local OS authority and packaging.

The `pot-server` package enables the optional `web-ui` feature by default.
Its build script generates and compresses the frontend before embedding it.
`--no-default-features` skips Web tooling and resources completely. There is no
separate compression feature. `POT_ACCESS_ASSETS` remains the external-directory
fallback for builds without embedded assets.

Development uses `"conda:brotli-bin" = "1.2.0"` in `mise.toml`; run `mise install`.
This installs the actual Brotli CLI, not a Rust compression implementation, and
requires no separately installed Conda. Standalone Cargo users must install the
CLI themselves. Missing Brotli produces an actionable error.

`mise run build:web-ui` and Cargo's build script use the same cache under
`<Cargo target directory>/pot-web/`. Source/lockfile fingerprints validate the
WASM/static asset cache; raw and compressed payload hashes detect missing or
corrupt files. Brotli version and quality are part of the compression cache key.
Unchanged assets are reused individually. Generated files stay in the ignored
build directory and must not be committed. `mise run test:web-assets` checks
reuse, invalidation, corruption recovery and missing-tool diagnostics.

The first build uses rustup to prepare the pinned nightly WASM target and
wasm-bindgen CLI, then obtains checksum-verified noVNC sources. Nested Cargo
builds use a separate target directory and native compiler flags are removed to
avoid lock contention and cross-target contamination. A cache lock serializes
simultaneous frontend builds. CI installs Brotli through mise too.

Brotli provides a larger size reduction than JS minification or `wasm-opt -Oz` on
the measured client; those additional optimizers are not part of the build.
The comparison and qualification are recorded in
[Web assets validation](validation/embedded-web-assets-20261012.md).

To build a CLI with embedded Web UI and the Tailscale library from this checkout:

```sh
mise exec -- python scripts/build-cli
```

The local archive under `.local/releases/` contains `bin/pot`, the Tailscale
library and builtin skills. Extract it into a chosen prefix and add its `bin`
directory to PATH. This is a developer build artifact, not a published Server
download. Enable web-ui-server through the [explicit Web UI setup](deployment.md#explicit-web-ui).
MacOS Sandbox helpers/resources require additional provider setup.

## Browser UI

App and browser mount the same `pot-ui` application; see [Shared UI](shared-ui.md)
for the adapter boundary. See [Web UI deployment](deployment.md#explicit-web-ui)
and [Tailscale HTTPS](deployment.md#tailscale-https) for the configurable
web-ui-server listener, QR/Token administration and `mise run build:web-ui`.
The App's embedded Runtime remains loopback-only. Desktop broker/control and
physical-device qualification are separate gates.

## Validation

```sh
mise run fmt           # Explicit formatting fixes
mise run check         # Formatting, scripts, Rust workspace, Swift Helper build
mise run test          # Backend / protocol, no real model calls
mise run test:app
mise run test:executor # Guest receipt, idle expiry and cleanup contracts without a VM
mise run test:ui        # Portable shared state and localization (no GPUI feature)
mise exec -- cargo test --locked -p pot-ui --features presentation -j 4 # Shared application state
mise run licenses      # Enforce Rust license policy and generate dependency notices
mise run test:licenses # Check approved, disallowed and unknown license cases
mise run test:release   # ZIP metadata, permissions, symlinks and Cask checksum
mise run test:i18n     # Catalogue parity, native switching and embedded web assets
```

Current real CLI/VM acceptance (build and bundle first; no paid model calls):

```sh
mise exec -- python scripts/tests/sandbox-runtime.py --helper .local/d1/Pot.app/Contents/Helpers/PotHelper --resources .local/d1/Pot.app/Contents/Resources/runtime --output /tmp/pot-sandbox-acceptance.json
```

Apple VM networking requires Virtualization privileges. This starts two isolated Linux VMs with pinned Host pot-agent and local mock models, validates lazy startup, worker scope, Sandbox independence, cancellation, desktop and native resume, then cleans up. Dated results and limits are in [decoupling acceptance](validation/host-pot-agent-20261006.md).

### CI cache behavior

CI runs on pushes to `main` and release tags matching `v*.*.*`. Opening or updating
a pull request does not trigger CI.

CI installs only each job's declared mise tools and disables implicit installation
with `MISE_AUTO_INSTALL=false`. Rust caches are separated by OS, architecture and
actual compiler identity, prefer the current lockfile, and fall back to the same
compiler when dependencies change. Cargo still validates fingerprints and the
locked dependency graph. Swift caches are independent and include Xcode identity;
Rust dependency or task changes do not invalidate the Helper build cache.
CI disables Rust incremental compilation to reduce snapshot size. Successful
`main` jobs save build caches; release-tag jobs only restore them to avoid filling repository
cache storage with branch-specific multi-gigabyte snapshots. The license job caches
Cargo sources separately and always reruns the policy checks. Cache schema or
compiler changes require an initial cold build. All existing checks remain enabled.

### Git hooks

`mise run setup` installs repository-local hk pre-commit/pre-push hooks without changing global hooks. Run it once per clone. Git hooks load tools through mise without shell activation; mise itself must be on PATH.

- pre-commit: checks formatting/scripts for changed files; Rust formatting covers the workspace, and rule/check-entry changes trigger all lightweight checks. Also checks staged whitespace.
- pre-push: every push runs checks in four parallel groups: Rust, macOS formatting/Swift, script tests, and licenses. macOS runs one full-workspace Clippy pass with all targets, followed by backend, UI, App and deterministic eval tests. Clippy covers compilation checking, so the hook does not repeat `check:rust` or the backend-only Clippy pass. Linux runs backend Clippy and portable tests, skipping macOS-only checks. Cargo tasks run sequentially.
- Hooks only check; they neither fix nor stage. Source checks read the working tree, so partially staged content must also be formatted. Image/VM/paid model tests are excluded. A new machine may compile slowly; later runs reuse caches.
- Each pre-commit step and pre-push group prints its elapsed seconds and exit code, including failures. `scripts/timed-check` also appends UTC start/end times, elapsed seconds and exit code to the ignored `.local/hooks/timings.jsonl` in the current checkout. Groups run concurrently, so their durations must not be added to infer total hook time. A timing-log write failure is reported without changing the check's result. `mise run test:hooks` verifies the wrapper.

Manual hook checks: `mise exec -- hk run pre-commit --all` or `mise exec -- hk run pre-push --all`. Use `mise run check` for full local checks and `mise run fmt` for fixes. Base rules live in `ruff.toml`, `.swiftformat`, and `scripts/dev-check`; hooks select and invoke them.

Cargo commands run through mr-boxington (`mbx`) via mise's native Rust integration.
Use `mise run` tasks or `mise exec -- cargo ...`; calling `~/.cargo/bin/cargo`
directly bypasses this integration. `mise exec -- mbx doctor --json` checks the
active tools, and `mise exec -- mbx explain --last` explains the latest build's
cache results. Remove other compiler-cache wrappers before using mbx: an existing
`RUSTC_WRAPPER` or Cargo `build.rustc-wrapper` can bypass mbx's own compiler cache.
Native mise integration does not require `mbx setup` or a standalone Cargo shim;
inside `mise exec`, Cargo resolves to mise's command wrapper.

The shared mbx compiler store is separate from this checkout's explicit
`CARGO_TARGET_DIR` (`.local/poc/cargo-target`), which mbx leaves in place. Builds
reuse matching compiler outputs across checkouts; tests still execute normally.
The native toolchain is pinned to Rust 1.100 beta (`beta-2026-10-14`). This meets
mbx's Cargo requirement for dependency build seeding in managed targets; the
explicit target directory above continues to use compiler-action caching. Keep
the pinned nightly used by `build:web-ui` separate from this native toolchain.
Machine-wide wrapper configuration belongs in machine inventory.

## Code entry points

Rust workspace members and their bundled assets live under `crates/`. The browser client is `crates/web-ui/` (Cargo package `pot-web-ui`); build with `mise run build:web-ui`, producing `pot_web_ui.js` and `pot_web_ui_bg.wasm`. Shared localization build code lives in `crates/localization/`. Swift Helper, plugins, skills, scripts and release packaging remain outside the Cargo workspace.

| Directory | Responsibility |
|---|---|
| `crates/app/` | Native bootstrap, transport and platform adapter; Swift text input and system services |
| `crates/ui/` | Complete shared GPUI application, navigation, pages, configuration, composer, tokens and localization |
| `crates/web-ui/` | WASM bootstrap and browser adapter, authenticated transport, history, PWA and push |
| `helper/` | Swift Containerization, VM lifecycle |
| `crates/server/` | Standalone startup, HTTP/SSE/MCP/PTY routes, listener, shutdown |
| `crates/assistant/` | Assistant identity, instructions, conversations and durable mailboxes |
| `crates/relay/` | Cross-Control message forwarding through injected endpoints; currently App-only |
| `crates/runtime/` | Runtime composition and execution management |
| `crates/runtime-execution/` | Shared Agent invocation, concurrency and metadata tracing |
| `crates/core/` | Immutable Agent models, execution contracts, shared tool schema |
| `crates/storage/` | SQLite primitives, file-request ledger and transactions |
| `crates/agent/` | In-process pot-agent |
| `crates/tool-executor/` | Guest command sessions and patch implementation; explicit local fixtures for crates/eval/tests |
| `crates/gateway/` | Provider metadata and HTTP transport; encrypted credential references |
| `crates/protocol/` | Shared message and control types |
| `crates/sandbox-images/linux/default/` | Guest desktop, browser, broker/viewer resources |
| `scripts/` | Build, bundle, icon generation |

`poc/` is removed; experiment code/evidence remain in Git history. `.local/poc/` is a retained local build/download cache path without dependencies on old experiment sources.

Machine connection/deployment notes belong in `~/Workspace/Machines`. Do not commit credentials, user sessions, or build artifacts. Inspect live state through Runtime APIs.

### Supported integrations

Claude, Gemini, DeepSeek and OpenCode use API connections through pot-agent. pot-agent Host tools require the owner permission service; macOS/Windows provider qualification remain outside this increment. See [Agent permissions](permissions.md). Old guest deployment has no compatibility migration. Superseded configuration and engine formats are not imported.

## About and dependency licenses

The native Pot menu includes About Pot with version, developer, MIT, project link,
and Rust dependency notices. Bundling enforces the cargo-about policy and embeds
its report. See [About and license policy](licenses-and-about.md) for scope,
reviewed MPL dependencies, and CI checks.

macOS prepared-instance setup and qualification limits: [macOS Sandbox](macos-sandbox.md). Non-VM checks: `mise exec -- python scripts/tests/macos-provider.py` and `mise exec -- python scripts/tests/macos-helper.py --helper .local/d1/helper-build/release/PotMacOSHelper`.

## Separate Sandbox resources

Client bundling does not build or embed Linux Sandbox resources. Use `mise run build:sandbox-image`, then `mise exec -- python scripts/export-sandbox-resources --update-catalog` before client compilation. `mise run export:sandbox-resources` checks an export against the embedded catalog. Offline VM fixtures also require `mise exec -- cargo build -p pot-sandbox-images` to provide the verified importer. See [resource delivery](sandbox-resources.md) for tag publication, cache/approval behavior and pinned-instance compatibility.

### Multiple browser clients

`mise run test:clients` runs real loopback HTTP listeners with production Runtime,
Access, storage and SSE routes. Independent browser identities and shared Tokens exercise configuration,
Token rotation/local clearing, simultaneous request-ID retries, conflicting reuse, busy-turn retry,
owner/browser state consistency, streams/reconnects, global Token rotation and
restart persistence/durable receipts. A local deterministic model fixture is used;
no user keys, containers or paid model endpoints are required. The suite is included
in `mise run test` and the Linux backend CI job. Rootless Podman qualification is
manual on an explicitly selected Linux host; no Podman integration CI was added.


### Desktop web assets

Edit `desktop/index.template.html`, `crates/sandbox-images/linux/default/index.template.html`,
and the shared `desktop/i18n.js`; translations belong in `crates/app/locales/*.json`.
Run `mise run build:web` to inspect generated assets under `.local/web/`.
Control Cargo builds generate and embed their own fresh assets automatically.
Image/prepare entry points also run the generator and copy the Sandbox output.
Use `mise run test:i18n` to verify catalog parity, template defaults, independent
resources, asynchronous startup and automatic third-language discovery.

### Brand assets

Run `mise run icons` on macOS to regenerate the ICNS, application/README PNGs,
opaque maskable Home Screen PNGs, monochrome menu-bar exports, the shared avatar
mask (`crates/avatar/assets/PotMask.png`) and Sandbox wallpaper.
The generator links `crates/app/swift/PotIcon.swift`, the same vector master used by
native navigation and menu-bar rendering. See [brand design](design/pot-brand/README.md).
