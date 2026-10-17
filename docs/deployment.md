# Deployment

Pot has one Go backend with two hosts: `pot serve` runs standalone Runtime; the
macOS Wails App composes its local Runtime in process. The App's main window and
tray use direct bindings and the shared frontend. An independent
`web-ui-server` process publishes browser access to one explicitly selected
Control. See [Architecture](architecture.md) for ownership and failure rules.

## Install and data roots

Use the Go CLI archive built for the target OS/architecture, or build from a
pinned checkout using the toolchains in `mise.toml`:

```sh
mise install
mise run build:cli
```

CLI archives include the Go executables, installed Provider packages and retained
runtime assets. The macOS Apple VM package includes its signed Swift Helper. Linux Sandbox execution
uses Podman on both hosts and separately prepared, verified OCI image resources. See
[Development](development.md) for artifact layout and build options. Upgrade
Control, Runners and App from the same revision.

Linux CLI archives bundle the official GNU/Linux Node runtime and require a
glibc-based host for JavaScript plugins. The default managed Linux Sandbox uses
Debian 13 (`trixie-slim`) and packages the same checksum-pinned official Node
release for its guest architecture. See [Sandbox image](sandbox-image.md) for
base-image and package-snapshot pins. Keep Runtime data roots on a local
filesystem supporting private Unix sockets and file locks; a Sandbox's shared
workspace is not a Runtime data root.

The default Go root is `~/.pot-go` on macOS and Linux. Use `-root /absolute/path`
for an independent instance. New roots require empty directories and receive a
stable Runtime identity. App and standalone Runtime cannot open the same root
concurrently.
Keep each root private and preserve its identity and secret master key across
restarts. See [Secrets](secrets.md).

Tool, Provider and resource capabilities share Plugin packages under
`Resources/plugins/`, using one `plugin.json` manifest. Stop Runtime before
replacing a complete package and retain catalog records referenced by existing
Sandbox pins. Providers and image catalogs can be updated independently of Pot
core. Missing provider packages leave those Sandbox targets unavailable, with no
Host fallback. See [Plugin packages](plugins.md).

## Linux Sandbox runtime

Install Podman for the account running Pot. On macOS, use `brew install podman`;
on Linux, install the distribution's rootless Podman packages with cgroup v2 and
systemd support. Host-only Runtime operation does not require a running Sandbox.

