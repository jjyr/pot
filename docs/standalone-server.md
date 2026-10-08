# Standalone CLI and embedded App

For Agent-assisted Web UI deployment and HTTPS/HTTP access-mode selection, see
the [Web UI deployment guide](web-ui-deployment.md). Remote browser access requires HTTPS.

Pot has one backend with two hosts: `pot serve` runs it independently; the macOS
App owns it inside its own process. App requests, including the Swift settings
adapter, dispatch in memory. App does not connect to a daemon or management
socket. Closing App shuts down its backend before the Sandbox Helper.

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
`http://localhost:17701`. Run `pot open` for automatic browser configuration,
or `pot access token` to configure the browser manually. `pot access qr` prints
a connection QR containing the Token; do not share it.
Localhost HTTP is a local secure-context
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
pot tasks interrupt TASK_ID
pot tasks cancel TASK_ID
pot api /api/v1/snapshot
```

Configuration files use the existing Runtime API request schemas; see
[Runtime contract](../runtime/README.md). Secrets stay in private files, not
command arguments. `chat` prints the accepted message receipt, not an invented
model result; query snapshots/tasks for subsequent output. `pot api PATH
[JSON_FILE]` supports existing local queries and mutations. Browser route
permissions remain restricted; changing authentication does not enable remote
provider administration or Terminal access. Paired owner clients can open a
qualified Sandbox desktop through login-bound viewer tickets.

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
There is no direct Host execution fallback. Linux uses the Podman Provider
described below; Windows support is not delivered.

## Background service

On macOS, `pot service install` writes a per-user LaunchAgent definition;
`pot service start` starts it, and `stop`/`uninstall` manage it. Installation does
not auto-start, requires no root, and does not overwrite an existing service
configuration. This is an optional independent CLI host, not an App dependency.
App owns its own backend lifecycle and never starts this service.

## Authorization

See [Browser access](mobile-access.md). One persistent random Token authorizes
all browsers. Browsers save it locally and send it on API requests. The owner CLI
can show/regenerate it. Regeneration invalidates all browsers and active streams/desktops.
There is no individual browser authorization list.

## Optional embedded Tailscale

Release archives include libtailscale. Save `{"POT_TAILSCALE_HOSTNAME":"pot-server"}`
with `pot config server FILE`, then restart. Without that setting, existing
localhost and manual HTTPS modes retain their behavior. Run `pot access connect`,
inspect `pot access status` for the login URL/device approval state, complete login,
and run `pot access enable`. Enable MagicDNS and HTTPS certificates in the tailnet
DNS settings. The actual node DNS name is served on HTTPS port 443. Certificate
issuance/renewal is automatic; access is tailnet-only, without Funnel.
`pot access disconnect` closes access and retains the node identity.

Source builds use `mise run build:tailscale`. Binary-only installations still support
localhost/manual HTTPS. `scripts/install-cli --tailscale-library FILE` adds the
built `.local/tailscale/libpot_tailscale.dylib` (Linux: `.so`).

## Linux host and Podman

Linux Server registers `linux.podman` (`LinuxPodmanProvider`). macOS registers
`macos.apple-containerization` (`MacOSAppleContainerProvider`, Linux guest) and
`macos.apple-virtualization` (`MacOSAppleVMProvider`, macOS guest). Provider
names identify the **host** OS and engine; `os` identifies the guest. No legacy
Provider IDs are accepted. Existing definitions must be edited while Server is
stopped; changing the Apple Containerization ID does not change task IDs or data.

Linux execution requires rootless Podman, cgroup v2 with systemd delegation,
subordinate UID/GID ranges, and pasta (passt). Pot invokes the local Podman CLI;
no Podman API socket/service is used. Host execution is never a fallback.

```sh
mise run build:container-image
mise run build:cli
pot init
pot serve
```

The image build compiles a target musl executor and uses the shared Dockerfile.
Use `scripts/build-container-image --arch amd64|arm64 --tag IMAGE` through mise
for another target/tag. Cross-platform image RUN steps require an appropriate
native builder or configured emulation. `POT_PODMAN_IMAGE` selects an already
installed image (default `localhost/pot-sandbox:0.1.0`); first preparation pins
its local immutable image ID per environment. Starting an environment never
pulls an image or follows a changed tag. Install/build images as the same user
running Server. Updating an environment's pinned image requires a new Sandbox.

The root broker runs as root **inside the user namespace**, with SYS_ADMIN for
private skill mounts; execution children drop privileges and set no_new_privs.
Only that environment's `/data` workspace, global user skills and read-only
builtin skills are mounted. Host product state and Podman sockets are not mounted.
The executor port is published only on loopback with a private per-boot token;
pasta disables gateway-to-host mapping, and `--no-hosts` prevents Podman from
adding a host-loopback alias. Container cgroups expose only
the private delegated subtree; writable containment is checked before readiness.
Stopping or orderly Server shutdown removes containers, preserving workspace.
The image's pinned tini init reaps orphan descendants after cgroup cancellation.
Unexpected restarts change the observed boot generation and invalidate sessions;
commands with unknown outcomes are not replayed. Podman desktop viewing, exclusive
input takeover, clipboard, browser launch and cgroup metrics use the private
broker behind the Server gateway. Its port is published only on loopback, and
no broker credentials reach the browser. The Web Sandbox page opens a separate
viewer tab; closing it releases its ticket and keeps the container running.
CPU/memory limits are enforced; disk quotas are unavailable and
`disk_gib` must be `0` (unlimited), rather than claiming a working quota.

For a user service, save a unit such as this under
`~/.config/systemd/user/pot.service`, replacing both absolute paths:

```ini
[Unit]
Description=Pot Server
After=network-online.target

[Service]
ExecStart=/absolute/prefix/bin/pot --root /absolute/instance serve
Restart=on-failure
Delegate=yes

[Install]
WantedBy=default.target
```

Use `systemctl --user daemon-reload` and `systemctl --user enable --now pot`.
`pot service` still manages macOS LaunchAgents only. Do not add service restrictions
that disable user namespaces or mount/cgroup operations needed by rootless Podman.
Configure browsers with `pot access token` or scan `pot access qr`. For access from another machine,
use the documented owner-terminated HTTPS endpoint or an SSH local tunnel to
localhost; raw remote HTTP is refused. Model configuration remains owner CLI-only.
