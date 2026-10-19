# Agent Link

`pkg/agentlink` is the message-only connection layer shared by Control and
external Agent adapters. A connection binds one local assistant address to one
remote assistant address. It does not grant owner access, transfer files, invoke
tools, create runs or define another execution permission policy. Runner
Execution Link adapters retain their independent membership and execution
contracts.

```text
Protected local UI                   Protected remote UI
      |                                      |
local Agent adapter                 remote Agent adapter
      |                                      |
      +---------- Agent Link ----------------+
      |         one authenticated channel    |
existing durable mailbox            remote durable mailbox
      |                                      |
existing peer receiver              explicit Agent consumer
```

## Identity and invitations

Agent identity is the existing `{control_id, kind, agent_id}` address with
`kind=assistant`. Adapter identity is `pot` or `codex`; a display name is not
machine identity. Connection IDs are independent of plugin IDs and Agent IDs.

The protected host UI calls `Invite` with its assistant and actual reachable
origin. The returned URL contains a versioned invitation in its fragment. The
fragment is a bearer capability and must not be logged, included in model tools
or sent through chat. Origins require HTTPS except for explicit loopback HTTP.
URLs with embedded credentials, query strings or arbitrary paths are rejected.
HTTP redirects are refused. A host without a published origin can still call
`Connect` to establish an outgoing connection.

One side pastes the other's URL. `Connect` first persists a connection intent
and encrypted shared credential, then presents the invitation to the remote
message listener. The listener verifies its current local identity and claims
the invitation transactionally, binding the full request digest. The same
handshake can be retried after a lost response; another request cannot reuse the
invitation. The default invitation lifetime is one hour. Unclaimed invitations
for the same assistant and origin are reused until expiry.

Each side stores the same connection ID and opposite local/peer identities.
Only the initiating side needs the other's reachable origin. The listener does
not call an arbitrary caller-provided callback URL. Adding a remote Agent never
registers the entire remote assistant directory or grants machine management.

## Reliable transport and message provenance

The narrow HTTP listener is rooted at `/api/v1/agent-link` and accepts only
authenticated `POST /pair`, `/exchange` and `/revoke`. It has no owner API,
cookie authorization or browser CORS access. Local management APIs belong to
the host and must retain their own owner authorization.

The initiating side's host runs `Service.Run` independently of UI selection.
Each bounded exchange pushes queued local envelopes and returns remote queued
envelopes. Envelopes and receipts must match the exact pair of bound assistant
addresses and `kind=message`; user instructions, Runtime events, task results and
plugin notifications are rejected. Batches are capped by the existing mail
page size and text limits. The listener limits per-connection request rates and
concurrent exchanges.

The adapter's `Mailbox` is the existing durable business inbox/outbox. No second
message queue is created by Agent Link. `Pending` must filter both complete
addresses before applying its page limit. `Deliver` must commit the immutable
envelope before returning its receipt, reject conflicting reuse, and preserve
conversation/reply/hop provenance. `Acknowledge` must compare the full immutable
receipt before removing an envelope from the pending projection.

Pending immutable messages are the recovery cursor: when a response or receipt
is lost, the source resends the same envelope. The receiver deduplicates it and
returns the same receipt. A separate acknowledgment-only request commits remote
receipts after local delivery. A crash between those steps leaves the source
pending, so restart safely repeats delivery. There is no lossy in-memory cursor
whose advancement can discard an uncommitted message.

Transport acceptance means the receiving mailbox durably contains the message.
It does not mean a model has read it, an assistant has replied, or an execution
has finished. External adapters need an explicit durable consumer; they must
not represent inbox acceptance as injection into an existing Codex App chat.

## Storage, status and revocation

The caller supplies a migrated `storage.ControlDatabase` and a `SecretStore`
implementing the existing encrypted secrets port. Connection and invitation
tables contain public metadata, secret references and hashes, never plaintext
tokens. Secrets are written before their references are committed: an
interrupted transaction can leave an unused encrypted secret, but cannot expose
a plaintext credential or create an authorized metadata record without one.

Connections start `connecting`; only a successful message exchange marks them
`ready` and records `last_seen`. Transport failures mark them `offline`. A
listener that has not heard from its dialer for thirty seconds becomes offline.
The mailbox's queued and processed states remain separate from connection
availability.

Revocation first persists a tombstone, fencing further admission without
deleting original messages or their receipts. A dialer retains the encrypted
credential solely while an offline peer still needs notification; the host
retries that notification after restart and then deletes the credential. A
revoked listener returns an authenticated revoked result to the next dialer
request. Token hashes retained in tombstones permit that result without keeping
plaintext tokens. A later explicit connection uses a new invitation and ID.

The host owns process supervision, origin publication and the integration of
`Handler`, `Run`, local management actions and its existing mailbox. Agent Link
never opens a network listener, configures Tailscale, or exposes owner APIs by
itself.
