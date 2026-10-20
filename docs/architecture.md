# Architecture

Pot uses Go for product logic, Wails v3 with the system WebView for the macOS
App, and a shared HTML/CSS/JS frontend. The main window, tray panel and browser
use the same components, styles and state contracts. Go executes local commands
and Node tool plugins. Runtime manages no VMs, containers or guest brokers.
See [component boundaries](backend-components.md).
Reusable Go libraries live in `pkg/`, command entry points in `cmd/`, and product
implementation in `internal/`. [Go libraries](go-libraries.md) describes the
independent Agent loop and complete-host embedding APIs.

```text
UI / assistant / task
         |
 Agent Link collaboration contract
         |
 +-------+-------------------------+
 |                                 |
local adapter                 authenticated peer TCP
 |                                 |
local Pot assistants       autonomous Pot / Codex node
 |                         own assistants and execution
local task executor
```

## Autonomous nodes

`pot serve` hosts a standalone node; App composes its local Runtime in process.
Both own their assistants, models, credentials, workspaces, permission policy,
usage accounting and task execution. The local operator API uses a private Unix
socket; App uses native bindings. The independent browser service connects to a
local node and publishes authenticated browser access. No remote owner API,
Control enrollment, Runner membership or model proxy is provided.

Fresh roots contain no assistants. First-use Chat explicitly bootstraps Pot once
a default Provider exists; later assistants are created from + with random kitchen names and local defaults. Provider
storage alone creates no identity. Each private root retains its stable signing
key. App and standalone Runtime cannot open the same root concurrently.

Closing a window hides it while background work continues. Quitting App stops
local executions. A linked peer remains an autonomous node rather than an
execution machine controlled by this installation.

## Collaboration and ownership

Agent Link connections identify nodes. Each node advertises its assistant
directory, persisted default assistant and actual capabilities. Pot exposes real
assistants; the Codex adapter exposes virtual assistants bound to persistent
Codex sessions. An explicit assistant wins; omission resolves the receiver's
saved default. Accepted work keeps its original assistant even when the default
or directory ordering changes.

Local and remote collaboration use the same business parameters and lifecycle.
Local calls use an in-process adapter; remote calls use authenticated Agent Link
transport. A transport identity alone grants no node administration, provider
credentials or filesystem access. Task Agents retain their restricted tool set.

The receiving node owns accepted tasks and their execution facts. The sender
stores a delegation and original remote reference. Status, results and supported
cancellation address that original task. An accepted cancellation does not prove
execution stopped. Capability declarations gate optional operations; unsupported
operations fail explicitly. Notifications return to the original sender rather
than the current default assistant.

Assistant conversations use durable independent contexts. Incoming peer text is
reference data, not new user authority. Explicit communication tools send replies;
final model text is not automatically forwarded. Task results and notifications
resume the owning assistant through existing notification processing. See
[Agent Links](agent-links.md), [assistant messages](assistant-messages.md) and
[tasks](task-coordination.md).

## Transport and recovery

A shared [peer transport](peer-protocol.md) carries signed TCP sessions with
stable Ed25519 host identities. Service admission separately checks the saved
Agent Link connection. Explicit private `tcp://host:port` endpoints authenticate
frames but do not encrypt content; VPN or Tailscale can supply confidentiality.
No membership or distributed execution service is registered on this listener.

A request identity binds immutable contents. Exact retries return original
receipts, and conflicting contents fail. Lost acknowledgements, restart and
reconnection never create replacement work. Disconnect pauses a connection while
retaining configuration and history; deletion removes its saved directory entry.
Neither action silently cancels accepted work.

The local task journal, operation receipts, process supervision, permissions and
uncertainty fences remain authoritative. Upgrades retain stable node keys and
archive retired membership facts. Historical remote launches remain at their
original destination; they are never converted to new delegations or replayed
locally. Cached transcript pages remain readable. Missing original execution or
owner evidence leaves recovery fenced, including unconfirmed process cleanup.

## Local execution

Local model selection, allowance accounting, approvals and task resource
preparation stay on the owning node. Explicit task execution targets must be
local; remote work uses Agent Link delegation. No implicit model, credential,
workspace or plugin transfer occurs between nodes.

Every product Agent invocation, including mail and summaries, passes through the
node's shared execution manager. The internal local executor retains immutable
launch identity, checkpoints, commands and cleanup receipts. Its implementation
package is named `internal/runner`; it is no longer a distributed node role.

## Context and resources

Agent receives explicit execution services and context; it does no product or
machine discovery. Main loads global, assistant and destination project rules.
Worker loads its task brief and destination project rules, with no Main/global
instruction inheritance. Deeper project skills take priority; selecting global
skills never filters destination project skills. Omitted/empty selections load
no globals. See [Agent context](agent-context.md).

The node fixes model configuration and selected plugin/Skill names at Task
creation. Immediately before local execution, it prepares the source Profile's
current selected plugin directories and global Skills. Prepared content and
digests remain bound to the original run and are reused for allowance resumes.
Project Skills and AGENTS.md are discovered at the fixed execution workspace.
Main instructions do not cross the Task boundary.

Model context uses classified append-only sections. State refreshes do not rewrite
user input. Compaction durably installs current sections, continuation summary
and the last real user message before continuing. Provider cache lifetime does
not reset section comparison. Raw HTTP/token observations, estimates and budget
reservations remain distinct.

Tool plugins use the package format in `pkg/plugin` and bounded Node sessions in
`pkg/pluginhost`. Packages contain schema-2 `contributes.tools` with
`execution: "runtime"`. Provider/resource capabilities are unsupported. Runtime
locates tool/Skill assets independently of execution preparation. See
[Plugin packages](plugins.md).

Execution uses the destination Runtime service account. Protected machines run
commands, patches and plugins under the pinned SRT SDK. Trusted machines execute
directly; Blocked machines reject new work. Host command exceptions match exact
command/context and are scoped to the current work or stored as editable permanent
rules for one enrollment. Instruction edits retain independent exact review. See [Permissions](permissions.md).

Main and Task workspaces live in the destination filesystem with no mapping or
implicit project copy. Commands use local OS paths. A trusted supervisor drains process descendants before confirming cleanup; missing
proof retains unknown-result fences. SRT restricts direct filesystem and Unix socket
access while leaving network access open. It provides no VM isolation or quotas. See [Executor](executor.md). Optional VM management, automatic node
placement and remote Main tools remain deferred under issue #74.

## Persistence and updates

`pkg/storage` owns all SQLite schemas and runs embedded, versioned migrations
before returning a database handle. Business components own queries and restart
recovery. See [Database migrations](database-migrations.md) for history, upgrade
behavior and schema-change rules.

A Go data root has a stable `identity.json`, exclusive process lock and private
component stores. Initialize each root independently and retain its machine
identity across restarts. The same root cannot be opened by
App and standalone Runtime concurrently. Provider keys are encrypted using a
separately retained master key; see [Secrets](secrets.md).

Owner resource commands persist pending admission and original terminal receipts.
A repeated request returns its original response, including after later policy
edits. A pending command after process failure blocks conflicting replacement;
inspection never silently replays it.

UI state is scoped by node, assistant and conversation. Navigation generations reject
late replies; drafts and selections retain their owner. Healthy clients subscribe
to changes and stream conversation/Task output. Reconnect uses snapshots and
contiguous durable cursors. There is no healthy-state UI data polling loop.
See [shared UI](shared-ui.md), [node UI](multi-space-ui.md) and
[UI events](ui-events.md).
