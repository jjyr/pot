# Codex Agent Link adapter

Codex joins Pot through the same Agent Link invitation and TLS 1.3 TCP transport
as another Pot node. The local plugin owns a directory of virtual assistants.
Each virtual assistant maps to one persistent Codex App Server thread. Direct
chat and delegated tasks use that same thread, in one queue per assistant.
Separate assistants can run in parallel and retain separate conversation context.
Assistants pointing at the same directory still share those files.

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

Open the existing connections page with `pot_open_connections`, or:

```sh
~/.codex/plugins/pot/bin/pot-codex-agent connections \
  --root ~/.config/pot-codex/agent
```

Copy the connection segment on one side and paste it into **Add Agent** on the
other side. The node directory supplies available assistants. An explicitly
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
peer TCP uses `127.0.0.1:8793`. Configure a reachable private or Tailscale endpoint through the
existing connection settings. One reachable endpoint permits bidirectional
communication. The TCP endpoint never exposes the owner HTTP API. Identity keys
and owner credentials are encrypted; the host root must be owner-only.

The detached host keeps receiving work after an MCP chat closes. Subsequent
plugin processes verify and reuse that host. Version 4 requires stopping an old
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
