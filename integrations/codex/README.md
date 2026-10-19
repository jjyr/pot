# Pot ↔ Codex Bridge

Delegate an independent task from Pot on machine A to Codex on machine B. Both
machines can be on the same Tailscale network. Pot connects outward to B over
HTTPS; A needs no inbound listener. Durable SSE delivers progress and results
without polling or waking a model for every token. This does not attach to or
synchronize the conversation currently open in the Codex App.

```text
A: Pot tools ── HTTPS JSON ──> B: Bridge daemon ── JSONL stdio ──> codex app-server
   Control   <── SSE events ──      SQLite                           independent thread
      │                             ▲
      └─ checkpoint + inbox ACK ────┘
                                    ▲
                         local stdio MCP (pot plugin)
                                    │
                         user's Codex App / CLI chat
```

The daemon owns each App Server subprocess and reuses B's normal Codex login and
configuration. It neither needs nor exports an OpenAI token. Closing an App window
does not own these daemon-launched processes; stopping the Bridge stops its
supervised descendants. App Server crashes, shutdown or host restart can leave
external effects unknown. They are reported as failures; tasks are never replayed.

## Source and prerequisites

Build this integration from the same revision as Pot. Before this PR merges, use
`feat/codex-bridge` or the PR's exact commit in an isolated checkout; `main` does
not contain these files until merge. After merge, a fresh main checkout is the
normal entry point:

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
WebSocket is exposed. The Bridge's own HTTP protocol is version 1.

## Install the Codex-side package on B

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
installed/enabled in Codex. The MCP tools will report a missing pairing until the
next step. If your Codex surface has no plugin marketplace, use its officially
supported local MCP configuration instead:

```toml
# Add this table to your own Codex config.toml, preserving existing tables.
[mcp_servers.pot]
command = "/absolute/home/.codex/plugins/pot/bin/pot-codex-bridge"
args = ["mcp", "--config", "/absolute/home/.config/pot-codex/local-pair.json"]
```

That fallback installs the same MCP tools; the plugin's skill must also be read or
installed using your surface's supported Skills workflow. Do not register both
methods in the same chat. Never put a bearer token in MCP arguments or config.

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

The task API uses the Pot-side bearer; the local MCP credential can only create
messages and inspect its pair's message receipts. All IDs are scoped to that pair.

| Method/path | Meaning |
|---|---|
| `POST /v1/tasks` | Persist `{request_id, workspace, mode, prompt}`; return the original task on an exact retry |
| `GET /v1/tasks/{id}` | Inspect task state, final text and B-relative file references |
| `POST /v1/tasks/{id}/cancel` | Accept a cancellation request; inspect the later terminal state |
| `GET /v1/events?after={cursor}` | Subscribe to durable SSE events (`Last-Event-ID` takes precedence) |
| `GET /v1/snapshot` | Initial task states, current watermark and a bounded page of unaccepted notices |
| `POST /v1/receipts/{notice-id}` | Record Pot's durable inbox acceptance after the host ACK |
| `POST /v1/messages`, `GET /v1/messages/{id}` | Local Codex messaging and receipt/result inspection |
| `POST /v1/messages/{id}/reply` | Persist an explicit Pot-side answer, idempotently |

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
mise exec -- node --test plugins/codex/notifications.test.mjs
python3 integrations/codex/install_test.py
```

The deterministic fixture is a real supervised JSONL subprocess. It exercises the
protocol and transports without claiming a real paid Codex execution or two-machine
Tailscale acceptance. The installer tests use isolated homes and never change the
user's actual Codex configuration.
