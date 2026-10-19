# Pot ↔ Codex Bridge

## Message connections

The Codex plugin now starts a message-only Agent host automatically. Its stable
Codex Agent identity and durable messages survive chat windows and host restarts.
It shares the same typed Agent Link plugin host as Pot; connecting grants only
peer messaging, not file access, execution permission or Control administration.
The host and message addresses use `node_id` for this generic communication
identity. Execution Control authorization remains a separate responsibility.

Install using `mise exec -- python3 integrations/codex/install.py`, then enable
Pot in Codex. The MCP command is `pot-codex-bridge host --root
/absolute/home/.config/pot-codex/agent`. No pairing file, workspace choice or
App Server model run is needed to open connections. `pot_open_connections` opens
the local page; alternatively run:

```sh
~/.codex/plugins/pot/bin/pot-codex-bridge connections \
  --root ~/.config/pot-codex/agent
```

The page shows this Agent's public service endpoint, **Copy address**, and **Add
Agent**. Copy creates a short-lived invitation only in the local clipboard flow.
Paste the other side's address to connect in either direction. One reachable
endpoint is sufficient for authenticated bidirectional messages. The page checks
the pasted address automatically and shows the verified Agent before **Add
Agent** becomes available. Checking does not create a connection or consume the
invitation. Connection address and identity fingerprint remain in collapsed
details; adding uses one final action and shows a brief success confirmation.
No separate messaging permission step is required. Invites
contain message-only capabilities: keep them out of chat, prompts and logs.

Owner management stays on `127.0.0.1:8792` using the local HTTP API. The
separate message listener on `127.0.0.1:8793` uses the shared TCP Peer protocol.
Configure a reachable `tcp://host:port` address over Tailscale or another trusted
private network. No TLS certificate is needed. The host verifies Ed25519 nonce
challenges and signs session traffic; invitations pin the remote host's public
key. TCP endpoints must resolve entirely to private or tailnet addresses, and
connections recheck DNS and pin the selected address. There is no HTTP peer
fallback, anonymous readiness route, or owner management service on this socket.

Select Tailscale only after installing and logging in locally. Its explicit
configure action inspects actual Serve settings and reuses a matching TCP
forwarder, or creates one when the selected port is unused. It refuses to
replace unrelated settings and never enables Funnel or changes ACLs. Manual
setup, after checking `tailscale serve status`, is
`tailscale serve --bg --tcp=8793 tcp://127.0.0.1:8793`; copy the actual TCP address.
The forwarding target is the message socket, never the owner HTTP listener.

For a direct private address the host opens a message-only listener on the
corresponding local interface. Before saving an address it proves that the
endpoint holds this stable Codex host's private key. A different key, invalid
address or failed handshake closes the candidate and keeps the prior listener.
Restart restores the saved listener or reuses a working TCP forwarder. If the VPN
is unavailable, local management remains usable with a network repair state.

The Ed25519 private key is stored in encrypted Secrets; metadata contains only
its public key and secret reference. Restore the original database and master
key if identity material is lost; startup never silently replaces an existing
host key. Upgrading from the earlier HTTP design invalidates its connection
address and requires a new TCP invitation. Owner credentials, stable Agent IDs,
mailbox messages, processing claims and receipts are preserved without launching
or replaying work.

The first plugin process ensures and verifies a detached user-level daemon;
subsequent chats reuse it after an authenticated version check. A foreign service
on that port is an error, never assumed to be the Bridge. The daemon continues
after the MCP chat closes and receives no model-start instruction from startup.
Private identity, encrypted credentials and logs live under the supplied root;
the directory must be owner-only. Stop the owning daemon process to stop service;
starting the plugin again resumes its persisted inbox and transport.
Plugin version 0.4.0 requires message host version 3 and does not reuse an older
daemon. Stop the old daemon before starting the updated plugin. The Node ID,
encrypted keys, message IDs and consumer claims survive the database upgrade;
old wire field names are not accepted by the new Peer protocol.

`pot_connections` lists established peers. `pot_send` queues an explicitly
authorized message, with exact-retry idempotency. `pot_messages` reads this stable
Agent's durable inbox, and `pot_message_ack` marks an actually handled message
processed. Unread messages remain queued until a Codex chat explicitly invokes
the reader. This does not inject messages into an existing Codex App chat or
silently start an independent App Server task. Transport receipt means remote
durable inbox acceptance, distinct from reading, processing or execution.

