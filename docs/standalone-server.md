# Standalone CLI and embedded App

Pot has one backend with two hosts: `pot serve` runs it independently; the macOS
App owns it inside its own process. App requests, including the Swift settings
adapter, dispatch in memory. Its local requests do not use a daemon or management socket. Additional
remote instances connect through the signed access endpoint; see
[Multi-Server client](multi-server-client.md). Closing App shuts down its backend before the Sandbox Helper.

## Install and run

The release CLI embeds the matching Web UI; users need neither a repository nor
Rust/Node/WASM build tools. Extract a CLI archive into a chosen prefix and add
its `bin` directory to PATH. A binary-only installation is also supported.
Building a release from source uses the pinned toolchains:

```sh
mise exec -- python scripts/build-cli
mise exec -- python scripts/install-cli .local/poc/cargo-target/release/pot --prefix ~/.local
pot init
pot serve
```

`pot serve` runs in the foreground and serves local Web UI at
`http://localhost:17701`. Run `pot access pair` in another terminal and enter the
printed one-use code in the browser. `pot open` opens the instance URL; `pot
access qr` prints only that URL as a QR. Localhost HTTP is a local secure-context
convenience, not remote encryption: remote access requires owner-terminated
HTTPS. HTTP listeners cannot bind outside loopback.

Use `pot --root /absolute/instance/path ...` for a separate instance. Defaults
are `~/Library/Application Support/pot` on macOS and `$XDG_DATA_HOME/pot` (or
`~/.local/share/pot`) on Linux. Data paths do not depend on the current directory.
App and CLI cannot open the same product directory concurrently: an OS lock
fails startup instead of silently sharing SQLite or attaching to another host.
Different instances also need different listener ports.

Standalone management uses `product/control.sock`, mode 0600 inside a private
product directory. CLI injects no user-entered credentials and never prints the
internal bearer. OS access to this socket is full owner authority. Requests
carrying browser Origin headers are rejected. The App has no such socket.
There is still an authenticated loopback integration listener for external
Codex/MCP/model subprocesses. In App mode its routes are restricted to these
integrations; the App UI cannot use it to access snapshots/settings.

## Configure and operate without App

```sh
pot status
pot doctor
pot config get providers
pot config set providers /secure/provider-request.json
pot config set agent /secure/agent-request.json
pot config get routing
pot config set routing /secure/routing-request.json
pot chat "Inspect the project"
pot tasks list
pot tasks show TASK_ID
pot tasks cancel TASK_ID
pot api /api/v1/snapshot
```

Configuration files use the existing Runtime API request schemas; see
[Runtime contract](../runtime/README.md). Secrets stay in private files, not
command arguments. `chat` prints the accepted message receipt, not an invented
model result; query snapshots/tasks for subsequent output. `pot api PATH
[JSON_FILE]` supports existing local queries and mutations. Pairing grants instance owner access, including remote provider/environment
management and desktop-session APIs. The browser interface remains smaller
than the native App; there are no permission tiers.

`pot config server /secure/listener-settings.json` saves supported `POT_ACCESS_*`
settings in `server.json` for both hosts. Environment overrides this file.
Restart the host to apply changed listeners/certificates. Remote example:

```json
{
  "POT_ACCESS_ORIGIN": "https://pot.example.com",
  "POT_ACCESS_BIND": "127.0.0.1:17701",
  "POT_ACCESS_CERT": "/secure/fullchain.pem",
  "POT_ACCESS_KEY": "/secure/private.key"
}
```

Keep private keys mode 0600. A relay must pass TLS bytes through unchanged;
third-party TLS termination does not meet the owner-endpoint encryption model.
No DNS, certificates, public ports or relay deployment is provisioned for you.

## Sandbox provider and optional engines

Backend/Web UI installation is independent of App. Actual environment execution
requires a supported provider. On macOS, an optional distribution may include
`lib/pot/PotHelper` and `lib/pot/runtime` (signed/entitled Helper, kernel, Sandbox
images and optional Node/Codex runtime). `pot serve` discovers this package and
starts Helper with `--sandbox-only`; Helper does not launch another Server.
Backend shutdown settles operations before terminating Helper.

Build with `scripts/build-cli --runtime-resources DIR --helper FILE`, or install
with the corresponding `scripts/install-cli` options. The release provider
resources must be built and qualified separately; a binary-only release does
not claim VM execution. `POT_RUNTIME_RESOURCES` and `POT_HELPER` are development
overrides. Codex requires its native runtime; pot-agent is built into Server.
There is no direct Host execution fallback. Linux provider and Windows support
are not delivered by the CLI packaging work.

## Background service

On macOS, `pot service install` writes a per-user LaunchAgent definition;
`pot service start` starts it, and `stop`/`uninstall` manage it. Installation does
not auto-start, requires no root, and does not overwrite an existing service
configuration. This is an optional independent CLI host, not an App dependency.
App owns its own backend lifecycle and never starts this service.

## Authorization

See [Browser access](mobile-access.md). Owner-generated pairing codes replace
WebAuthn. The browser stores an origin-scoped non-exportable P-256 signing key
in IndexedDB. Server persists public keys, names and subscriptions in
`access-clients.json`; short sessions and challenges remain in memory. Existing
old authentication records are not granted new authority. Re-pair old clients;
previously disabled access remains disabled. Old files are retained for history,
not read as credentials.
