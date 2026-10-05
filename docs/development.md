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

`bundle` preinstalls dependencies in a separate VM and builds the complete Sandbox OCI image. Software downloads happen only during build; App loads the image directly at startup. See [Sandbox image](sandbox-image.md) for caches, fonts, and sizes.

The development bundle is `.local/d1/Pot.app`. `scripts/pot open` uses `.local/d1/local-runtime` (overridable with `POT_LOCAL_ROOT`); double-clicking defaults to `~/Library/Application Support/pot`. These use separate data.

`configure-pi` imports host Pi DeepSeek credentials into private guest configuration without model calls or embedding keys in App. The model is `deepseek-v4-flash`; see [UI specification](ui-ux-design.md#allowance-configuration) for configuration, default allowances, and K/M token input rules. Existing configuration is not overwritten automatically; the usage ledger is not reset.

## Standalone Server (development entry point)

Build and start the backend without App/Helper. Paths belong to the Server machine. Use a separate data directory rather than opening active App/VM data.

```sh
mise exec -- cargo build --locked -p pot-server
POT_DATA_DIR=/tmp/pot-server/product POT_WORKSPACE=/tmp/pot-server/workspace POT_RUNTIME_BIND=127.0.0.1:17700 mise run server
```

`POT_PI_BIN` selects the Pi executable. Server writes private bearer/boot/port data to `runtime.json` in its data directory. HTTP/SSE/MCP/PTY retain the existing Control API. Without a configured Agent, main chat rejects sends while Terminal remains available. Shutdown stops background coordinators, sessions, PTYs, Codex hosts, and SSE.

App retains local Helper/VM discovery without remote endpoint settings. Cross-machine deployment still needs TLS, identity, and artifact transfer; this change establishes only independent backend execution boundaries.

## Validation

```sh
mise run fmt           # Explicit formatting fixes
mise run check         # Formatting, scripts, Rust workspace, Swift Helper build
mise run test          # Backend / protocol, no real model calls
mise run test:app
POT_TEST_PI_BIN="$(command -v pi)" mise exec -- cargo test --locked -p pot-runtime real_pi -- --ignored
```

The final command uses host-installed Pi and a local mock model without paid calls. See the [Documentation index](README.md) for real App/VM/model results; old POC fixtures do not establish current product acceptance.

### Codex validation

Users needing authentication run the official CLI login command in the independent Terminal (Codex supports
`codex login --device-auth`); Pot only checks status in Settings -> Agent.
Enter an account-supported model in Settings -> Assistant and explicitly select Codex.
Only the guest CLI manages login state, stored in persistent engine-home.

Credential-free integration test (explicitly select an installed official Codex CLI; no paid model calls):

```sh
mise exec -- cargo build -p pot-server
mise exec -- python scripts/tests/codex-runtime.py --runtime .local/poc/cargo-target/debug/pot-server --codex /absolute/path/to/codex
```

After building the latest image, run the same contract in an isolated VM without networking:

```sh
codesign --force --sign - --entitlements helper/virtualization.entitlements .local/d1/helper-build/release/PotImageCheck
.local/d1/helper-build/release/PotImageCheck .local/d1/codex-guest-check .local/d1/resources scripts/tests codex-guest.sh
```

Use a new result directory for each cold validation. This tests real CLI behavior against a local mock model, not real login/model acceptance.

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
| `driver/` | Native Pi/Codex/Claude/AGY execution adapters |
| `gateway/` | Provider HTTP transport and model protocol codecs |
| `protocol/` | Shared message and control types |
| `image/` | Guest desktop, browser, broker/viewer resources |
| `scripts/` | Build, bundle, configuration import, icon generation |

`poc/` is removed; experiment code/evidence remain in Git history. `.local/poc/` is a retained local build/download cache path without dependencies on old experiment sources.

Machine connection/deployment notes belong in `~/Workspace/Machines`. Do not commit credentials, user sessions, or build artifacts. Inspect live state through Runtime APIs, not host access to active guest SQLite.

### Claude offline contract

Pot checks Claude status only through official interfaces; users authenticate in the normal Terminal themselves.
Enter a model in Assistant settings and explicitly select Claude. Real Claude account testing is skipped by user decision (no account).
Account-free development validation uses the original CLI and a local mock Anthropic service:

```sh
mise exec -- python scripts/tests/claude-runtime.py --runtime .local/poc/cargo-target/debug/pot-server --claude /absolute/path/to/claude
.local/d1/helper-build/release/PotImageCheck .local/d1/claude-guest-check .local/d1/resources scripts/tests claude-guest.sh
```

Guest checks require the Helper signing/VM prerequisites above. Tests use an isolated temporary HOME without host credentials.


### Terminal process cleanup (Linux guest)

```sh
.local/d1/helper-build/release/PotImageCheck .local/d1/terminal-guest-check .local/d1/resources scripts/tests terminal-guest.sh
```

Use a new isolated result directory and the Helper signing above. Validate PTY close, normal Runtime exit, and restart;
cover foreground sleep, separate background process groups, and HUP/TERM-resistant background shells/grandchildren without model calls.
Processes deliberately detached with setsid are outside this contract and remain subject to VM lifecycle termination.


### AGY Driver regression

AGY integrates the original CLI. Main chat, background tasks, schedules, and summaries are enabled by user decision, without Pot orchestration tools; roles are constrained by prompts.
Users log in through Terminal; status remains unknown. Account-free mock transport regression:

```sh
mise exec -- cargo build --locked -p pot-server
mise exec -- python scripts/tests/agy-runtime.py --runtime .local/poc/cargo-target/debug/pot-server
```

Use the Helper signing above and a fresh offline guest directory to validate the same regression in the packaged Runtime,
and original CLI help/headless failure with an empty HOME:

```sh
.local/d1/helper-build/release/PotImageCheck .local/d1/agy-guest-check .local/d1/resources scripts/tests agy-guest.sh
```

Mock transport does not prove original CLI model/tool behavior; an original offline failure smoke test does not establish real account or role isolation acceptance.
