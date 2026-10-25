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

## Nodes and assistants

A stable Ed25519 `peer.Identity` authenticates `{node_id, public_key}`. Assistant
addresses are `{node_id, kind: assistant, agent_id}`. Node IDs, connection IDs,
assistant IDs and execution IDs are distinct. Names and origin icons are display
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
capabilities; a peer invitation does not authorize remote operator management.
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

## Copy and paste admission

The Link page (连接 in Chinese) and Codex connection page share address discovery
and copy actions. Addresses stay in a scrollable popup opened by **Copy connection
address**, leaving the default page for the graph and other actions. Escape, an
outside click or the close button dismisses the popup. Opening either page reads
local interfaces and existing Tailscale forwarding without enabling access.
Addresses are ordered by Tailscale
name, Tailscale IP, public IP, private IP and loopback, followed by other saved
addresses. **Get connection address** enables only the selected endpoint, verifies
this node’s identity and displays the complete single-use invitation in a selected,
read-only field. Manual copying is available immediately; the clipboard button is
an optional shortcut. Closing the popup clears the invitation, and invitations
are never put in snapshots or browser storage. Tailscale names and IPs use direct
listeners without requiring Serve configuration or administrator permissions.
Existing raw TCP forwarding can be reused, but copying never changes Tailscale
configuration. All selected endpoints remain enabled independently
and are restored on restart; an unavailable interface does not block startup.

System Tailscale is optional and managed outside Pot. Pot only reads its addresses
and existing forwarding; it has no login, Serve configuration or disconnect
operation for the system client. Explicit endpoint configuration accepts only
`address` mode and verifies the same signed TCP identity.

Paste the invitation in the existing Add Agent editor; live verification is
followed by connection, then Link selects the new node. No assistant binding
wizard is required. Link keeps its graph, regions, edges and responsive inspector.
The inspector shows provided assistants, opens their existing chat UI, and offers
disconnect, reconnect and confirmed deletion.

Invitations identify the node, independently of its available assistants. A node
with an empty directory can both publish and consume invitations.

Invitation version 5 carries the pinned node key, reachable TCP endpoint,
public display anchor and a single-use capability in its URL fragment. Never
log this capability, expose it through model tools or send it in chat. Preview
proves the server key before revealing the capability and cannot claim it or
exchange business data. The pair intent persists before confirmation, so
lost responses retry the original request. Both directions use one authenticated
session; only the dialing side needs an endpoint.

Endpoints permit public unicast IPv4/IPv6, loopback, private LAN and Tailscale
addresses, including DNS resolving entirely to usable unicast addresses. Unspecified,
multicast and link-local addresses are rejected. Dialing validates the actual numeric
address again. There is no public HTTP, proxy or protocol downgrade fallback.
TLS 1.3 encrypts frames and invitation capabilities, with mutual Ed25519 key
proof and the invitation or saved public-key pin. Self-signed certificates are
generated automatically. Calls use independent Yamux streams with CBOR metadata
and optionally Zstd-compressed bodies; see [Peer protocol](peer-protocol.md). Peer listeners
expose only identity verification and Agent Link, without membership or
distributed execution services.

## Business contract and recovery

Each connection forwards up to 32 business requests concurrently over one
multiplexed session and holds up to 64 additional requests in FIFO dispatch order.
A full queue returns `agent_link_capacity_exceeded` before sending. Queued callers
can cancel without sending; sent calls retain the transport's response or uncertain
outcome and are never automatically replayed by the forwarder. Different
connections have independent queues and shared dials. A caller's cancellation does
not cancel another caller's dial. Dials and dispatched calls have a 15-second bound.
The receiver admits 32 business handlers and one independent mailbox exchange;
slots remain held until handlers return, even if callers stop waiting. These limits
count RPC handlers, not running Tasks. Authenticated business and exchange calls
have no fixed per-minute rate limit. Invitation preview retains its separate
single slot and 180-admissions/minute budget. The peer transport's 64 pending-call
and memory limits remain in force.

`Backend` provides `Directory`, `Observe`, `Submit`, `Status` and `Cancel`. The same receiver
implementation owns in-process calls and authenticated remote calls. The receiver
enforces per-assistant operation support and caller ownership; unsupported
operations return a specific error. Capability checks for new submissions must
not prevent exact retries from returning an already accepted receipt. Optional
`ChatBackend`, `TaskInputBackend` and `ReviewBackend` interfaces supply only the
operations that an adapter implements. Codex implements task input and human
review; direct user chat is provided by Pot adapters.
Remote operations are scoped to the authenticated node and original caller
address, including its assistant, task or user kind. Returned task IDs, request
identity and assistant node are checked.

`observe` returns a live, payload-free node summary: hostname, per-assistant task
counts (running, queued, waiting for approval or allowance), chat activity and
uncertain-cleanup fences. Counts cover all local executions on the receiving
node, regardless of their caller. Caller-side records of linked Tasks and
historical remote launches are excluded; an unavailable original execution is
not reported as an allowance wait. No prompts, task text, workspace paths, tool
arguments, model settings or credentials are exported. Outbox counts and the
oldest known acceptance timestamp are scoped to the authenticated caller's node;
the request cannot select another peer. Delivery receipts still mean durable
acceptance, never completed processing.

