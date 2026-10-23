# Deployment

Pot has one Go backend with two hosts: `pot serve` runs standalone Runtime; the
macOS Wails App composes its local Runtime in process. The App's main window and
tray use direct bindings and the shared frontend. An independent
`web-ui-server` process publishes browser access to one explicitly selected
local node. See [Architecture](architecture.md) for ownership and failure rules.

Deploy one complete Pot node on each execution machine and connect nodes through
Agent Link. Control and Runner are not separate services or configurable node
roles. Configure models, credentials, resources and machine permissions on the
node that will execute the work. The optional browser service only publishes UI
access to a local node; it does not coordinate remote execution.

## Install and data roots

Use the Go CLI archive built for the target OS/architecture, or build from a
pinned checkout using the toolchains in `mise.toml`:

```sh
mise install
mise run build:cli
```

CLI archives include the target Go CLI, pinned Node, tool plugins and Skills.
Linux Node requires a glibc-based host. Install any tools required by your selected
plugins, including Chrome/Chromium for browser execution. Keep private data roots
on a local filesystem supporting Unix sockets and file locks. See
[Development](development.md) for packaging.
Upgrade each node independently from a compatible protocol revision. Manual upgrades can
apply pending [database migrations](database-migrations.md); stop the old
processes and back up the complete data root before schema-changing upgrades.
The chat UI can update one local installation and its running hosts; see
[Updates](updates.md). Remote installations require separate coordination.

The default root is `~/.pot` on macOS and Linux. Use `-root /absolute/path`
for an independent instance. New roots may contain extension packages and configuration before initialization. Each root receives a stable node identity. App and standalone Runtime cannot open the same root
concurrently.
Keep each root private and preserve its identity and secret master key across
restarts. See [Secrets](secrets.md).

Local configuration has one file owner:

```text
~/.pot/
  plugins/<name>/plugin.json   user packages and declared assets
  skills/<name>/SKILL.md       user Skill bundles
  extensions.json             enablement and installation sources
  mcp.json                    independent MCP definitions and secret references
  cache/resources/            immutable local execution snapshots
  tasks.sqlite                task/run state, checkpoints and event history
  operations.sqlite           tool side-effect receipts
```

Builtin packages and Skills ship under `Resources/plugins` and `Resources/skills`.
User packages use unique names across both roots. Stop Runtime before replacing
complete packages manually. Configuration files contain no credential values;
credentials remain in the encrypted secret store. See [Extensions](extensions.md)
and [Plugin packages](plugins.md). Remote work uses Agent Link delegation.

Existing installations using `~/.pot-go` must explicitly select that root with
`-root`, or stop all hosts and move the complete root to `~/.pot` before starting
the new build. Preserve identity, keys and all databases together. Pot does not
silently open or merge a different root. On upgrade, former `agent/plugins` and
`agent/skills` move into the root, and SQLite extension settings export once to
files. Conflicting nonempty destinations stop startup for resolution.

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
Both ends must support peer protocol v4 (TLS/Yamux/CBOR/Zstd); there is no downgrade.

The private local socket serves CLI operator requests:

```sh
pot status -root "$HOME/.pot-node"
pot api -root "$HOME/.pot-node" /api/v1/runtime
```

Each node owns its Providers, credentials, assistants, permissions and workspaces.
Configure Providers through App or the local API when creating a local assistant.
First-use Chat offers Add assistant: API, another Pot, then Codex. An explicit
API creation action creates an assistant with a random two-word slug ID such as
`amber-otter` and a default name such as `Amber Otter`; linked chat and task
submission need no local assistant or Provider. Assistant creation uses a stable
`request_id` and an optional display `name`; deleting an idle assistant retains its
history and retires its schedules.
An omitted local API assistant selector uses the first active local assistant.
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

`regenerate-token`, `disable` and `qr` use the same operator scope. Browser
publication uses the explicit `-listen` and `-origin` settings. For access over
Tailscale, install and manage the system client independently and bind the
browser listener to its interface; Pot does not run an embedded Tailscale node.
Browser HTTPS still uses the configured certificate and key. The browser
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
pot chat -root "$HOME/.pot-control" "Investigate the build"
pot tasks list -root "$HOME/.pot-control"
pot tasks show TASK-ID -root "$HOME/.pot-control"
pot tasks interrupt TASK-ID -root "$HOME/.pot-control"
pot tasks cancel TASK-ID -root "$HOME/.pot-control"
pot doctor -root "$HOME/.pot-control"
pot config get agent -root "$HOME/.pot-control"
```

These commands select the first active local assistant by default. Use
`-assistant ASSISTANT-ID` to select a specific assistant from the node's directory.

Configuration sections are `providers`, `agent`, `routing`, `usage-policy` and
`instructions`. `config set SECTION JSON_FILE` uses the same revision checks as
UI/API callers. Agent configuration accepts an editable `AssistantSelection` or
the complete save request; it never writes a resolved Task snapshot back into a
Provider. Routing stores only profile/provider concurrency limits.

`pot service install|start|stop|uninstall -root PATH` manages a user service on
macOS or Linux. Install writes a reviewable definition without overwriting an
existing one. Start/stop act on that definition, and the recorded root must match.
Use the same account/root when restarting after upgrades. Neither building nor
installing an archive installs a user service.

## Agent Link access

The topology page manages Agent Links. Each node starts a loopback TCP peer
listener and shows its actual bind address. Access setup verifies a reachable
`tcp://host:port` on a public, private or system Tailscale interface. Existing TCP
forwarding can be reused; getting an invitation never changes Tailscale Serve
configuration. Opening the page does not change network settings. Setup verifies
the node's signing identity before saving an endpoint. See [Peer protocol](peer-protocol.md).

Copy connection information from one node and paste the complete segment into
Add Agent on the other. It carries a short-lived, single-use invitation, so treat
it as a credential. The connection opens the peer's advertised assistant directory
and supported collaboration operations; it grants no node administration access.
Use the existing assistant chat and task delegation interactions. Disconnect
pauses the link; delete removes the saved connection while retaining history.
Neither action silently stops accepted work. See [Agent Links](agent-links.md).