Each MCP process has a private reader identity. Inbox batches are claimed
atomically, so two Codex chats cannot handle the same batch concurrently.
Retries by that reader resume its original batch. The host binds the deepest
claimed processing parent to subsequent sends, including forwards to another
peer; acknowledging a message does not reset the causal limit. A daemon restart
keeps reader claims. If the original chat is lost, stop it and explicitly use
**Recover unconfirmed messages** on the local connection page. This invalidates
old readers and reopens only unconfirmed messages with their original causal
facts. Previously performed actions can remain uncertain; recovery does not
execute a model or mark a message processed.

The default message plugin does not load the old delegation-only skill. Its
unchanged text remains available for an explicitly configured legacy integration;
the message tools supply their own narrow schemas without a new system prompt.

## Independent task delegation

The following older, separately authorized delegation API remains independent of
message connections. A message invitation cannot call its task tools or relax
its workspace and mode policy. Existing task records and uncertain outcomes are
preserved; this API still uses its explicit daemon and pairing configuration.

Delegate an independent task from Pot on machine A to Codex on machine B. Both
machines can be on the same Tailscale network. Pot connects outward to B over
HTTPS; A needs no inbound listener. Durable SSE delivers progress and results
without polling or waking a model for every token. This does not attach to or
synchronize the conversation currently open in the Codex App.

```text
A: Pot tools ── HTTPS MCP ──> B: Bridge daemon ── JSONL stdio ──> codex app-server
   Control   <── SSE events ──      SQLite                           independent thread
      │                             ▲
      └─ checkpoint + inbox ACK ────┘
                                    ▲
                         local stdio MCP proxy (optional legacy registration)
                                    │
                         user's Codex App / CLI chat
```

The daemon owns each App Server subprocess and reuses B's normal Codex login and
configuration. It neither needs nor exports an OpenAI token. Closing an App window
does not own these daemon-launched processes; stopping the Bridge stops its
supervised descendants. App Server crashes, shutdown or host restart can leave
external effects unknown. They are reported as failures; tasks are never replayed.

## Source and prerequisites

Build this integration from the same revision as Pot. A fresh main checkout is
the normal entry point; when testing a PR, use its exact commit in an isolated
checkout:

```sh
git clone https://github.com/jjyr/pot.git pot
cd pot
mise install
```

For an existing checkout, inspect its status and current revision first. Do not
reset, switch branches over local changes, or replace another checkout. A separate
`git worktree add` or clone is suitable for testing the PR.

