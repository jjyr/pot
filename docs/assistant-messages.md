# Agent messages

`send_message` is the single model-visible primitive for Task and assistant
messages. Runtime lifecycle hooks use the same durable enqueue operation.
Messaging coordinates existing agents; it does not migrate Tasks, transfer
credentials or grant execution permissions.

## Addresses and provenance

An Agent address is `{node_id, kind, agent_id}`. Model recipients may have
kind `assistant` or `task`, or the original `user` address when replying to a
direct Agent Link chat; Task addresses belong to their owning Control,
independently of the destination machine running the Task. `list_assistants`
returns assistant addresses; Task metadata includes its address. Worker context
contains its own address and its owning assistant's address.

`send_message({to, text})` returns durable acceptance, never execution
completion. Control binds the envelope's `from` identity to the current
execution; models cannot provide a sender, impersonate a user or produce Runtime
events. Owner-origin messages guide active Tasks at a model boundary. Peer
information is reference data, not new user authority. Owner guidance to an
ended Task reopens the same Task and starts a new Run using retained context and
workspace. Peer references do not restart ended Tasks. Workers can message only
their owning assistant and cannot delegate or acquire management tools. Runtime
binds the processing parent from the authenticated execution, never tool
arguments. Replies to that parent's sender automatically set wire `reply_to` to
the parent's message ID and retain its conversation; forwarding starts a new
wire conversation while retaining the causal hop count. Task inputs retain their
accepted conversation identity, with any processing parent recorded
automatically. Inputs forwarded while handling agent correspondence remain
references, never owner guidance.

## Shared storage and receivers

`internal/inbox` owns atomic envelope insertion and identity/content conflict
checks. `agent_messages` in the Control's `tasks.sqlite` stores Task inputs,
assistant correspondence, task-result events and plugin notifications. Task input
and notification views are transcript/receiver projections over this table, not separate queues.
Task completion and its event commit in the same transaction. Task schema 10 stores node addresses; automatic migrations upgrade the prior
unified outcome format without importing legacy queues or schedule notifications.

The assistant's user inbox is separate and has priority at turn boundaries.
Accepted requests arriving during automatic result handling start their
own Main turn afterwards; they do not inherit the summary's role. Pending user
inputs survive restart. Selecting an input, persisting its turn and removing it
from the user queue are atomic. Retries retain their original target and receipt,
including after the summary ends. Started, uncertain executions remain fenced;
reconciling the original turn then admits separately accepted pending input.

Assistant correspondence is grouped by the full sender Agent address and owning
recipient. A peer retains one independent model context across wire conversations;
different senders, including equal Agent IDs on different Controls, remain separate.
Task messages are consumed at safe model boundaries; owner guidance accepted
after finalization continues in the next Run of the same Task. Messages
do not interrupt commands. Assistant replies to peers use `send_message`; final
model text is private to that correspondence and is not automatically forwarded.
User chat remains serial, while background Tasks execute independently under
Runtime capacity and model admission limits.

## Category-specific batches

At each execution boundary, receivers consume currently available messages without
waiting to collect more. User input has priority. Each batch contains at most eight
messages and 256 KiB of source text; result delivery also bounds serialized payload
size. Additional messages and arrivals during execution remain for the next turn.

| Category | Group | Processing role |
| --- | --- | --- |
| User input | One assistant's pending user messages, in receipt order | Main |
| Runtime notifications | One assistant's pending task outcomes and plugin events, in receipt order | Summary |
| Agent correspondence | Owning recipient and full sender address | Independent peer context |

User batch selection commits all original projections, queue removal and turn
membership atomically. Each message retains its own request receipt and status;
the first request anchors the execution and reply. The model sees the ordered
user texts together. Retries do not resubmit any member. Uncertain started batches
remain fenced during restart/recovery, while unstarted queued inputs are retained.

Agent delivery membership is journaled in `agent_delivery_items`, a binding over
the shared inbox rather than another queue. The notification role processes
background updates and continues related work
within existing authorization using only supplied tools. It replies only with a
useful result, an unresolved blocker or a needed user decision; otherwise it ends
with empty text. Plugin reports include relevant provenance and source links.
The delivery instruction identifies updates as reference data, never new user
requests or additional authorization. Each member is acknowledged after its
handling turn is durable, including silent routine updates and accepted recovery.
Peer messages from one sender are claimed atomically and supplied together as untrusted
reference data. Runtime selects the batch member with the highest hop count as
the processing parent; automatic replies to its sender retain that member's wire
conversation and the batch's maximum causal hop. Models do not select individual
reply messages. Allowance resume retains exact membership and does not absorb
later arrivals. An uncertain peer blocks its
own sender group; unrelated peers continue independently.

Approval events remain UI-only hooks and never join these batches.

