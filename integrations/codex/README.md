# Codex Agent Link adapter

Codex joins Pot through the same Agent Link invitation and TLS 1.3 TCP transport
as another Pot node. The local plugin owns a directory of virtual assistants.
Each virtual assistant owns its persistent message context. Explicit Codex tasks
bypass assistants: each task owns one independent persistent App Server thread.
Task continuations resume that thread; unrelated tasks can execute in parallel.
Workspaces remain receiver-owned and shared directories do not isolate files.

The local MCP stdio surface uses [the shared MCP component](../../docs/mcp.md)
and its official Go SDK server. Agent Link and Codex App Server JSONL retain
their own transports and business handlers.

The adapter's `observe` operation reports live task counts, chat queue activity,
hostname and cleanup fences without exporting task text, workspaces or model
configuration. Task and chat jobs share the execution queue but remain separate
in reported task counts. Directional pending-message summaries are scoped to the
authenticated peer. Pot displays these summaries in the existing icon topology.

```text
Pot assistant -- create_task(executor=codex) --> task T --> Codex thread T
      ^                    | approval / result                 |
      +--------------------+-----------------------------------+
Pot assistant -- send_message --> Codex assistant --> message thread
      ^                              | extra approval: decline
      +--------- result / denial ----+
```

Online users may retire an idle virtual assistant through the separate
`assistant.delete` capability. Settings use Pot's shared deletion disclosure and
confirmation dialog. Queued messages, active jobs and uncertain cleanup prevent
retirement. The adapter keeps a tombstone, thread binding and durable history;
restart and owner configuration cannot resurrect that ID. Removing the default
leaves the default unset. This does not delete the underlying Codex thread or
workspace files, and other assistants and the node connection remain available.