B needs Linux or macOS, the repository's Go toolchain for building (or a bridge
binary built on the same OS/architecture), and a locally installed Codex CLI.
Run `codex --version` and `codex login status` locally. The protocol baseline is
`codex-cli 0.163.0`; its generated non-experimental schema was used to verify
field names and permissions. `serve` and `check` require that CLI version.
Requalify other releases rather than assuming
experimental protocol compatibility. The protocol implementation
uses the official v2 App Server JSONL API: `initialize`, `initialized`,
`thread/start`, `turn/start`, `turn/interrupt`, item and turn notifications.
See [App Server](https://developers.openai.com/codex/app-server),
[plugin authoring](https://learn.chatgpt.com/plugins/build/plugins), and
[MCP](https://developers.openai.com/codex/mcp). No experimental network App Server
WebSocket is exposed. Tools use MCP Streamable HTTP at `/mcp`; durable
notification recovery uses the Bridge's version 1 HTTP/SSE protocol.

## Legacy delegation: optional local MCP registration

The installer enables the message host by default. The following installation
details and manual registration are relevant only when also choosing the
independent delegation API. They do not replace the message connection flow.
From this checkout:

```sh
mise exec -- python3 integrations/codex/install.py
```

The installer builds `cmd/pot-codex-bridge`, copies the supported Codex compatibility
package (`.codex-plugin/plugin.json`, `.mcp.json`, `skills/`) into
`~/.codex/plugins/pot`, renders absolute MCP command paths, and registers a local
source in `~/.agents/plugins/marketplace.json`. It preserves unrelated marketplace
entries, pairing files and Bridge configuration. Rerunning is safe; changed managed
files cause an actionable stop rather than overwriting your edits. To use a
prebuilt binary, pass `--binary /absolute/path/pot-codex-bridge`. To test in
isolation, pass `--home /absolute/temporary/home`. It does not alter Tailscale,
install a system service, or start model work.

With a supported CLI (verified with `codex-cli 0.163.0`), the installer runs the
official `codex plugin add pot@personal --json` and verifies installed/enabled
status through `codex plugin list --json` (it uses your existing marketplace name
if different). Restart Codex and start a new chat. With an older CLI or
`--source-only`, select **Pot** in the personal plugin directory to install and
enable it manually. Registered local source is not the same as
installed/enabled in Codex. The default plugin now exposes message connections;
the following manual MCP registration is specifically for the independent
delegation API. If your Codex surface has no plugin marketplace, use its officially
supported local MCP configuration instead:

```toml
# Optional independent delegation only. Keep the default message host entry.
[mcp_servers.pot_delegation]
command = "/absolute/home/.codex/plugins/pot/bin/pot-codex-bridge"
args = ["mcp", "--config", "/absolute/home/.config/pot-codex/local-pair.json"]
```

This explicit legacy entry exposes the older paired tools; use the unchanged
legacy skill only with this separately configured integration. Do not register
duplicate legacy proxies. Never put a bearer token in MCP arguments or config.

## Configure B and pair the devices

Edit `~/.config/pot-codex/bridge.json`; only B's owner chooses local directories:

```json
{
  "listen": "127.0.0.1:8791",
  "database": "bridge.sqlite",
  "codex": "codex",
  "workspaces": {"project": "/absolute/path/on/B/to/project"}
}
```

Relative database paths resolve beside this file. Workspace aliases resolve to
existing absolute directories. Keep configuration, database and pairing files
private to the service account (directory mode 0700, files 0600). Default binding
is loopback. Direct non-loopback listening requires explicit `tls_cert` and
`tls_key`; unspecified addresses such as `0.0.0.0` are rejected.

Run the Bridge in a terminal on B:

```sh
~/.codex/plugins/pot/bin/pot-codex-bridge check --config ~/.config/pot-codex/bridge.json
~/.codex/plugins/pot/bin/pot-codex-bridge serve --config ~/.config/pot-codex/bridge.json
```

Leave it running while configuring the other terminal. For Tailnet HTTPS, the
owner can configure an existing Tailscale Serve installation:

```sh
tailscale serve --bg http://127.0.0.1:8791
```

Use the actual HTTPS URL reported by Tailscale. This command changes B's Serve
configuration; review it yourself, especially if B already serves another app.
No Funnel or ACL change is needed by this integration. Ordinary TLS certificate
and hostname verification is mandatory. For an existing private CA, add its PEM
as the optional `ca` field in the private pairing file; do not disable verification.

Find A's **Control ID** and destination **assistant ID** in Pot's existing machine
and assistant details. A pairing fixes that route and a set of workspace aliases:

```sh
~/.codex/plugins/pot/bin/pot-codex-bridge pair \
  --config ~/.config/pot-codex/bridge.json \
  --endpoint https://YOUR-B.TAILNET.ts.net \
  --control CONTROL_ID --assistant ASSISTANT_ID --device pot-A \
  --workspaces project \
  --remote-file /private/path/pot-A-pair.json \
  --local-file ~/.config/pot-codex/local-pair.json
```

The two files contain distinct credentials. `local-pair.json` only permits the
Codex-side messaging/receipt API. Transfer the remote file privately to A (for
example, with an existing SSH connection), never through chat or a prompt. The
command refuses to overwrite existing files. Pair once; installation reruns do
not rotate credentials. Add `--allow-write` only if you want this pairing to allow
`workspace-write`; remote input can otherwise request only `read-only`. Even with
that option, the Bridge never accepts `danger-full-access`, changes approval
policy, or automatically approves a request.

A needs a Pot build from this revision and the packaged `plugins/codex` directory.
`mise run build:plugins` includes it in the normal Runtime resources. For an
existing installation, stop Pot before replacing its installed package, following
[plugin packaging](../../docs/plugins.md); do not edit the main checkout or a
running package in place. Pair through the normal authenticated local Pot API:

```sh
pot codex-pair -root /absolute/path/to/POT_DATA_ROOT /private/path/pot-A-pair.json
```

The command verifies Bridge identity over HTTPS, verifies the Control/assistant
route, saves the pairing in encrypted Secrets, and enables the background stream
for that assistant. Exact retries return the existing account. A different pairing
requires disconnecting the old Codex Bridge connection first through Pot's existing
integration management API/settings. The generic notification settings show stream
status and backlog. Enable the Codex tools for the paired assistant using the
existing plugin selection; only expose this pairing to assistants you intend to
allow to submit to the fixed route. No token belongs in notification config.

## Use and inspect results

Ask Pot to delegate a task with a new `request_id`, workspace alias, mode and text.
The tools are `codex_submit`, `codex_status`, `codex_cancel`, `codex_message`, and `codex_reply`.
An exact request retry returns the same task; changed payload with the same ID
returns a conflict. An accepted task is queued; completed output is delivered
through the same plugin inbox and summary consumer used by other plugins. A busy
assistant finishes its foreground work before summarizing results.

App Server reports `queued`, `running`, `waiting_approval`, `completed`, `failed`,
`cancelling`, or `cancelled` through the Bridge projection. Approvals become visible
notifications and are safely declined. Interactive input and permission elevation
are rejected. If review is needed, inspect locally and submit a new authorized
task. `cancel` returning accepted means only that cancellation was requested;
`cancelled` is published only after an interrupted turn and confirmed process
cleanup (or for a queued task that never started). Unknown cleanup remains a failure.

Event previews count JSON escaping toward their 20 KiB bound; truncated previews
retain an ID for querying the original task/message. The result includes bounded final assistant text and workspace-relative changed
file references. Those files are on B, relative to the selected alias. They are not
Pot downloads or URLs on A. There is no automatic repository/workspace transfer,
artifact download service, commit creation, push, or merge. Ask for commit details
in the task when needed; they can be included in Codex's final text.

From Codex, explicitly ask to use `pot_notify` or `pot_request`. Both return a
message ID; `pot_receipt` distinguishes persisted **accepted**, Pot-inbox
**received**, and explicitly **answered**. An inbox receipt is not assistant
completion or permission to execute the request. `codex_reply` returns an authorized
response from Pot; notification summary turns have no reply tools. No automatic
response forwarding or delegation loop runs, and message depth is capped at 1.

## Event recovery and bounds

The Bridge serves sessionless MCP Streamable HTTP at `POST /mcp`, using the
official Go MCP SDK and JSON responses. It supports MCP protocol versions
`2025-11-25` and `2025-06-18`, `initialize`, `notifications/initialized`,
`tools/list`, `tools/call`, and `ping`. Clients send
`Accept: application/json, text/event-stream`, `Content-Type: application/json`,
and the negotiated `MCP-Protocol-Version` after initialization. The endpoint
requires the pairing bearer on every request, rejects cross-origin browser
writes, and does not expose an unauthenticated discovery endpoint. There are no
MCP sessions to persist; `GET` and `DELETE /mcp` return 405.

The Pot-side bearer discovers and calls only the five `codex_*` tools. The local
credential discovers and calls only the three `pot_*` tools. Discovery and each
operation enforce that role and scope all IDs to the authenticated pair.
Successful calls return JSON text plus the same object in `structuredContent`;
operation failures use MCP `isError`. Request conflicts, missing IDs and capacity
errors remain distinguishable by their error codes. Do not retry an uncertain
submission with a new `request_id`.

| MCP tool | Meaning |
|---|---|
| `codex_submit` | Persist `{request_id, workspace, mode, prompt}`; return the original task on an exact retry |
| `codex_status` | Inspect `{task_id}` state, final text and B-relative file references |
| `codex_cancel` | Request cancellation of `{task_id}`; inspect the later terminal state |
| `codex_message` | Inspect `{message_id}` and its original complete body |
| `codex_reply` | Persist an explicit `{message_id, result}` answer, idempotently |
| `pot_notify`, `pot_request` | Persist `{request_id, body, correlation?, depth?}` for the paired Pot assistant |
| `pot_receipt` | Inspect `{message_id}` acceptance, Pot inbox receipt, or explicit answer |

Any MCP client supporting Streamable HTTP can use `https://BRIDGE-HOST/mcp`
with the appropriate pairing credential from its secure credential store.
Alternatively, a local stdio-only client can run `pot-codex-bridge mcp --config
/absolute/private/pair.json`. That process forwards JSON-RPC to the same MCP
endpoint, including initialization and tool discovery; it contains no separate
tool implementation. Use the remote pairing for delegation or the local pairing
for Pot messaging, and never expose either credential in a prompt or command-line
argument. The daemon must already be running for either transport.

MCP makes the Bridge callable from other agents; Codex remains the execution
backend. Adding another execution backend requires its own adapter for lifecycle,
permissions and results. Pot's packaged plugin uses a transport-independent MCP
client module for tool calls; its durable background consumer continues to use
the following authenticated endpoints. The former task and message REST routes
have been removed, so upgrade the Bridge and Pot plugin together.

| Method/path | Meaning |
|---|---|
| `GET /v1/identity` | Verify pairing identity, role and destination during setup |
| `GET /v1/events?after={cursor}` | Subscribe to durable SSE events (`Last-Event-ID` takes precedence) |
| `GET /v1/snapshot` | Initial task states, current watermark and a bounded page of unaccepted notices |
| `POST /v1/receipts/{notice-id}` | Record Pot's durable inbox acceptance after the host ACK |

MCP tool results do not replace durable delivery. Long-running execution returns
a task ID immediately; Pot receives completion through the existing SSE inbox,
and other MCP clients can query `codex_status`. Disconnecting a tool client does
not cancel a persisted task. MCP progress notifications and MCP task extensions
are not used for task completion or recovery.

`GET /v1/events?after=CURSOR` is a real SSE stream with ordered numeric event IDs,
`Last-Event-ID` support, initial `hello`, and 15-second heartbeats. Every projection
and its event commit in one SQLite transaction. Subscription registration and log
reads share the commit lock, preventing the snapshot/subscription gap. Progress is
coalesced to at most once per second; token text is never journaled.

The plugin acknowledges a batch only after Control atomically commits its opaque
checkpoint with the existing trusted inbox. The stream host sends ACK after that
transaction, and the plugin persists outstanding Bridge receipt ACKs in its next
checkpoint. Reconnect backs off to 60 seconds with jitter and resumes from the
committed cursor. Duplicate/older IDs cannot resubmit tasks or duplicate summaries.
Quiet streams and progress do not call a model.

The event journal retains 4,096 records. Expired/future cursors return
`409 resync_required` (or an SSE `resync` frame on an active slow stream). The
snapshot returns a watermark and bounded pages of unaccepted critical notices for that pair;
those notices survive event retention. Recovery commits them before subscribing
from the watermark. Inbox acceptance is deduplicated using the original notice
identity, including after restart. Accepted notices are retained as receipts.

Bounds: 64 KiB HTTP/JSONL MCP requests, 24,000 bytes per task prompt, 16,000 bytes
of final text, 12,000 bytes per Codex→Pot message/reply, 64 pending critical notices
for new admission, 256 retained tasks and 256 messages per pair, 32 active/queued
tasks globally, one executing task at a time, two SSE connections per pair, and
180 HTTP requests per pair per minute. Streams fetch only 32 records at a time;
network writes have a five-second deadline. State and queues are bounded; admission
returns capacity errors instead of deleting idempotency records. Archive/replace a
full Bridge data directory deliberately after settling tasks and retaining needed
receipts; changing data directories creates a new pairing, not a retry of old work.

## Troubleshooting and lifecycle

- `codex_unavailable...`: install Codex on B and use an absolute executable path if
  your service PATH differs. Verify `codex login status` as that same OS user.
- `codex_*_failed...`: inspect B's CLI version/login/configuration; protocol failures
  are fenced and never retried as a new execution. There is a 30-minute task limit.
- `cleanup_unconfirmed_requires_operator_reconciliation`: new admission and
  queued execution are fenced. Stop the daemon and inspect the original process
  and workspace locally. Only after confirming it stopped, deliberately archive
  the private Bridge data directory and pair a fresh directory; never retry an
  old ID against new storage as if its outcome were known.
- `bridge_http_401` / authorization status: check the pairing and device route.
  `429` indicates rate, inbox or retained-history capacity; inspect before retrying.
- TLS/address errors: verify Tailnet reachability, Serve URL, hostname and CA.
  There is no insecure TLS fallback or automatic alternate destination.
- Workspace denial: configure the alias on B and explicitly include it in the
  pairing. A cannot choose arbitrary absolute paths or another target assistant.
- Stop with SIGINT/SIGTERM. Supervised Codex descendants are drained. After a
  restart, unfinished tasks become `failed`: queued tasks report
  `bridge_restarted_before_execution`, started tasks report
  `bridge_restarted_outcome_unknown`; inspect the workspace before making a new
  request. Stopping computation does not roll back external effects.

Run reproducible tests without model credentials:

```sh
mise exec -- go test -race ./integrations/codex/bridge ./pkg/pluginhost ./pkg/integrations/codex
mise exec -- go test -race ./internal/control -run TestCodexBridge
mise exec -- node --test plugins/codex/notifications.test.mjs plugins/codex/mcp-client.test.mjs
python3 integrations/codex/install_test.py
```

The deterministic fixture is a real supervised JSONL subprocess. It exercises the
protocol and transports without claiming a real paid Codex execution or two-machine
Tailscale acceptance. The installer tests use isolated homes and never change the
user's actual Codex configuration.
