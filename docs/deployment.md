# Deployment

Pot has one Go backend with two hosts: `pot serve` runs standalone Runtime; the
macOS Wails App composes its local Runtime in process. The App's main window and
tray use direct bindings and the shared frontend. An independent
`web-ui-server` process publishes browser access to one explicitly selected
local node. See [Architecture](architecture.md) for ownership and failure rules.

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
Upgrade each node independently from a compatible protocol revision. Manual upgrades can
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
[Plugin packages](plugins.md). Main and Tasks use their owning node's local filesystem. Remote work is delegated
through Agent Link to an autonomous node.

## Standalone node

Initialize a private root and run the node:

```sh
pot init -root "$HOME/.pot-node"
pot serve -root "$HOME/.pot-node" -peer-listen 100.64.0.10:19444
```

`-peer-listen` binds authenticated Agent Link TCP. Use a private LAN, VPN or
Tailscale endpoint. TLS 1.3 encrypts traffic and proves possession of both
Ed25519 identity keys, using automatically generated self-signed certificates
and public-key pins. No CA or certificate-file configuration is needed. The
listener grants no remote owner, membership, model proxy or raw execution access.

The private local socket serves CLI operator requests:

```sh
pot status -root "$HOME/.pot-node"
pot api -root "$HOME/.pot-node" /api/v1/runtime
```

Each node owns its Providers, credentials, assistants, permissions and workspaces.
Configure Providers through App or the local API. First-use Chat creates a
kitchen-named assistant once a default Provider exists. Assistant creation uses a stable `request_id` and
`name`; deleting an idle assistant retains its history and retires its schedules.
An explicit missing assistant never falls back to another identity.

Local workspace and installed resource paths can be persisted:

```json
{
  "version": 1,
  "workspace": "/absolute/agent-workspace",
  "resources": "/absolute/Resources"
}
```

```sh
pot config server /absolute/server-settings.json -root "$HOME/.pot-node"
```

Settings stay private and use absolute paths. Restart to apply changes. Startup
migrates old owner-listener/TLS settings out of `server.json`; those retired
listeners are not started. Configure browser TLS on the browser service itself.

Machine permission policy controls this local node. `execution_access: "ask"`
runs ordinary work in SRT and reviews explicit host requests; `allow` trusts the
machine. See [Permissions](permissions.md). A peer's policy is managed on that
peer, rather than sent from this node.

## Existing distributed installations

Upgrade each installation with its original data root and signing key. Migrations
archive retired membership state and retain execution journals, task identities,
receipts and cached transcript pages. Existing remote tasks keep their original
target. Unknown results and unconfirmed cleanup remain fenced; upgrading never
replays them or creates new delegations. Historical original evidence is required
for recovery. Reconnect autonomous installations through Agent Link for new work.
Old native remote Space credentials are no longer read or used for connections.

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

The target must be a running local node. Its private socket credential and stable
identity are checked before serving. Remote node access uses Agent Link within
that node. Browser listener TLS uses `-tls-cert` and `-tls-key`.

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
cannot use this interface. No model command is
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

## Agent Link access

The topology page manages Agent Links. Each node starts a loopback TCP peer
listener and shows its actual bind address. Access setup verifies a reachable
`tcp://host:port` on a private interface or explicitly creates a Tailscale TCP
Serve mapping. Opening the page does not change network settings or overwrite
unrelated mappings. Setup verifies the node's signing identity before saving an
endpoint. See [Peer protocol](peer-protocol.md).

Copy connection information from one node and paste the complete segment into
Add Agent on the other. It carries a short-lived, single-use invitation, so treat
it as a credential. The connection opens the peer's advertised assistant directory
and supported collaboration operations; it grants no node administration access.
Use the existing assistant chat and task delegation interactions. Disconnect
pauses the link; delete removes the saved connection while retaining history.
Neither action silently stops accepted work. See [Agent Links](agent-links.md).
