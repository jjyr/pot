# Agent Link

Agent Link connects autonomous nodes. Pot, another Pot and the Codex adapter use
one collaboration contract: assistant directory, messages, task submission,
status, cancellation and result notification. Each receiver owns its models,
credentials, workspaces, permissions and execution supervision. A connection
never grants owner HTTP APIs, model proxying or raw process launch access.

The [architecture overview](architecture.md) shows the shared local/peer operation
port and its execution adapters.

## Nodes and assistants

A stable Ed25519 `peer.Identity` authenticates `{node_id, public_key}`. Assistant
addresses are `{node_id, kind: assistant, agent_id}`. Node IDs, connection IDs,
assistant IDs and execution IDs are distinct. Names and origin icons are display
metadata. One machine can host several autonomous nodes.

`Directory` advertises the current assistants, persisted default assistant ID
and per-assistant operation descriptions. Pot exposes real assistants. Codex exposes virtual assistants;
each owns one persistent Codex session. Chat and tasks for the same virtual
assistant execute serially in that session. Additional assistants allow separate
contexts and parallel work; using the same filesystem does not isolate files.

An explicit target wins. An omitted target uses the receiver's persisted default,
initially its first assistant. Resolution occurs once at durable acceptance.
Exact retries return that original receipt even after the default changes or the
assistant is removed. Removing the default does not silently select another:
the directory advertises an empty default until the owner chooses a valid one.
Accepted tasks retain their original owner and historical reference.

## Product operation port

`agentlink.Port` defines `Describe(Target)`, `Call(Target, Operation, params)`
and capability-checked `Subscribe(Target)` for cursor-based chat frames.
A target contains an assistant ID and an optional saved link ID. Both local and
linked chat, task and assistant-scoped schedule routes use this port. HTTP and
native adapters only translate typed requests; there is no separate linked-chat
HTTP API or unexported native task payload. The local adapter remains in the
current process; this change does not introduce an agent or scheduler plugin.

`agentPort.Call` is the operation entry point. It uses the effective capabilities
for admission and dispatches operations to their implementations. Owner and peer
describe authorization scope; dispatch follows the requested capability and target.

Directories advertise a version-1 operation description per assistant under
`operations`, with explicit `peer` scope. The authenticated local operator sees
an `owner` description. `GET /api/v1/agent-links/capabilities?assistant_id=...`
returns the effective description; assistant catalogue rows carry the same data.
The frontend chooses controls and subscription/polling from these descriptions.
Source icons and location labels do not grant or remove functionality. Missing
or unknown-version descriptions grant no operations. Directory refreshes replace
cached capability descriptions; the receiver still enforces authorization at
admission. Directory descriptions must cover every advertised assistant; a peer
cannot publish an owner-scoped description. Upgrade connected adapters together.

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
addresses. Copying enables only the selected endpoint, verifies this node’s
identity and copies one single-use invitation. Tailscale copying creates private
TCP forwarding when needed. All copied endpoints remain enabled independently
and are restored on restart; an unavailable interface does not block startup.

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

`Backend` provides `Directory`, `Observe`, `Submit`, `Status` and `Cancel`. The same receiver
implementation owns in-process calls and authenticated remote calls. The receiver
enforces per-assistant operation support and caller ownership; unsupported
operations return a specific error. Capability checks for new submissions must
not prevent exact retries from returning an already accepted receipt. Legacy
node-wide feature flags are replaced by the per-assistant descriptions.
Remote operations are scoped to the authenticated node and original caller
address, including its assistant, task or user kind. Returned task IDs, request
identity and assistant node are checked.

`observe` returns a live, payload-free node summary: hostname, per-assistant task
counts (running, queued, waiting for approval or allowance), chat activity and
uncertain-cleanup fences. Counts cover all tasks owned by that receiving node,
not only the caller's delegations. No prompts, task text, workspace paths, tool
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

Message migration `0012_agent_message_timestamps` records local insertion time
outside immutable envelopes. Exact retries retain that timestamp. Historical
messages retain zero (unknown); if an outbox contains such a message, its oldest
timestamp remains unknown. Aggregates cover the entire node route rather than a
bounded delivery page.

The receiver durably deduplicates the complete task request before executing.
The sender stores a delegation and remote reference, never a local worker copy.
A lost submission response retries the same request ID; it cannot create another
execution. A disconnected or uncertain request remains unresolved. Definitive
admission failures surface in the original task list and notification route.
Cancellation never fabricates completion; before the remote reference arrives,
a durable cancellation intent waits for acceptance and then targets that task.

Direct user chats use a persisted user session address per connection and target
assistant, rather than the local default assistant. User requests address an
assistant; assistant replies to user addresses require the original request,
conversation and full sender/recipient binding. They enter the durable user
projection without autonomous local processing. Session addresses survive default
changes, local assistant deletion and restart. Peer text retains its existing
receiver-owned execution policy; a user session address grants no remote owner
API access. Proactive messages still address a real assistant explicitly. A
Codex sender discovers the current directory and supplies assistant_id; omission
or a removed target fails without selecting another recipient. Task delegation
retains its separate receiver-default contract.

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
Task details expose cancellation and local archive state; unsupported local
controls, steering and file access are unavailable. Neither current adapter
advertises cross-node artifact access. Directory entries and exported capabilities
must be implemented before additional controls are exposed.

## Lifecycle and upgrades

Disconnect pauses communication and retains endpoint, keys, directory, history
and task references. Reconnect reuses that connection. Delete revokes the saved
connection and removes its active directory projection; a minimal authenticated
tombstone fences a stale peer. Messages, tasks, results and receipts remain.
Neither action silently cancels receiver-owned execution.

Control migration `0017_agent_link_nodes` fences previous assistant-scoped links
and expires their invitations. Re-copy an invitation to authorize node-wide
collaboration. Old mailbox history, IDs, keys and encrypted receipts are retained;
upgrading never replays execution. `0018_agent_link_delegation_stop` persists
cancellation intent and direct-chat sender binding. Tasks migration
`0011_agent_link_task_targets` explicitly introduces optional target/source fields
without changing existing run identity or uncertain-effect receipts.

The old Bridge pairing, independent HTTP/MCP/SSE delegation channel and Pot-side
`codex_*` plugin are removed. See the [Codex adapter](../integrations/codex/README.md)
for installation, virtual assistant configuration and conservative restart fences.
See [database migrations](database-migrations.md) for upgrade and backup rules.

Control migration `0020_agent_link_direct_chat` upgrades established version-4
connection presentation anchors to version-5 node identities, preserving node
pins, connection IDs, histories and receipts. Unfinished admissions remain fenced
and need a fresh invitation; old invitations expire. Historical chat sender
bindings remain unchanged. New chats persist their own user session identity.
Tasks migration `0012_agent_link_direct_chat` records the typed user message
contract and indexes delivery routes without rewriting old immutable envelopes.
Upgrade Pot and its Codex plugin together; old wire versions are not accepted.

Control migration `0022_agent_link_operations` expands cached node feature flags
into per-assistant peer operation descriptions. It preserves peer pins, routes,
accepted task payloads and immutable receipts and never replays execution.
