# Agent Link

`pkg/agentlink` is the message-only connection layer shared by Control and
external Agent adapters. It binds one local assistant address to one remote
assistant address. It grants neither owner administration nor execution access.
Runner Execution Link retains its independent membership and run contracts.

```text
Protected local UI                     Protected remote UI
      |                                        |
local Agent adapter                    remote Agent adapter
      |                                        |
      +------- signed peer TCP session --------+
      |            bidirectional push          |
existing durable mailbox               remote durable mailbox
      |                                        |
existing peer receiver                 explicit Agent consumer
```

## Host identity and invitations

Agent identity remains `{node_id, kind, agent_id}` with `kind=assistant`.
Adapter identity is `pot` or `codex`; display names do not identify machines.
Connection IDs are distinct from plugin IDs and assistant IDs. The host injects
its durable `peer.Identity` and Ed25519 private key through `Options`; keys are
never derived from assistant names or addresses. The assistant's Control ID must
match the authenticated host identity.

The protected host UI calls `Invite` with an assistant and its actual reachable
`tcp://host:port` endpoint. Public-key-pinned invitation version 3 carries that
host identity, the target assistant and a single-use invitation code in the URL
fragment. This capability stays in local protected UI and encrypted storage;
never log it, expose it to model tools, or send it through chat.

Endpoints use the shared `pkg/peer` validation and direct TCP dialer. Loopback,
RFC 1918 IPv4, IPv6 ULA and Tailscale addresses are supported, including DNS names
that resolve entirely to the permitted private ranges. The actual connection
resolves and checks again before dialing a validated numeric address. There are
no HTTP proxies, redirects, web certificates or public HTTP fallbacks. TCP frame
signatures authenticate traffic but do not encrypt it; use the trusted local
network, VPN or Tailscale for network confidentiality.

One side pastes the other's address. `Connect` persists the exact intent before
dialing. The transport proves possession of both Ed25519 private keys with fresh
nonce challenges and verifies the invitation's expected remote host key. Only
then does `Authorize` claim the invitation transactionally, binding the full
request and authenticated remote host identity. Retrying a lost response reuses
that same claim; another host key or assistant route cannot reuse it.

Invitations expire after one hour by default. Unclaimed invitations for the same
assistant and endpoint are reused until expiry. Each side stores the same
connection ID and opposite assistant identities plus the verified peer host key.
Only the dialer needs a reachable remote endpoint. The other side sends through
the same established TCP session and needs no callback listener address.

## Shared host and signed sessions

The host mounts `Service.Authorize` under the `agent-link` service in its shared
`peer.Server` and calls `Service.Attach` from `OnSession`. These ports do not open
owner, execution or directory management APIs. The separate `identity` service
and local management UI belong to the host. Agent Link does not create a second
network listener or configure Tailscale itself.

Initial session admission contains only the invitation's public target metadata.
The invitation code is sent in a dedicated `pair` RPC only after the server has
proved possession of the pinned key. Before this claim, the temporary session
cannot exchange messages; a successful durable claim activates that same session.
An unanswered claim session closes after ten seconds. Subsequent
reconnections authenticate the pinned Ed25519 host key and connection ID; they
do not use reusable bearer session tokens. Every RPC is signed and scoped to its
authenticated session and monotonic frame sequence by `pkg/peer`. The service
exposes only `pair`, `identity`, `exchange` and `revoke` business methods, each restricted
to the session's connection. Per-connection rate and concurrent admission limits
supplement the shared transport's frame and request bounds.

## Read-only identity preview

`Service.Preview(ctx, address)` performs an actual pinned handshake and returns
the current target `Identity`, host public identity, endpoint, public-key
fingerprint and invitation expiry. It does not claim the invitation, create a
connection, write credentials or attach a durable message session. Expired,
already-used, mismatched and invalid invitations produce separate errors.

Preview handshake admission likewise contains no invitation code. Only after
verifying the server's signed identity does the client reveal that code to the
restricted `preview` RPC. This temporary session cannot call `pair`, `exchange`,
`revoke` or other services. Both the client operation and the server-side preview
session are bounded to five seconds. Address input is capped at 16 KiB; preview
RPC input is capped at 128 bytes and the returned card at 8 KiB. The server reads
the current invitation state again for each card request, so preview does not
reserve it or substitute for the final add operation's checks.