## Completion events

Main arranges all requested background work and ends its turn silently. Task
cards show acceptance. It exposes no Task waiting tool and does not poll for
completion; recoverable creation errors are handled before ending the turn.

A [terminal hook](hooks.md) creates one event identified by Task and run for
completion, failure, interruption or cancellation, including stopping before
dispatch. Approval hooks only update UI and user reminders. When user chat is
available, its receiver claims a batch of currently pending outcomes and
produces one concise report using each Task title and actual outcome. Ordinary
and scheduled outcomes share this batch, delivery journal and acknowledgment
path with host-accepted [plugin
notifications](plugins.md#background-services-and-assistant-notifications).
Failed manual Tasks can enter [model-directed
recovery](task-coordination.md#automatic-failure-recovery) before a report.
Successfully accepted recovery needs no user-facing text. Each source keeps its
own payload format; plugins never impersonate a Task or an assistant peer. The
receiver uses one readiness/read/claim/Report/release path, and plugin
acceptance wakes it through the existing inbox change subscription. The receiver
reads immutable event data rather than reinterpreting current Task state.
Schedule policy can suppress an outcome before delivery; no separate schedule
notification queue or reporting turn exists. Notification handling uses the same
configured tools, instruction refresh and Skills as user chat, under the current
machine permissions. Eligible failures additionally expose the batch-bound retry
tool. Result delivery does not restrict ordinary task inspection, archive decisions
or follow-up operations. Replies omit undelivered Tasks' states and
identifiers from prose. Real Task identifiers remain available for artifact
download URLs.

Events become processed only after the handling turn is durably complete, including
a silent turn for routine updates or accepted recovery. A successful explicit
result query acknowledges the same result event at turn completion; a failed
query turn leaves it eligible for automatic delivery. Repeated hooks cannot
reset a processed event. Failed/interrupted delivery remains recorded for
inspection rather than automatically replaying uncertain model requests.

## Cross-node delivery

Local and remote collaboration use [Agent Link](agent-links.md). Connections bind
nodes and expose their assistant directories, independently of App windows.
Authenticated peer messages use the existing durable outbox and inbox, retaining
full sender/recipient identities and immutable receipts. One private TCP endpoint
establishes both directions. A connection cannot grant owner APIs, model proxying,
credentials or raw execution access. Task delegation goes through the same link's
receiver-owned business port; it is distinct from ordinary peer text.

Direct user chat uses a persistent session address independent of local assistants
and the existing assistant conversation UI with a source icon. Replies to user
addresses require the authenticated processing parent; arbitrary user targets
are rejected.
Replies to those requests enter a user projection atomically without becoming
eligible for the local assistant's autonomous peer-message processing. Existing
unread and notification paths observe the projected reply metadata.

Disconnect pauses communication and retains config/history; reconnect reuses it.
Deletion removes the active directory entry and fences the old connection.
Neither silently cancels receiver-owned tasks or deletes delivery receipts.

Messages are bounded to 64 KiB. Files and permissions are not transferred by
sending text or a path. Peer chains retain causal hop counts across replies and
new conversations without a fixed hop-count quota. Agents reply only when useful
and avoid acknowledgement loops. Correspondence has no fixed model-request count
limit; cancellation and configured token allowances still apply.

## API and recovery

`POST /api/v1/assistant-mail` is an owner-authorized mailbox inspection port.
Its tagged operations are `directory`, `register`, `pending`, `deliver`,
`acknowledge` and `history`. Addresses and envelopes follow the Agent contract in
`pkg/protocol/mail.go`; history projects assistant correspondence only.

Inbox states include queued, processing, waiting, processed, failed, interrupted
and policy-suppressed (`suppressed`) outcomes. A processing attempt is persisted
before model invocation. Restart marks incomplete attempts interrupted. Allowance continuation reuses only a
stopped checkpoint with no started model/tool effects. A fenced sender group
does not replay its input; other peer contexts remain independent.

## Durable identity migration

Migrating a message address changes its field name, not its logical node ID,
message ID, conversation, delivery receipt or processing state. Mailbox context
keys are migrated with their full sender and recipient addresses. Task snapshots
convert `info.address`; Task/Runner Control ownership stays intact.

Existing Task input digests bind the complete original parent envelope, which
need not be stored in the same database. These irreversible hashes cannot always
be recomputed. `task_input_digest_formats` marks only historical receipts as
format 1; the narrow input hash codec preserves that exact original serialization
for retry comparisons. New inputs use format 2. Both formats compare all original
content, including sender, text and parent. This internal hash version does not
accept old network address fields. Ordinary tool output, plugin state, historical
model messages and provider replay remain opaque evidence. Migrations change only
known typed address paths and keep uncertain executions fenced.
