# Codex Agent Link adapter

Codex joins Pot through the same Agent Link invitation and TLS 1.3 TCP transport
as another Pot node. The local plugin owns a directory of virtual assistants.
Each virtual assistant maps to one persistent Codex App Server thread. Direct
chat and delegated tasks use that same thread, in one queue per assistant.
Separate assistants can run in parallel and retain separate conversation context.
Assistants pointing at the same directory still share those files.

The local MCP stdio surface uses [the shared MCP component](../../docs/mcp.md)
and its official Go SDK server. Agent Link and Codex App Server JSONL retain
their own transports and business handlers.

The adapter's `observe` operation reports live task counts, chat queue activity,
hostname and cleanup fences without exporting task text, workspaces or model
configuration. Task and chat jobs share the execution queue but remain separate
in reported task counts. Directional pending-message summaries are scoped to the
authenticated peer. Pot displays these summaries in the existing icon topology.

```text
Pot chat / task tools
        |
   Agent Link (directory, messages, submit, status, cancel)
        | TLS 1.3 / Yamux / CBOR + optional Zstd
Codex Agent Link host
        +-- assistant A -- serial chat/task queue -- persistent Codex thread A
        +-- assistant B -- serial chat/task queue -- persistent Codex thread B
```

The receiving host chooses its local Codex login, model configuration, workspace
and sandbox mode. Remote requests cannot select arbitrary paths, change sandbox
permissions or approve an interactive request. The host declines approvals and
reports that local review is required. File references remain workspace-relative
on the receiving machine; the adapter does not advertise artifact downloads.

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
traffic in both directions. The node directory supplies available assistants. Pot can connect and chat with
Codex and submit tasks without any local assistant or API. The directory
advertises versioned `peer` operation descriptions per assistant; Pot uses them
for controls and queries. Node identity owns connection admission. Task callers
can be authenticated assistants, tasks or user sessions; the original caller
owns receipts and result routing. A persistent user session address owns direct
chat and its correlated replies. Peer task submission cannot supply owner-only
configuration, model credentials or Skills. An explicitly
selected assistant handles the work; otherwise the receiver uses its persisted
default, initially its first assistant. An accepted task keeps its original
assistant even when the default subsequently changes.

The initial `codex` assistant uses an isolated read-only directory under the host
root. Create another assistant for an owner-selected workspace:

```sh
~/.codex/plugins/pot/bin/pot-codex-agent assistant \
  --root ~/.config/pot-codex/agent \
  --id project --name Project --workspace /absolute/path/to/project \
  --mode workspace-write --default
```

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
plugin processes verify and reuse that host. Version 7 requires stopping an old
message host before upgrading. Stable node identity, message IDs and history
survive the database migration. The old independent Bridge pairing, HTTP/SSE
channel, `codex_*` tools and Pot-side Codex integration plugin have been removed.
Old configuration/history files are not silently deleted or replayed.

## Execution and recovery

The receiver durably admits each request before execution. Exact retries return
the same task; a changed request with the same identity conflicts. Direct incoming
messages enter the same assistant queue as tasks, and final replies retain their
original peer, assistant, conversation and causal parent. Durable inbox acceptance
remains distinct from completed execution.

Disconnect pauses transport and preserves configuration. Reconnect resumes it.
Delete removes the saved connection and directory entries. Neither operation
silently cancels accepted work or removes its history.

The host starts or resumes the assistant's persistent App Server thread and
persists the thread ID before starting a turn. Failure to resume never silently
creates a replacement context. Cancellation requests interruption; cancellation
is final only after a confirmed interrupted turn and supervised process cleanup.
Restart never replays unfinished work: queued tasks fail before execution and
started tasks fail with unknown outcome. Unknown cleanup fences new admission
for that assistant and requires local reconciliation; inspect the original
process/workspace and use a new assistant only after confirming it stopped.

The JSONL protocol follows Codex CLI 0.163.0 App Server v2 (`initialize`,
`thread/start`, `thread/resume`, `turn/start`, `turn/interrupt`). Authentication and
model configuration remain local to Codex. Deterministic tests use a supervised
fixture and do not claim live paid model or two-machine Tailscale qualification.

```sh
mise exec -- go test -race ./integrations/codex/agent
mise exec -- python3 integrations/codex/install_test.py
```

## Explicit message recipients

Plugin 0.8.0 / host version 7 uses Agent Link invitation version 5. Upgrade it with
Pot from the same revision. Established connections retain their node pins and
IDs after migration; unfinished admissions need a fresh invitation. Existing
virtual assistants, Codex threads and mailbox history remain intact.

`pot_connections` exposes connected nodes and their assistant directories.
`pot_send` requires `connection_id`, `assistant_id`, `request_id` and `text`.
Codex chooses a real recipient from the directory. Missing or deleted targets
fail instead of selecting the receiver default. This does not change task
submission's persisted default behavior. Direct chat results return through the
original request to the Pot user's conversation even when that Pot has no local
assistants. Local owner configuration is not a recipient binding UI.

The Codex adapter implements the shared [chat endpoint contract](../../docs/agent-links.md#chat-execution-projection).
Its authenticated `chat.status` read projects the original caller-owned job and
returns the durable final reply even before the ordinary outbox publisher wakes;
subsequent delivery deduplicates that identical envelope. Cleanup uncertainty
remains unknown. Codex text is currently published at turn completion; this
adapter does not advertise incremental text or tool events.
