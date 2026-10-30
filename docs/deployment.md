# Deployment

Pot has one Go backend with two hosts: `pot serve` runs standalone Runtime; the
macOS Wails App composes its local Runtime in process. The App's main window and
tray use an authenticated loopback WebSocket and the shared frontend; native
bindings provide desktop capabilities and transport bootstrap. An independent
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

On upgrade, former `agent/plugins` and `agent/skills` move into the root, and
SQLite extension settings export once to files. Conflicting nonempty destinations
stop startup for resolution.

## Standalone node

Initialize a private root and run the node:

```sh
pot init -root "$HOME/.pot-node"
pot serve -root "$HOME/.pot-node" -ui 127.0.0.1:17703
```

The dedicated `-peer-listen` option is removed. Publish `/mcp` using the existing
browser listener (`pot serve -ui` or `pot web-ui-server`). The configured public
origin must match the URL used by clients. HTTP/HTTPS, certificates and
reachability remain deployer-managed.

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
pot ask -root "$HOME/.pot-control" "Investigate the build"
pot tasks list -root "$HOME/.pot-control"
pot tasks show TASK-ID -root "$HOME/.pot-control"
pot tasks interrupt TASK-ID -root "$HOME/.pot-control"
pot tasks cancel TASK-ID -root "$HOME/.pot-control"
pot doctor -root "$HOME/.pot-control"
pot config get agent -root "$HOME/.pot-control"
```

`pot ask` submits to an existing local assistant using the same conversation,
configuration and task delegation as the Web UI. It connects to the running
instance; it does not start a separate agent. With exactly one local assistant,
selection is automatic; otherwise pass `--assistant ID`. Existing conversation
context is retained. Each invocation submits a new message (queued if busy).
`pot ask -` reads a prompt from stdin; `--no-wait` returns the acceptance IDs.
The default waits for the assistant, associated tasks and their result handling.
Replies go to stdout; identities and progress go to stderr. `--json` emits one
structured result. Approval, pause, failure and uncertain states return a nonzero
exit status. Ctrl-C stops waiting without cancelling submitted work.
Execution progress and conversation replies arrive as two subscriptions on one
`pot.v1` WebSocket, without periodic status queries. `ask` waits for the
conversation stream to reach the execution snapshot's boot/cursor and for all
referenced replies before unsubscribing. Reconnects obtain fresh snapshots;
omitted replies in a bounded history window are loaded through the history
resource. Replies are deduplicated by message ID. Transport reconnection is
bounded to three retries and never resubmits the prompt. Use `pot usage` for
model, task and background-result usage.

Model overrides are request-local:

```sh
pot ask "Investigate the build" --model MAIN-MODEL --effort high \
  --task-model TASK-MODEL --task-effort low
