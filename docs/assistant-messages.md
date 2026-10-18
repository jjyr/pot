# Agent messages

`send_message` is the single model-visible primitive for Task and assistant
messages. Runtime lifecycle hooks use the same durable enqueue operation.
Messaging coordinates existing agents; it does not migrate Tasks, transfer
credentials or grant execution permissions.

## Addresses and provenance

An Agent address is `{control_id, kind, agent_id}`. Model recipients may have
kind `assistant` or `task`; Task addresses belong to their owning Control,
independently of the destination machine running the Task. `list_assistants`
returns assistant addresses; Task metadata includes its address. Worker context
contains its own address and its owning assistant's address.

`send_message({to, text})` returns durable acceptance, never execution
completion. Control binds the envelope's `from` identity to the current execution;
models cannot provide a sender, impersonate a user or produce Runtime events.
Owner-origin messages guide active Tasks at a model boundary. Peer information
is reference data, not new user authority. Ended Tasks retain reference without
restarting; user requests for more work require a new Task. Workers can message
only their owning assistant and cannot delegate or acquire management tools.
Runtime binds the processing parent from the authenticated execution, never tool
arguments. Replies to that parent's sender automatically set wire `reply_to` to
the parent's message ID and retain its conversation; forwarding starts a new wire
conversation while retaining the causal hop count. Task inputs bind to their
original run, with any processing parent recorded automatically. Inputs forwarded
while handling agent correspondence remain references, never owner guidance.

## Shared storage and receivers

`internal/inbox` owns atomic envelope insertion and identity/content conflict
checks. `agent_messages` in the Control's `tasks.sqlite` stores Task inputs,
assistant correspondence, task-result events and plugin notifications. Task input
and notification views are transcript/receiver projections over this table, not separate queues.
Task completion and its event commit in the same transaction. Task schema 7 and
schedule schema 2 require the unified outcome format; older schemas are rejected
without importing legacy queues or schedule notifications.

The assistant's user inbox is separate and has priority at turn boundaries.
Accepted requests arriving during an automatic read-only summary start their
own Main turn afterwards; they do not inherit the summary's role. Pending user
inputs survive restart. Selecting an input, persisting its turn and removing it
from the user queue are atomic. Retries retain their original target and receipt,
including after the summary ends. Started, uncertain executions remain fenced;
reconciling the original turn then admits separately accepted pending input.

Assistant correspondence is grouped by the full sender Agent address and owning
recipient. A peer retains one independent model context across wire conversations;
different senders, including equal Agent IDs on different Controls, remain separate.
Task messages are consumed by the original run at safe model boundaries; they
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
the shared inbox rather than another queue. A notification summary reports each
delivered task's success/failure and summarizes external plugin events with their
provenance and source links. It acknowledges each member after its reply is durable.
Peer messages from one sender are claimed atomically and supplied together as untrusted
reference data. Runtime selects the batch member with the highest hop count as
the processing parent; automatic replies to its sender retain that member's wire
conversation and the batch's maximum causal hop. Models do not select individual
reply messages. Allowance resume retains exact membership and does not absorb
later arrivals. An uncertain peer blocks its
own sender group; unrelated peers continue independently.

Approval events remain UI-only hooks and never join these batches.

## Completion events

Main dispatches background work, acknowledges its accepted state and ends its
turn. It exposes no Task waiting tool and does not poll for completion.

A [terminal hook](hooks.md) creates one event identified by Task and run for
completion, failure, interruption or cancellation, including stopping before
dispatch. Approval hooks only update UI and user reminders. When user chat is
available, its receiver claims a batch of currently pending outcomes and produces
one concise report using each Task title and actual outcome. Ordinary and
scheduled outcomes share this batch, delivery journal and acknowledgment path
with host-accepted [plugin notifications](plugins.md#background-services-and-assistant-notifications).
Each source keeps its own payload format; plugins never impersonate a Task or an
assistant peer. The receiver uses one readiness/read/claim/Report/release path,
and plugin acceptance wakes it through the existing inbox change subscription.
The receiver reads immutable event data rather than reinterpreting current Task
state. Schedule policy can suppress an outcome before delivery; no separate
schedule notification queue or reporting turn exists. Summaries have no
tools, do not infer undelivered Tasks' states and omit identifiers from prose. Real Task identifiers remain
available for artifact download URLs.

Events become processed only after the reply is durable. A successful explicit
result query acknowledges the same result event at turn completion; a failed
query turn leaves it eligible for automatic delivery. Repeated hooks cannot
reset a processed event. Failed/interrupted delivery remains recorded for
inspection rather than automatically replaying uncertain model requests.

## Cross-Space delivery

A Space is a Control identity. Local messages enter the shared inbox directly;
remote assistant correspondence enters the source Outbox. App verifies endpoint
identities, exchanges directories, delivers envelopes and then acknowledges their
immutable receipts. Relay preserves the original sender and does not become the
author. Both directions operate independently of UI selection. Native owner
ports perform relay operations; peer transport cannot forge Runtime task events.

App must run to carry cross-Space messages. Assistant replies remain queued while
it is absent. No new pairing, credential store or network authorization is added.
Web and CLI do not run a relay. Remote Task inputs continue through their original
Control/Runner binding rather than moving execution to a different machine.

Messages are bounded to 64 KiB. Files and permissions are not transferred by
sending text or a path. Automatic peer chains retain a 16-hop bound, including
new conversations created while handling a received message. Agents reply only
when useful and avoid acknowledgement loops.

## API and recovery

`POST /api/v1/assistant-mail` remains the owner-authorized relay transport port.
Its tagged operations are `directory`, `register`, `pending`, `deliver`,
`acknowledge` and `history`. Addresses and envelopes follow the Agent contract in
`pkg/protocol/mail.go`; history projects assistant correspondence only.

Inbox states include queued, processing, waiting, processed, failed, interrupted
and policy-suppressed (`suppressed`) outcomes. A processing attempt is persisted
before model invocation. Restart marks incomplete attempts interrupted. Allowance continuation reuses only a
stopped checkpoint with no started model/tool effects. A fenced sender group
does not replay its input; other peer contexts remain independent.
