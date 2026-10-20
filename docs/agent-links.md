# Agent Link

Agent Link connects autonomous nodes. Pot, another Pot and the Codex adapter use
one collaboration contract: assistant directory, messages, task submission,
status, cancellation and result notification. Each receiver owns its models,
credentials, workspaces, permissions and execution supervision. A connection
never grants owner HTTP APIs, model proxying or raw process launch access.

```text
Existing topology / assistant chat / task UI
                     |
          Pot collaboration coordinator
                     |
          pkg/agentlink.Backend
            /                   \
    local Pot adapter      TLS peer session
            |                   |
     local task journal     remote adapter
                          /              \
                    Pot assistants   Codex virtual assistants
```

## Nodes and assistants

A stable Ed25519 `peer.Identity` authenticates `{node_id, public_key}`. Assistant
addresses are `{node_id, kind: assistant, agent_id}`. Node IDs, connection IDs,
assistant IDs and execution IDs are distinct. Names and origin icons are display
metadata. One machine can host several autonomous nodes.

`Directory` advertises the current assistants, persisted default assistant ID
and capabilities. Pot exposes real assistants. Codex exposes virtual assistants;
each owns one persistent Codex session. Chat and tasks for the same virtual
assistant execute serially in that session. Additional assistants allow separate
contexts and parallel work; using the same filesystem does not isolate files.

An explicit target wins. An omitted target uses the receiver's persisted default,
initially its first assistant. Resolution occurs once at durable acceptance.
Exact retries return that original receipt even after the default changes or the
assistant is removed. Removing the default does not silently select another:
the directory advertises an empty default until the owner chooses a valid one.
Accepted tasks retain their original owner and historical reference.

## Copy and paste admission

The protected local connection toolbar copies one single-use invitation. Paste
it in the existing Add Agent editor; live verification is followed by connection,
then the existing topology selects the new node. No assistant binding wizard is
required. The topology graph, regions, edges and responsive inspector remain.
The inspector shows provided assistants, opens their existing chat UI, and offers
disconnect, reconnect and confirmed deletion.

Invitation version 4 carries the pinned node key, reachable private TCP endpoint,
public display anchor and a single-use capability in its URL fragment. Never
log this capability, expose it through model tools or send it in chat. Preview
proves the server key before revealing the capability and cannot claim it or
exchange business data. The pair intent persists before confirmation, so
lost responses retry the original request. Both directions use one authenticated
session; only the dialing side needs an endpoint.

Endpoints permit loopback, private LAN, IPv6 ULA and Tailscale addresses, including
DNS resolving entirely to allowed ranges. Dialing validates the actual numeric
address again. There is no public HTTP, proxy or protocol downgrade fallback.
TLS 1.3 encrypts frames and invitation capabilities, with mutual Ed25519 key
proof and the invitation or saved public-key pin. Self-signed certificates are
generated automatically. Calls use independent Yamux streams with CBOR metadata
and optionally Zstd-compressed bodies; see [Peer protocol](peer-protocol.md). Peer listeners
expose only identity verification and Agent Link, without membership or
distributed execution services.

## Business contract and recovery

`Backend` provides `Directory`, `Submit`, `Status` and `Cancel`. The same receiver
implementation owns in-process calls and authenticated remote calls. Directory
capabilities are truthful; unsupported operations return a specific error.
Remote operations are scoped to the authenticated node and original assistant
caller. Returned task IDs, request identity and assistant node are checked.

The receiver durably deduplicates the complete task request before executing.
The sender stores a delegation and remote reference, never a local worker copy.
A lost submission response retries the same request ID; it cannot create another
execution. A disconnected or uncertain request remains unresolved. Definitive
admission failures surface in the original task list and notification route.
Cancellation never fabricates completion; before the remote reference arrives,
a durable cancellation intent waits for acceptance and then targets that task.

The existing mailbox owns messages and immutable delivery receipts. Pending
selection filters both node IDs before applying the page bound, preserving
accepted senders even after assistant removal. Reconnection resends originals;
conflicting message IDs or receipts are rejected. Receipt means durable inbox
acceptance, not model processing or task completion. Direct user chat replies
are atomically admitted as already consumed by the user conversation, so they
cannot simultaneously trigger the local assistant's autonomous mail consumer.
The existing unread tracking and notification paths display their replies.

Remote task outcomes enter the existing durable background notification inbox
for the original directing assistant. They retain receiver provenance. Source
icons in assistant lists, chat titles and task rows identify Codex or another Pot.
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
