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

CLI archives include the target Go CLI, pinned Node, tool plugins and Skills.
Linux Node requires a glibc-based host. Install any tools required by your selected
plugins, including Chrome/Chromium for browser execution. Runtime creates no VMs
or containers. Keep private data roots on a local filesystem supporting Unix
sockets and file locks. See [Development](development.md) for packaging.
Upgrade Control, Runners and App from the same revision. Manual upgrades can
apply pending [database migrations](database-migrations.md); stop the old
processes and back up the complete data root before schema-changing upgrades.
The chat UI can update one local installation and its running hosts; see
[Updates](updates.md). Remote installations require separate coordination.

The default Go root is `~/.pot-go` on macOS and Linux. Use `-root /absolute/path`
for an independent instance. New roots require empty directories and receive a
stable Runtime identity. App and standalone Runtime cannot open the same root
concurrently.
Keep each root private and preserve its identity and secret master key across
restarts. See [Secrets](secrets.md).

Tool packages live under `Resources/plugins/`, with a `plugin.json` manifest and
explicit declared files. Stop Runtime before replacing complete packages. See
[Plugin packages](plugins.md). Main and default Tasks use the owning Control's
local filesystem; explicit remote Tasks use their destination Runtime.

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

Control runs unspecified Tasks on its own Runtime. Select an enrolled Runner
explicitly with `machine_id`; labels describe machines and support explicit
filtering. Set labels through the owner API:

```sh
pot api -root "$HOME/.pot-control" -method POST \
  -body '{"action":"set_tags","runtime_id":"RUNNER-ID","tags":["build","linux"]}' \
  /api/v1/runtime/control
```

Labels never grant capability or change default placement. Configure execution authorization through
Settings → Execution permissions or `POST /api/v1/permissions` with the current
policy revision. `execution_access: "ask"` runs ordinary work in SRT and reviews
explicit host requests; `allow` trusts this machine and bypasses SRT. See
[Permissions](permissions.md) for exact command rules and application state.

## Advanced local resources

`GET /api/v1/runtime/resources` reports assistant definitions and durable owner
command receipts. The POST interface supports only `action: "create"`,
`kind: "assistant"`, a stable `request_id`, and assistant creation fields.
Machine authorization uses [Permissions](permissions.md), not resource creation.
VM definitions, mappings, image preparation and lifecycle commands are absent.
Unknown commands and obsolete fields fail without modifying stored data.

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

## Agent message access

The topology page manages Agent Links separately from third-party accounts and
Runner enrollment. Each Control starts a dedicated loopback message listener;
the page shows its actual bind address. Use its access setup to verify an existing
HTTPS reverse proxy or explicitly configure Tailscale Serve. The public origin
must route to this message listener, not the owner API. Setup probes the endpoint
and checks the returned Control identity before saving it. Merely opening the
page does not change network configuration. Existing unrelated Serve mappings
are never overwritten.

Copying an Agent address creates a short-lived, single-use invitation. Treat the
complete address as a credential and paste it only into the other Agent's
connection page. Ordinary status displays the nonsecret endpoint. The receiving
side validates the invitation and binds it to the selected assistant. A
connection grants mutual messaging only; assistant execution keeps its existing
permissions. See [Agent Links](agent-links.md).

Adding a machine uses a separate Control membership address and pinned identity.
If a listener binds all interfaces, the product cannot infer its public origin:
enter the reachable Control URL in the add-machine flow. It is verified against
the selected Control identity before an invitation is issued. Cancelling an
unused invitation revokes that code; cancellation after successful enrollment
keeps the joined machine.
