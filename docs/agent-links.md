# Agent Link

Agent Link connects autonomous nodes. Pot, another Pot, Codex and Antigravity adapters use
one collaboration contract: assistant directory, messages, task submission,
status, cancellation and result notification. Each receiver owns its models,
credentials, workspaces, permissions and execution supervision. A connection
never grants owner HTTP APIs, model proxying or raw process launch access.

A node may advertise assistant and node task-execution endpoints in one `targets`
catalogue. Each entry carries its address, identity and versioned capabilities.
`create_task.target` selects the endpoint; the receiving Adapter owns task sessions
and execution.
Only their original directing assistant's authenticated user may approve their
operations. Assistant messages never carry approval authority or request user
approval; the receiving assistant declines operations outside its permissions and
reports the denial. See [Agent execution](task-coordination.md#agent-execution).

The [architecture overview](architecture.md) shows the shared local/peer operation
port and its execution adapters.

Pot MCP connection setup and receiver operation are described in
[Pot MCP connections](pot-mcp-connections.md). The product mounts MCP through the browser publication and its private Control
socket. Dedicated TCP pairing/listener entry points have been removed.

## Nodes and assistants

OAuth + PKCE binds a logical caller or explicitly consented receiver node.
Assistant addresses are `{node_id, kind: assistant, agent_id}`. Node, connection,
assistant, Task and execution-run identities remain distinct. Names are display
metadata. One machine can host several autonomous nodes.

`Directory.targets` binds each receiver's identity directly to its capability
description. Assistant endpoints have peer scope; independent node execution has
caller scope. The optional `default_target` is used by receiver-owned presentation
and messaging policy. Task creation always selects the current assistant when its
target is omitted; other Task receivers are explicit.

Adapter identifiers describe implementations and grant no capabilities. Directory
and Task receipts must match the connected node and Adapter identity. Task receipts
have one `receiver`, whose address is an assistant or independently owned Task.
Exact retries return the original receipt even after directory changes or identity
retirement. Node, caller, Task, run and saved connection identities remain distinct.

## Product operation port

`agentlink.Port` defines `Describe(Target)`, `Call(Target, Operation, params)`
and capability-checked `Subscribe(Target)` for cursor-based chat frames.
A target is the shared `{node_id, kind, agent_id}` receiver address. Connection
routing and locale are invocation context rather than parts of that address. Both local and
linked chat, task and assistant-scoped schedule routes use this port. HTTP and
native adapters only translate typed requests; there is no separate linked-chat
HTTP API or unexported native task payload. The local adapter remains in the
current process; this change does not introduce an agent or scheduler plugin.

`agentPort.Call` is the operation entry point. It uses the effective capabilities
for admission and dispatches operations to their implementations. Owner and peer
describe authorization scope; dispatch follows the requested capability and target.

Directories attach a version-1 capability description to each endpoint. Assistant
endpoints use explicit `peer` scope. The authenticated local operator sees
an `owner` description. `GET /api/v1/agent-links/capabilities?assistant_id=...`
returns the effective description; assistant catalogue rows carry the same data.
The frontend chooses controls from these descriptions and uses the shared chat
subscription. Adapters own native wakeups, peer polling and reconnection.
Source icons and location labels do not grant or remove functionality. Missing
or unknown-version descriptions grant no operations. Directory refreshes replace
cached capability descriptions; the receiver still enforces authorization at
admission. Directory descriptions must cover every advertised assistant; a peer
cannot publish an owner-scoped description. Upgrade connected adapters together.

`assistant.profile` permits presentation editing only; it does not authorize
retirement. `assistant.delete` is an independent receiver-advertised operation
available only to authenticated user callers, never assistant or task callers.
Its typed request binds the selected assistant and saved profile revision.
The receiver retires only an idle identity and rejects stale revisions, active
work and unconfirmed cleanup. No deletion is queued while disconnected. A
successful deletion first persists removal of the exact assistant address from
the sender's saved directory, then attempts a refresh. A failed refresh retains
that removal and the other known assistants. Directory reads carry in-memory
epochs so responses started before an acknowledged deletion or newer read cannot
restore an older snapshot. Sync success and failure update connection status
against the current saved directory. These snapshots supply both mention
completion and the assistant tool directory; historical identity presentation
caches remain separate. Deletion does not revoke the node connection, choose a
replacement default or delete workspace files. Offline state alone is not
deletion evidence, and assistants without chat capabilities remain valid targets.

Chat capabilities distinguish send, receipt, history, subscription, attachments,
references and individual turn controls. Task capabilities distinguish submit,
read, list, cancel, input, events, files, archive and advanced owner controls.
The existing local configuration, usage and approval interfaces remain owner
capabilities; a connection alone does not authorize remote operator management.
A `task.submit` request may carry a serializable `configuration` only through
the owner adapter's `task.configure` capability. Peer transport rejects that
field before calling the receiver, even if its contents claim a local identity.
`chat.references` and `task.configure` describe accepted input modifiers rather
than independent commands; `chat.subscribe` uses the subscription method.
Skills, model configuration, tool preparation and filesystem policy remain with
the execution-side implementation; they are not transmitted through Agent Link.

Linked task lists, archive choices and outcome cursors are projections of this
caller's durable submissions, not access to the receiver's entire task catalogue.
Task details include their effective operation description. Unsupported task
transcripts or input controls are not queried or displayed.

Owner-authorized failed-task retry and task guidance enter the same operation
port. Assistant-originated guidance retains its authenticated sender and parent;
using the port never converts it into user authority. Retry delivery authorization
is checked before calling the owner-only `task.retry` operation.

The current scheduler is retained behind the owning assistant's
`schedules.manage` capability. It is not moved into a separate service or made a
peer operator API. Its internal occurrence transactions and local execution
remain receiver implementation details.

## Connect and recover

An ordinary MCP client configures the published Pot URL, completes the existing
browser login and confirms its connection once. It needs no local Host or plugin.
Receivers additionally register their own real endpoints and claim addressed
Work/Control over that outbound connection. See [MCP collaboration](mcp-collaboration-contract.md)
for receipts, causation, run admission, cancellation and human-review fencing.

Update Pot and relevant plugins/adapters on both ends and explicitly reconnect.
Old TCP invitations are not parsed or imported. There is no listener, old pairing
restoration, forwarding fallback or simultaneous legacy release mode.

Control migration 0009 stores a minimal unsupported history row and last known
state, then drops pairing/listener/invitation configuration. It does not copy
grants into OAuth. Tasks, reports, receipts, messages and saved local artifact
content retain their original identities. Old remote work is excluded from
reconciliation and stays at its last confirmed unfinished state; it is never
automatically completed, failed, cancelled or submitted again. History reads use
the saved database records, without scanning old sessions or source files.

The current remote contract has no file-byte transfer. The previous Agent Link
service also exposed no TaskFiles/file-read operation; local file snapshots and
saved message-file content remain available under their original source checks.
A remote result path is metadata, not a delivered file.

## Chat execution projection

Chat access uses one typed endpoint contract for send, read, acceptance receipt
and subscription. Endpoint resolution chooses an implementation; lifecycle and
rendering rules never branch on location or adapter identity. Each accepted input
retains a separate reply lifecycle, independent of its send receipt and the
assistant's aggregate activity. The authenticated `chat.status` peer operation
checks the original caller, input and target before returning execution facts and
explicit correlated replies. It cannot read another caller's conversation.
Status reads never submit or repeat work. Failed, cancelled and unconfirmed
outcomes remain visible without requiring a text message. Successful empty
outcomes settle execution silently; only agent-authored text or resource
presentations produce completed reply bubbles. Text arriving before execution
ends remains intermediate output.

The [UI event contract](ui-events.md#main-conversation-streams) defines wire fields and
states. See [database migrations](database-migrations.md) for persisted projection
upgrades. This chat execution projection applies to receivers that implement
`ChatBackend`; it is separate from Codex tasks and assistant messaging.

## Lifecycle and upgrades

Disconnect pauses communication without cancelling work or removing history.
Revocation invalidates the OAuth connection; reauthorization cannot revive a
revoked grant. SDK sessions carry no owner authority. Renew expired access with
the same client only after browser login and consent. Deployers manage
reachability, HTTP/HTTPS and certificates.

## Operator assistant management

Codex and Antigravity advertise node-level `assistant.management`. This operation
is available to the authenticated user interface, never an assistant tool. Control
replaces supplied `from` data with its authenticated operator identity; the peer
receiver independently checks the authenticated node and user kind. Creation is
idempotent by caller and request ID; conflicting retries fail. The operation
cannot select arbitrary paths, credentials, tool policies or a different sender.

```text
Authenticated UI -> Control -> Agent Link -> receiver
                                catalog / create / update
                                assistant.profile / assistant.delete
                                             |
                              receiver-owned workspace scopes
                              assistant + atomic request receipt
```

Catalog schema version 1 returns runtime kind/name, supported actions and typed
fields (`name`, `model`, `workspace`, optionally `effort`), required/editable/default
metadata, model effort choices, workspace IDs/modes, and complete assistant
addresses/configuration/revisions. Required fields apply to creation. Updates send only changed
editable fields and preserve omitted name, model and effort. An omitted effort
preserves the receiver setting on update;
an explicit empty effort selects the model default when the field is supported.
Unknown actions and unsupported fields are
rejected. It carries no scripts or arbitrary form definitions. Codex discovers
models through App Server `model/list` without opening a thread or turn;
Antigravity uses `agy models`, whose slugs already encode reasoning choices.
Neither receiver advertises a separate instructions field in this version.

A workspace ID identifies a persistent owner-registered path and mode, independently
of any assistant ID. Remote creation can reuse these scopes but cannot submit new
paths or broaden permissions. The default workspace uses the resolved host-root
path, including when the root is reached through a filesystem alias. Creation
rejects a registered workspace that has since been replaced with a symlink.
Workspace selection is immutable for an existing
session; create another assistant to use another scope. Each assistant has its own
identity, saved avatar, model, revision and native conversation. Model/effort changes
require an idle assistant and revision CAS; presentation-only edits can apply while
busy. Profile/avatar and retirement use the existing operations and busy fences.
Creating an assistant never configures the separate node task executor.

Mutation receipts are scoped by authenticated caller and request ID. Identical
retries return the original result before checking today's catalog/revision;
different content conflicts. Assistant insertion and its result commit together.
Unconfirmed responses require the same request ID. A later directory failure does
not undo creation, and ordinary periodic synchronization updates all clients.
Errors distinguish unsupported, unauthorized, unreachable, unavailable model or
workspace, revision conflict, busy, capacity and invalid requests. The UI translates
these identifiers and retains raw diagnostics in a disclosure.

Native profile/delete and the existing assistant-management catalogue use a
receiver-only human-operation channel. Only the original authenticated human UI
can enqueue it; ordinary MCP callers, models and execution tools cannot. Existing
profile/delete DTOs and native revision, idle, cleanup, scope and management
receipt checks remain authoritative. Receivers journal before execution; a lost
finished response can be reconciled, while uncertain effects are never replayed.
Server-side tombstones stop old registrations from reviving deleted assistants.
The connection does not grant raw workspace, credential or owner configuration.
