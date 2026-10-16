# Assistant messages

Assistant-to-assistant messaging uses the same addresses, envelopes and processing
semantics within a Space and across Spaces. A Space corresponds to a Control's
stable identity. Handoff is coordination expressed by agents in message bodies;
there is no handoff protocol, task migration state machine or automatic transfer
of permissions, files, running processes or task ownership.

## Modules

- `internal/protocol` defines addresses, envelopes, directories, receipts and
  relay API DTOs. An address is `{control_id, assistant_id}`; display names never
  route messages.
- `internal/assistant` owns SQLite Inbox/Outbox persistence, deduplication,
  reply binding, processing state and history.
- `internal/relay` is a transport-independent library with injected endpoint traits.
  It knows only protocol types; it has no UI, credential or database dependency.
- Control binds sender identity to the current assistant, checks destinations and
  submits message conversations through the existing Runtime execution manager.
- Server exposes the authenticated adapter. App alone composes relay endpoints
  from its existing local and remote connections and owns the relay lifecycle.

Control does not connect directly to another Control or receive its credentials.
No pairing, new account system, certificate or relay credential is introduced.
Web and CLI do not run a relay. Same-Space messaging does not depend on App.

## Tools and conversations

`list_assistants({})` lists local assistants and remote assistants discovered by
App. A cached remote directory does not prove connectivity.

`send_assistant_message({to, text, reply_to?})` accepts a durable message and wakes
an independent conversation belonging to the destination assistant. Replies to
the current correspondent automatically retain its conversation when `reply_to`
is omitted. An explicit reply must reference a received message and reverse its
participants. The tool returns acceptance/queueing, never execution completion.
The existing Task `send_message` tool retains its reference-only semantics.
Workers and result-summary sessions cannot invoke Assistant messaging tools.

Each communication conversation has separate durable model context and event transcript from
the assistant's user chat. Incoming text is classified as untrusted reference data, with a fixed processing request; it never becomes user authorization. It uses the receiving assistant's selected configuration
at conversation creation, current assistant instructions and existing task hub.
Provider credentials stay at the receiving Control. Messages carry trusted sender
metadata but their bodies are untrusted peer content, not user authorization.
Existing machine permissions continue to apply. Agent responses to peers use
`send_assistant_message`; final model text is not automatically forwarded.

Messages are text-only, bounded to 64 KiB. A source file path or download link does
not transfer a file or grant access on another Control. Automatic message chains
are bounded to 16 hops, including new conversations started while processing an
incoming message, to prevent unbounded agent reply loops. Agents should reply only
when useful and avoid courtesy-only acknowledgements.

## Delivery and recovery

Local sends enter the destination Inbox directly. Cross-Space sends enter the
source Outbox. App checks endpoint identities, exchanges assistant directories,
reads bounded batches for the exact destination Control, delivers them and only
then acknowledges the source. Both directions are attempted independently. UI
selection has no effect on routing or background relay operation.

The source retains the immutable envelope. The destination durably accepts before
returning its receipt. A receipt binds the complete envelope, including content and
both participants. Duplicate IDs with identical content return the original
acceptance; conflicting content fails. An App crash after delivery but before
acknowledgement causes safe redelivery, not a second Agent turn. App needs no
separate message database.

App must run to move cross-Space messages. Accepted remote work may continue after
App exits; replies remain queued until it reconnects. Identity mismatch fails
explicitly and never redirects queued work to a different Control. The App relay
uses existing authenticated connections; only owner-authorized native requests
can register directories, deliver envelopes or acknowledge messages.

Inbox states distinguish `queued`, `processing`, `waiting`, `processed`, `failed` and
`interrupted`; Outbox states are `queued` and `delivered`. A processing attempt is
persisted before model invocation. Restart marks incomplete attempts interrupted
instead of replaying potentially effective tools. Failed/interrupted attempts
fence later inputs in that local conversation; another conversation can proceed.
Allowance exhaustion before a model request retains a `waiting` envelope only
when the persisted checkpoint has no started model/tool or pending tool effects.
Automatic or owner-released continuation reuses that envelope and checkpoint,
without adding its request twice. Correspondence waits have separate scope keys;
manual continuation validates the recipient assistant. A waiting correspondence
fences later messages in that correspondence while other correspondences may run.
Unknown outcomes require investigation. An unconfigured receiver retains queued
messages. Diagnostics remain available through history. Native Chat projects the
latest inbound peer reply for conversations initiated by its assistant; there is
no separate mailbox or handoff UI. See [Multi-Space UI](multi-space-ui.md).

## API

`POST /api/v1/assistant-mail` accepts the tagged `RelayRequest` contract:
`directory`, `register`, `pending`, `deliver`, `acknowledge`, or `history`.
History selects an assistant and sequence cursor; batches are bounded to 32
records. The endpoint requires existing native owner authority, not an execution
lane token or browser session. Model calls cannot supply their sender identity.
Protocol schemas and bounds live in `internal/protocol/mail.go`.