The existing recovery pass samples observations over the authenticated session.
The sender validates node identity, assistant membership, unique IDs, bounded
counts and states, then caches the sample in memory with its local receipt time.
The sample is separate from the persisted directory and is lost on restart.
Sampling failure retains the last sample with an error; disconnect retains it as
cached data. Status changes invalidate the UI, and unchanged samples publish at
most every 15 seconds. Link additionally refreshes every 10 seconds while
visible so local activity, queue timestamps and freshness remain current.
After 30 seconds, a disconnected owner or a sampling failure, current totals
exclude that node and assistant activity is unknown. Cached values remain labeled
in the inspector. Connection health and execution state stay independent.

The message table records local insertion time outside immutable envelopes. Exact retries retain that timestamp. Historical
messages retain zero (unknown); if an outbox contains such a message, its oldest
timestamp remains unknown. Aggregates cover the entire node route rather than a
bounded delivery page.

The receiver durably deduplicates the complete task request before executing.
The sender stores a Task and original execution reference in its shared Task
catalogue. The receiver owns the actual execution. Local and peer submissions
share the same task operations; caller-side receipt caching does not run a worker.
A lost submission response retries the same request ID; it cannot create another
execution. A disconnected or uncertain request remains unresolved. Definitive
admission failures surface in the original task list and notification route.
Cancellation intent is persisted before any attempt to send it. Before acceptance
is observed it waits for the original reference; afterward it always targets that
reference. Disconnect and restart retain the intent for retry. Cancellation never
fabricates completion or proves that execution stopped.

Direct user chats with another Pot use a persisted user session address per
connection and target assistant, rather than the local default assistant. User requests address an
assistant; assistant replies to user addresses require the original request,
conversation and full sender/recipient binding. They enter the durable user
projection without autonomous local processing. Session addresses survive default
changes, local assistant deletion and restart. Peer text retains its existing
receiver-owned execution policy; a user session address grants no remote owner
API access. Proactive messages still address a real assistant explicitly. A
Codex sender discovers the current directory and supplies assistant_id; omission
or a removed target fails without selecting another recipient. Task delegation
uses the explicit executor or ordinary Pot routing contract.

The existing mailbox owns messages and immutable delivery receipts. Pending
selection filters both node IDs before applying the page bound, preserving
accepted senders even after assistant removal. Reconnection resends originals;
conflicting message IDs or receipts are rejected. Receipt means durable inbox
acceptance, not model processing or task completion. Direct user chat replies
are atomically admitted as already consumed by the user conversation, so they
cannot simultaneously trigger the local assistant's autonomous mail consumer.
The existing unread tracking and notification paths display their replies.

Assistant-originated remote task outcomes enter the existing durable background
notification inbox for the original directing assistant. User-originated tasks
instead retain a persisted user session for their source conversation and target.
They need no local assistant: results enter that conversation and its task outcome
projection without waking any local model. Restart and retries retain the original
caller, request, task reference and result ID. A different user session or caller
kind cannot inspect or cancel that accepted task. Retained conversation and task
reads still work after deleting a link, without permitting new execution.
Results retain receiver provenance. Source icons in assistant lists, chat titles and task rows identify Codex or another Pot.
Task details expose local archive state and controls advertised by the receiving
adapter. Codex supports terminal-run continuation; live steering and remote file
access are unavailable. Neither current adapter
advertises cross-node artifact access. Directory entries and exported capabilities
must be implemented before additional controls are exposed.

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

Disconnect pauses communication and retains endpoint, keys, directory, history
and task references. Reconnect reuses that connection. Delete revokes the saved
connection and removes its active directory projection; a minimal authenticated
tombstone fences a stale peer. Messages, tasks, results and receipts remain.
Neither action silently cancels receiver-owned execution.

Each revocation propagation attempt retires its transport session, including
failed or canceled attempts. The remote may close the connection before its
reply arrives. A later pending attempt opens a fresh authenticated session and
can observe the remote tombstone. Failed attempts retain the durable pending
revocation; closing the session does not retry business requests.

Persisted schemas and upgrade procedures are documented in
[database migrations](database-migrations.md). Upgrade connected Pot and Codex
adapters together. Admission requiring a new invitation stays fenced until the
user reconnects; upgrades never replay unfinished execution. See the
[Codex adapter](../integrations/codex/README.md) for installation, local execution
scope and conservative restart behavior.

## User-managed native assistants

A node may advertise `assistant.management` on its node endpoint. This narrow
capability accepts only authenticated `user` callers, never assistants or tasks.
The receiver returns its model catalog and opaque, locally configured workspace
scope IDs. Creation is idempotent by caller and request ID; conflicting retries
fail. Model/name edits require an idle assistant and its current revision and
retain its native conversation. The operation cannot select arbitrary paths,
credentials, tool policies or a different sender. Pairing with a host advertising
this capability grants those bounded management operations to the peer's user.
The Antigravity adapter implements it; Codex does not advertise it.

## Operator assistant management

Codex and Antigravity advertise node-level `assistant.management`. This operation
is available to the authenticated user interface, never an assistant tool. Control
replaces supplied `from` data with its authenticated operator identity; the peer
receiver independently checks the authenticated node and user kind.

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