```

The four flags are independent; omitted values inherit the saved assistant or
task-default configuration. Use `--provider ID` and `--task-provider ID` to
select another configured Provider (the profile ID used by assistant settings);
otherwise the respective existing Provider is used. With only a Provider
override, its default model is selected. `--effort default` or
`--task-effort default` clears the inherited effort and leaves it to the
Provider. Other accepted effort names are `none`, `minimal`, `low`, `medium`,
`high`, `xhigh`, and `max`; support depends on the model/Provider. Pot validates
configuration and protocol before accepting the message. The local model
directory does not enumerate per-model effort support; any further Provider
rejection is reported without silently substituting settings.

The assistant's frozen configuration also applies when it processes results
from tasks created by this request, including replacement tasks after retries.
Explicit task overrides fix the task model/effort selection and take precedence
over the assistant's tool arguments. They require local task execution; remote
targets are rejected when these overrides cannot be applied. Existing unrelated
tasks are not reconfigured. Saved assistant and Provider settings remain unchanged.
Experiment snapshots and task associations persist in `tasks.sqlite`.
Queued inputs and result batches with different experiment identities execute
separately so that one experiment cannot select another request's model.


```sh
pot ask --assistant ASSISTANT-ID "Investigate the build"
printf '%s' "Investigate the build" | pot ask --assistant ASSISTANT-ID - --json
pot usage
pot usage --assistant ASSISTANT-ID --from 2026-10-01 --to 2026-10-31
pot usage --task TASK-ID --details
pot usage --assistant ASSISTANT-ID --turn REQUEST-ID --json
```

`pot usage` defaults to today's global local-ledger usage in Asia/Shanghai.
`--from` and `--to` are inclusive dates and must be supplied together;
`--timezone` selects their time zone. Assistant and task filters are mutually
exclusive; model accounting for an assistant includes its own calls and owned tasks. `--turn` requires
one of these scopes. Actual model tokens, per-tool content estimates, and
communication counts/content estimates are separate; estimates must not be added
to actual usage. Missing measurements remain unknown. Remote execution usage
is unavailable locally. Communication records have no precise turn attribution
and are omitted for a turn filter. Tool/message activity is a separate direct-scope
view; it does not include child tasks and is unavailable for model/provider/request
filters. `--details` adds model request records (including frozen prices), tool calls
and messages, each paginated by 20 with independent next offsets; pass
`--offset N` to continue. `--json` preserves the structured statistics.

```sh
pot usage --assistant ASSISTANT-ID --group-by model
pot usage --ask REQUEST-ID --group-by stage
pot usage --model MODEL-ID --provider PROVIDER-ID --group-by date --json
```

`--group-by` accepts `model` (default), `stage`, `assistant`, `task`, or `date`.
`--ask` filters the recorded request correlation and can be combined with an
assistant filter. It does not create a separate ledger. Model groups retain
provider and profile identity. Stages are `assistant_processing`, `task_execution`,
`assistant_result`, `compaction`, or `unknown` for unattributed history.

Prices come automatically from the local models.dev directory: an exact supplier
quote takes precedence, then an identifiable model's original provider is used.
Provider identity uses the saved gateway directory binding, never its display name.
Unknown or ambiguous model IDs remain unpriced; bare family names such as `opus`
do not select a version. Complete Claude version IDs accept directory punctuation
variants such as `claude-opus-4.6` and `claude-opus-4-6` when unique.

No separate price file is required. The bundled directory supplies offline prices;
the existing daily refresh retains the last valid cache on failure. `--details`
shows supplier/origin matching; `--details --json` includes the frozen source,
directory provider/model, cost-version hash, catalog timestamp, rates and tiers.
Refreshes affect new reservations only, never existing request prices.

USD estimates include cached input reads/writes and context tiers selected from
actual normalized input tokens (strictly above the tier threshold). Missing usage
needed for a different cache rate remains unknown. Unsupported dimensions, such as
separately priced reasoning, audio or unknown tier types, retain an unpriced status
and explanation rather than applying a base price. These are catalog estimates,
not invoices or subscription allocations. Origin fallback may differ from the
actual third-party supplier's contract.



These commands use the authenticated private Runtime socket. Operator query
routes are not published by the browser service. Other everyday commands select
the first active local assistant by default; use `-assistant ASSISTANT-ID` to
select a specific assistant.

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

The browser publication mounts exactly `/mcp`, OAuth registration/authorize/consent/
token/revoke/connections and their discovery URLs. It forwards them through the
existing private authenticated Unix Control socket; it publishes no owner API
listener. External MCP bearer and SDK session metadata stay within MCPAccess.
Consent and revocation reuse the existing browser login and original session.
The public `Origin` and `Host` must match deployment configuration; non-browser
clients may omit `Origin`. Reverse proxies must preserve the configured public
Host and must not invent an owner session.

An ordinary client enters `https://pot.example/mcp`, logs in and confirms once.
A native receiver uses its documented explicit receiver-connect/receiver-serve
commands against that URL. A second Pot uses **Connect with browser** in connection
settings and needs no listener for reverse work. The optional setup plugin is
separate from a receiver and from Codex's independent MCP configuration.

Update both ends and explicitly reconnect. Old invitations and connections are
unsupported; no TCP listener starts, no old connection resumes and no fallback
occurs. Existing saved history remains readable; unfinished legacy remote work
keeps its last confirmed state. See [Agent Links](agent-links.md) and
[Pot MCP connections](pot-mcp-connections.md).

## Installation names

CLI packages contain `pot-PLATFORM/bin/pot`, `Resources/` and
`service/pot-runtime.service`. Keep the complete installation separate from the
data root. Package naming does not move an existing data root.

`pot service` manages `pot-runtime.service` on Linux or
`com.jjyr.pot.runtime.plist` on macOS. Linux units read optional
`~/.config/pot/runtime.env`. Installation refuses to overwrite a service definition.
Service configuration is an explicit operator action; the application does not
copy, rename or migrate old service definitions, environment files or roots.

This build requires the `pot` root identity discriminator. Roots made with the
previous discriminator are rejected before database startup and are not converted
or overwritten. Keep an old development root with its matching binary, and select
a separate unused root for this build. Do not edit identity markers or copy old
databases into a fresh root. This naming refresh provides no data migration.