The receiving host chooses the Codex login, model, workspace and sandbox mode.
Only the user in the directing assistant can approve that assistant's explicitly
delegated Codex task operations. Assistant messages carry no approval authority;
requests needing a human decision are declined and reported to the sender.
See [task and approval contract](../../docs/task-coordination.md#codex-execution).
## Install and connect

Install from the same revision as Pot:

```sh
mise exec -- python3 integrations/codex/install.py
```

The installer builds `cmd/pot-codex-agent`, installs the Codex plugin and registers
its local marketplace source. It preserves unrelated entries and refuses to
overwrite local package edits. `--binary /absolute/path/pot-codex-agent` uses a
prebuilt binary; `--home /absolute/temporary/home --source-only` tests installation
in isolation. The plugin starts its local Agent Link host automatically. It does
not start model work merely by being enabled or connected.

Run the plugin's setup entry (`pot-setup`) after installation. It calls
`pot_open_connections` and passes the returned temporary URL directly to Codex's
in-app browser through `open_in_codex`. The tool prepares a page; it does not
launch the system browser. The URL grants connection-management access for 30
minutes and must not be printed or shared. It never contains the durable owner
credential. Reopen setup if the page expires.

For a system-browser fallback outside Codex:

```sh
~/.codex/plugins/pot/bin/pot-codex-agent connections \
  --root ~/.config/pot-codex/agent
```

The connection page discovers loopback, active public and private interface
addresses (IPv4 and IPv6), and Tailscale addresses. Tailscale names appear first,
followed by Tailscale IPs, public IPs, other private IPs, loopback and unclassified
saved addresses. Click **Copy connection address** to open the scrollable address
list; it stays hidden by default. Each row has a **Get connection address** action. Choose
loopback for Pot on the same computer, a LAN
address for the same private network, a public address for other networks, or
Tailscale for the same tailnet. Discovery
only reads network state. Getting an address enables the selected signed peer
listener and displays the complete invitation in a selected, read-only field.
Copy it manually or use the optional clipboard button; browser clipboard access
is not a prerequisite. Closing the address list clears the invitation, and it is
never saved in browser storage. Tailscale names can bind directly to the assigned
IP, just like numeric addresses. An existing raw TCP forwarding mapping can be
reused; this flow never requires or changes Tailscale Serve configuration.
Selected addresses are persisted and
restored independently on restart. An unavailable address does not prevent the
local page or other addresses from working. A firewall or remote network can
still prevent access; discovery does not prove remote reachability.

Copy the connection segment on one side and paste it into **Add Agent** on the
other side. Only one side needs a reachable address; the same connection carries
traffic in both directions. The directory advertises assistant profile/deletion
controls and a separate caller-scoped task executor. A local Pot assistant owns
its explicit Codex task receipts, results and pending human approvals. Messages
address a real assistant explicitly. Peer submissions cannot select owner-only
configuration, model credentials or Skills.

The initial `codex` assistant uses an isolated read-only directory under the host
root. New assistants receive the same random kitchen name and saved character
avatar as ordinary Pot assistants; an explicit name overrides the random name.
Create another assistant for an owner-selected workspace:

```sh
~/.codex/plugins/pot/bin/pot-codex-agent assistant \
  --root ~/.config/pot-codex/agent \
  --id project --name Project --workspace /absolute/path/to/project \
  --mode workspace-write --default
```

Pot's assistant settings can edit the linked assistant's name and avatar and
restore its original character. The advertised `assistant.profile` peer operation
accepts authenticated user callers and presentation edits only, with a revision
check against stale settings. It cannot delete assistants or change execution
configuration. Directory refreshes distribute the receiver's saved image and name.

The plugin's `pot_update_profile` tool supports reading the current profile,
changing `name`, importing `avatar_path` (workspace-relative PNG, JPEG or WebP,
at most 8 MiB), or `reset_avatar: true`. A running virtual assistant is bound to
its own identity through `POT_CODEX_ASSISTANT_ID`; an ordinary Codex chat must
explicitly select an `assistant_id` from `pot_assistants`. It never selects the
node default implicitly. Images are normalized to 128px PNGs; escaping workspace
paths and symlinks are rejected. Rename and avatar changes retain the original
character, background, workspace, permissions and Codex thread.

An assistant's workspace and permission mode remain fixed for its session.
Create another assistant for a different scope. Rename or change the default
using the same command with the original workspace and mode. These are local
owner operations, never remote task arguments.

The owner HTTP listener is bound to `127.0.0.1:8792`; mutually authenticated TLS
peer TCP uses `127.0.0.1:8793`. Choose and copy a discovered address directly from the connection
page; no separate Configure access step is needed. One reachable endpoint permits bidirectional
communication. The TCP endpoint never exposes the owner HTTP API. Identity keys
and owner credentials are encrypted; the host root must be owner-only.

The detached host keeps receiving work after an MCP chat closes. Subsequent
plugin processes verify and reuse that host. Stop the host before an upgrade;
an incompatible running version is rejected without rewriting its databases.
Stable node identity, message IDs, sessions and history survive migrations.
See [database migrations](../../docs/database-migrations.md) for backup and
upgrade rules.

## Execution and recovery

### Human approvals in Pot

`create_task({executor:"codex", text:"Title\nInstructions"})` chooses the sole
available Codex node; set `link_id` to choose explicitly when several are connected.
Do not supply an assistant target. Omission of `executor` retains existing task
execution. Unavailable or ambiguous Codex selection fails without fallback.

The host's independent task execution scope initially uses its isolated read-only
workspace. Configure future tasks locally with:

```sh
~/.codex/plugins/pot/bin/pot-codex-agent executor \
  --root ~/.config/pot-codex/agent --workspace /absolute/path --mode workspace-write
```

Accepted tasks retain their workspace and mode. Their thread binding is persisted
before the first effective turn. Completed/cancelled tasks accept idempotent owner
guidance through `task.input` / `send_message` to the task address, starting a new
run in the same thread. Guidance during an active run is rejected; callers wait
for its outcome. Unknown cleanup fences that task's continuation, not unrelated
tasks. Restart never replays unfinished accepted work.

Codex retains its configured approval policy and reviewer; the adapter does not
force a human reviewer. Automatic review stays on Codex: its lifecycle events
are muted and its decisions/history are never sent over Agent Link. Only
pending requests for a human decision enter the directing assistant's existing
approval list and card, which show exact command, cwd, reason,
file changes and network scope. **Allow once** sends App Server `accept` for the
single request; **Deny** sends `decline`. Models cannot approve. Caller identity,
task, thread and turn are checked; a different assistant cannot decide. No
session-wide approval or policy amendment is offered. Cancellation, completion,
server resolution and host shutdown invalidate pending requests. Decisions do
not create a persisted grant.

Ordinary messages execute in the receiving assistant's message context. They
never request approval in either assistant's UI: human approval requests are declined
and the original sender receives a failure reply. Execution outcomes use the
normal task-result/message paths, without a separate approval outcome panel.
Resolved requests disappear from the existing pending approval list. Unsupported interactive
requests fail visibly; they do not open another approval path.

Normal completion waits for `turn/completed` and supervised process cleanup
before making the reply eligible for delivery to Pot. Successful replies contain the
final answer, excluding commentary and explicitly marked intermediate answers.
Command output, reasoning, automatic-review and progress notifications are disabled on this
connection; remaining tool items are discarded after reading and never included
in replies. Approval requests and turn lifecycle notifications remain enabled.
Resuming a thread excludes its history from the response without changing Codex's
persistent conversation context.

Failed and cancelled message replies carry `state`, `error` and `partial_output`
as JSON text, including when no model output was produced. The original reply
route and receipt behavior apply to these outcomes as well. The outbox publisher
commits each terminal reply exactly once. Thread-open
failures retain the RPC method, rejection code and bounded server diagnostic,
so a resume failure can be investigated without replacing its saved thread.
App Server JSONL
events are bounded to 4 MiB per line independently of the final-answer limit;
read failures (including oversized events) are reported as
`codex_jsonl_read_failed_outcome_unknown`, separately from process EOF.

Disconnect pauses transport and preserves configuration. Reconnect resumes it.
Delete removes the saved connection and directory entries. Neither operation
silently cancels accepted work or removes its history.

The host starts or resumes the task's or message assistant's App Server thread and
persists the thread ID before starting a turn. Failure to resume never silently
creates a replacement context. A thread has one App Server writer, including
while that writer is idle. Opening the adapter's thread in Codex desktop or
another client can therefore block Pot with `already has an active writer`.
The adapter reports `codex_thread_in_use` for this rejection; Pot explains the
conflict and recovery steps in the UI language while retaining the RPC diagnostic
in error details. Other thread-open errors keep their general failure summary.
Release that thread in the owning client before retrying in Pot. For an idle
Codex desktop chat, temporarily archive and then restore it to release the loaded
writer while retaining its history and thread identity. Do not delete writer-lock
files or reset the assistant's thread binding. Merely reconnecting Agent Link
does not release a writer owned by another App Server.

Cancellation requests interruption; cancellation
is final only after a confirmed interrupted turn and supervised process cleanup.
Restart never replays unfinished work: queued tasks fail before execution and
started tasks fail with unknown outcome. Unknown cleanup fences new admission
for that task or message assistant and requires local reconciliation; inspect the
original process/workspace before creating replacement work.

The JSONL protocol follows Codex CLI 0.163.0 App Server v2 (`initialize`,
`thread/start`, `thread/resume`, `turn/start`, `turn/interrupt`). Authentication and
model configuration remain local to Codex. The protocol fixture runs without
model credentials; see [development](../../docs/development.md) for its commands.

```sh
mise exec -- go test -race ./integrations/codex/agent
mise exec -- python3 integrations/codex/install_test.py
```

## Assistant messages

`pot_connections` exposes connected nodes and their assistant directories.
`pot_send` requires `connection_id`, `assistant_id`, `request_id` and `text`.
Codex chooses an explicit recipient from the directory. Missing or deleted
recipients fail without choosing a default. The runtime binds the executing
message parent; a task cannot impersonate an assistant to send peer messages.

Incoming assistant and task references enter the addressed assistant's durable
queue. The host admits, runs and acknowledges them automatically, then publishes
one terminal reply through the ordinary Agent Link mailbox. The adapter does not
offer direct user chat or a manual inbox consumer. Explicit Codex task execution
uses the independent node executor described above.