Both hosts use the `podman` provider and the architecture-specific OCI bundle in
the installed Podman plugin catalog. Start/Rebuild prepares missing resources through the
[owner download confirmation](sandbox-resources.md#consent-progress-and-cache); an arbitrary
locally tagged image cannot replace that verified bundle. Offline provisioning
uses `pot resources import` as documented in [resource delivery](sandbox-resources.md).

On macOS, Pot creates and selects a dedicated Podman machine for its Runtime
execution root. Its initial capacity is 4 CPUs, 8 GiB memory and 50 GiB disk.
These machine settings are distinct from per-container CPU/memory limits, and
there is no public machine-sizing option. Each Sandbox uses `disk_gib: 0` for
shared managed capacity. Pot neither adopts the user's default machine nor
reconfigures an existing shared machine to satisfy one container request.
The first explicit prepare/start can also download the digest-pinned official
Podman machine OS using the `applehv` backend. This is a runtime prerequisite
separate from Pot's Debian image catalog; its transfer is not included in the
catalog's image consent byte count. Prepare it as well for offline operation.

Machine state lives under the execution root's `podman-machine` directory.
Workspace data lives under `podman-shared`, shared into the machine at
`/mnt/pot/shared`; product databases and tokens stay outside it. Machine shares
also include exact trusted Skill roots and any explicit mapping roots fixed at
first initialization. A later Host mapping outside those pinned roots fails
explicitly; Pot does not restart sibling containers to add it.
Lifecycle and cleanup boundaries are defined in [Providers](sandbox-providers.md).

## Standalone Control

Initialize a dedicated root, then start a foreground listener:

```sh
pot init -root "$HOME/.pot-control"
pot serve -root "$HOME/.pot-control" -listen 127.0.0.1:19443
```

For HTTPS, supply both `-tls-cert /absolute/control.crt` and
`-tls-key /absolute/control.key`. The listener address is explicit; choose the
intended LAN/VPN/Tailscale interface when clients run elsewhere. HTTP does not
encrypt credentials or content. Deployment network protection and TLS are
operator choices.

A listener enables standalone Control but creates no assistant. Configure a
Provider through App; first-use Chat then bootstraps Pot automatically. API callers may use
`POST /api/v1/assistants`; it requires a stable `request_id` and a `name`, and
optionally an explicit assistant model selection and `locale` (`en` or `zh-CN`,
default English) for its persisted welcome. `POST /api/v1/assistants/bootstrap`
accepts `locale` and returns `assistant: null` without a default Provider, otherwise
the first existing assistant or a newly created Pot. Saving Providers alone never
creates conversational identities; Chat initiates the separate bootstrap request.

The private CLI socket authenticates operator requests without exposing its
internal token:

```sh
pot status -root "$HOME/.pot-control"
pot api -root "$HOME/.pot-control" /api/v1/runtime/control
pot api -root "$HOME/.pot-control" -method POST \
  -body '{"action":"owner_credential"}' /api/v1/runtime/control
```

The last command exports the standalone Control's owner credential for a native
remote Space or browser upstream. Store the credential string in an owner-only
file when using `-control-token-file`; this credential is separate from Runner
invitations and browser access tokens.

Listener and resource paths can be persisted without changing the running process:

```json
{
  "listen": "127.0.0.1:19443",
  "tls_cert": "",
  "tls_key": "",
  "workspace": "/absolute/agent-workspace",
  "resources": "/absolute/Resources"
}
```

```sh
pot config server /absolute/server-settings.json -root "$HOME/.pot-control"
```

Initialize the Go root first. Settings are private and typed; file/directory paths
must be absolute, and TLS paths must be configured together. Explicit startup
flags override saved values. Restart to apply changes.

## Runner membership

Run the same CLI on the execution machine with a separate root:

```sh
pot init -root "$HOME/.pot-runner"
pot serve -root "$HOME/.pot-runner"
```

Create a one-use invitation on Control:

```sh
pot api -root "$HOME/.pot-control" -method POST \
  -body '{"action":"create_invitation"}' /api/v1/runtime/control
```

On Runner, preview the destination:

```sh
pot api -root "$HOME/.pot-runner" -method POST \
  -body '{"action":"preview","url":"https://control.example:19443"}' \
  /api/v1/runtime/control
```

Verify the returned Runtime ID and fingerprint. Submit a join request using those
exact values and the invitation:

```json
{
  "action": "join",
  "request": {
    "request_id": "runner-join-1",
    "url": "https://control.example:19443",
    "invitation": "INVITATION",
    "expected_runtime_id": "CONTROL-ID",
    "expected_fingerprint": "CONFIRMED-FINGERPRINT"
  }
}
```

```sh
pot api -root "$HOME/.pot-runner" -method POST -body - \
  /api/v1/runtime/control < /absolute/join.json
```

For a private HTTPS CA, include its PEM contents as `ca_pem` in both preview and
nested join requests. Ordinary certificate and hostname verification remain
active. The CA persists with membership for reconnects. HTTP destinations are
also supported when explicitly selected.

Runner reconnects outward over signed WebSocket RPC and needs no inbound
execution listener, assistant or provider key. A Runtime already owning
assistants/AI configuration cannot join. Keep its root to retain identity and
membership; re-enrollment creates a new epoch. `remove_member` on Control uses
`runtime_id`; `leave` on Runner fences reconnect and confirms original cleanup
before removing membership. Unknown execution outcomes are not replayed.

Control routes unspecified Tasks only to machines labelled `task-runner`,
preferring its own Runtime then stable Runtime ID order. Set labels explicitly:

```sh
pot api -root "$HOME/.pot-control" -method POST \
  -body '{"action":"set_tags","runtime_id":"RUNNER-ID","tags":["task-runner","linux"]}' \
  /api/v1/runtime/control
```

Labels never grant capability. Choose each machine's Host/Sandbox policy through
Machines or `POST /api/v1/permissions`. Host operations request review under
`host_access: "ask"`; `allow` is an explicit machine-wide operator policy.

## Advanced local resources

`GET /api/v1/runtime/resources` lists assistant definitions, Host policy,
installed Sandbox Providers, definitions, selection and recent owner-command
receipts. This is a native/private owner API; browser sessions cannot use it.

Mutations use `POST` with a stable `request_id`, `action` and `kind`. Exact retries
return the original response, even after intervening policy edits. A pending
command after process failure blocks conflicting replacement and requires
inspection rather than automatic replay.

| Action | Kind | Additional fields |
|---|---|---|
| `create` | `assistant` | `name`, or a complete `assistant` creation request |
| `create` | `host` | Optional `policy`; defaults to explicit Host mode with command review |
| `create` | `sandbox` | Stable `sandbox_id`, `name`, installed `provider`, optional `resources` |
| `select` | `sandbox` | `sandbox_id`; affects later policy-resolved executions |
| `update` | `sandbox` | `sandbox_id`, `resources` |
| `mappings` | `sandbox` | `sandbox_id`, explicit `mappings` list; empty clears it |
| `start`, `stop`, `recover`, `rebuild`, `delete` | `sandbox` | `sandbox_id` |
| `status` | `sandbox` | `sandbox_id`; read-only, no command receipt required |

Creating/selecting a Sandbox does not boot it. Creation retries cannot overwrite
another definition; retired IDs cannot be reused. Deletion checks retained Task
artifacts, current schedule references, active leases and unresolved receipts.
Resource and mapping updates require the Provider's stopped-state conditions.
Remote Control commands cannot create/select a Runner's definitions; use that
Runner's local owner socket for these operations.

Example Linux definition:

```json
{
  "request_id": "sandbox-create-1",
  "action": "create",
  "kind": "sandbox",
  "sandbox_id": "development",
  "name": "Development",
  "provider": "podman",
  "resources": {"cpus": 4, "memory_gib": 8, "disk_gib": 0}
}
```

The `podman` provider is the same on macOS and Linux; prepared native macOS VMs
use `macos.apple-virtualization`. See [Providers](sandbox-providers.md),
[macOS Sandbox](macos-sandbox.md), [Host mappings](sandbox-image.md) and
[resource delivery](sandbox-resources.md).

## Independent browser service

A browser service root is distinct from a Runtime root. Explicit local serving:

```sh
pot web-ui-server -root "$HOME/.pot-web" -control local \
  -control-root "$HOME/.pot-control" -listen 127.0.0.1:17703 \
  -origin http://127.0.0.1:17703
```

The local target must be a running standalone Control. Remote serving uses
`-control https://control.example:19443 -control-token-file /absolute/owner-token`
and optionally `-control-ca /absolute/ca.pem`. Upstream authentication and identity
are checked before serving. The browser's public origin, listener TLS and upstream
Control transport are independent. Browser TLS uses `-tls-cert` and `-tls-key`.

Manage access through the browser service's private operator socket:

```sh
pot access -browser-root "$HOME/.pot-web" status
pot access -browser-root "$HOME/.pot-web" enable
pot access -browser-root "$HOME/.pot-web" token
pot open -root "$HOME/.pot-web"
```

`regenerate-token`, `disable`, `connect`, `disconnect` and `qr` use the same
operator scope. Embedded Tailscale belongs to this browser service. The browser
uses its access token to establish server-managed sessions; upstream owner keys
never reach browser JavaScript. Rotation/disabling invalidates existing sessions
and streams. Logout removes the active session. Browser policy excludes native
membership, advanced resource and owner credential interfaces.

## Everyday CLI and services

Private owner clients can create an interactive Host shell with
`POST /api/v1/terminals?assistant_id=OWNER` and `{"cols":80,"rows":24}`.
The response fixes an ephemeral terminal ID and Runtime `boot_id`; connect to
`/api/v1/terminals/ID/socket?assistant_id=OWNER` with the owner bearer credential.
Binary WebSocket frames carry PTY bytes; text frames resize with `cols` and
`rows`. An exit sends `{"exit_code":CODE}`. Disconnecting detaches; reconnecting
replays at most 64 KiB. Explicit `POST /api/v1/terminals/ID/close` ends the shell.
The owning assistant's Host workspace is the starting directory. There are at
most eight sessions per assistant. Runtime shutdown closes them; restart does
not recover terminal IDs or bytes. Browser origins, published browser clients
and execution-only Runners cannot use this interface. No model command is
accepted at creation, and terminal bytes are never stored in product databases,
transcripts or usage records.

```sh
pot chat -root "$HOME/.pot-control" -assistant main "Investigate the build"
pot tasks list -root "$HOME/.pot-control" -assistant main
pot tasks show TASK-ID -root "$HOME/.pot-control" -assistant main
pot tasks interrupt TASK-ID -root "$HOME/.pot-control" -assistant main
pot tasks cancel TASK-ID -root "$HOME/.pot-control" -assistant main
pot doctor -root "$HOME/.pot-control"
pot config get agent -root "$HOME/.pot-control" -assistant main
```

Configuration sections are `providers`, `agent`, `routing`, `usage-policy` and
`instructions`. `config set SECTION JSON_FILE` uses the same revision checks as
UI/API callers. Agent configuration accepts an editable `AssistantSelection` or
the complete save request; it never writes a resolved Task snapshot back into a
Provider. Routing stores only profile/provider concurrency limits.

`pot service install|start|stop|uninstall -root PATH` manages a user service on
macOS or Linux. Install writes a reviewable definition without overwriting an
existing one. Start/stop act on that definition, and the recorded root must match.
Use the same account/root when restarting after upgrades. Neither building nor
installing an archive starts a VM or installs a user service.