After one initial exchange confirms the new message channel, both hosts scan
their own durable outbox and independently push available messages through the
attached session. An idle outbox produces no empty business polls; signed peer
heartbeats maintain liveness. Closing a session changes availability to offline.
The original dialer reconnects; the incoming side waits for that connection.
Communication runs under the host, independent of window or Space selection.

## Durable delivery and provenance

`Mailbox` is the adapter's existing business inbox/outbox, not another Agent Link
queue. `Pending` filters the complete local and peer addresses before applying
the existing page limit. Envelopes must be `kind=message`, with the exact bound
assistant sender and recipient. Runtime events, task results, plugin reports and
user instructions are rejected rather than impersonated as peer correspondence.

`Deliver` durably commits the immutable envelope before returning its receipt,
rejects conflicting ID reuse and preserves conversation, reply and causal-hop
provenance. `Acknowledge` compares the full immutable receipt before removing an
outbound message from the pending projection. A failed or interrupted RPC never
causes a new message ID or a fabricated receipt. Pending originals are resent
on reconnection; duplicate delivery returns the existing receipt.

The source acknowledges only receipts for its actual submitted messages. The
receiving side independently pushes its own messages and receives their receipts
through the same session. Transport acceptance means durable inbox delivery,
not model reading, assistant processing, task execution or completion. External
adapters require an explicit durable consumer and cannot claim injection into an
existing Codex App chat.

## Persistence, migration and revocation

The caller supplies a migrated `storage.ControlDatabase` and the existing
encrypted `SecretStore` port. Metadata stores the bound public host key and
assistant identities; encrypted references retain invitation and retry material.
No per-connection bearer credential is used for transport authentication.

Control migration `0003_agent_link_tcp.sql` retires the HTTP format: old
invitations and reusable connection secrets are invalidated, active old links
become offline with `agent_link_reconnect_required`, and revoked tombstones stay
revoked. Reconnecting uses a new pinned TCP invitation. Existing mailbox messages,
immutable delivery receipts and unrelated encrypted provider secrets remain
untouched. There is no HTTP compatibility endpoint or protocol downgrade.

Revocation first persists a tombstone, fences all further message admission and
closes attached sessions. The dialer retains a pending revocation fact while its
peer is unavailable; it retries notification after reconnecting. If a revoke
response is lost because the remote session closed, a later authenticated
handshake reports the existing revoked binding and resolves that uncertainty.
An incoming-side tombstone rejects the original dialer's next handshake.
Revocation does not delete business messages, receipts or execution facts. A
later explicit connection receives a new invitation and connection ID.

## Logical node identity and upgrades

`node_id` identifies a logical communication endpoint, with `agent_id` selecting
an Agent beneath it. One machine may host several nodes. `peer.Identity` binds
`{node_id, public_key}` to the endpoint's stable Ed25519 key. Pot currently reuses
its Runtime ID value as its node ID; this does not make message addressing a
Control membership claim. Runner launch ownership, `Space.control_id` and Task
Control authority retain their existing meaning and names.

The peer handshake is version 2, copied Agent invitations are version 3, and
connection snapshots are schema 3. Old wire identity fields and old invitation
versions are rejected. Database migrations preserve IDs, keys, invitations,
message states and receipts while converting the persisted address fields and
their route/claim bindings. The existing invitation capability remains usable
through its newly copied version 3 address; an old copied version 2 address must
be copied again after the upgrade.

Product `secrets.Open` completes the key-dependent migration before returning.
`storage.FinalizeNodeIdentity` uses the existing master key to atomically convert
encrypted pair intents and invitation claim hashes; its pending receipt survives
failures for restart. `agentlink.New` refuses an incompletely migrated host with
historical credentials. An embedding host using an external SecretStore owns
conversion of that external store and must complete the database's migration
receipt only after its credentials and claims are safely converted. The product
key-based finalizer applies only to Pot's encrypted SQLite secret format.
