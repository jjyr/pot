# Agent messages

`send_message` is the single model-visible primitive for Task and assistant
messages. Runtime lifecycle hooks use the same durable enqueue operation.
Messaging coordinates existing agents; it does not migrate Tasks, transfer
credentials or grant execution permissions.

## Addresses and provenance

Assistant mention completions read undeleted local definitions and the current
Agent Link directory snapshots. Contacts and the bounded name/avatar cache used
to display historical messages are not selection directories. Opening an `@`
query waits for a current completion read; assistant and Agent Link invalidations
retire visible options and fence pending reads before refreshing both Chat and
Task guidance. Existing inline references and drafts keep their full addresses.
Remote deletion becomes known when the receiver's directory is refreshed by the
existing Agent Link lifecycle. An unreachable peer without deletion evidence
retains its cached directory and the existing offline indication; opening the
picker does not connect to peers or infer deletion from connectivity.

The owning node's valid directory is the authority for current assistant
identity. `list_assistants` uses the Control `ListAssistants` query over those
same local definitions and saved receiver directories, with its existing remote
availability policy. UI views can require capabilities such as `chat.read`;
absence from that narrower view does not retire a profile, message or task
target. A confirmed management deletion removes the full address from the saved
directory before a best-effort refresh, so tools, completions and UI projections
observe the retirement even when that refresh fails. A rejected deletion leaves
the active directory intact.

An Agent address is `{node_id, kind, agent_id}`. Model recipients may have
kind `assistant` or `task`, or the original `user` address when replying to a
direct Agent Link chat; Task addresses belong to their owning Control,
independently of the destination machine running the Task. `list_assistants`
returns assistant addresses; Task metadata includes its address. Worker context
contains its own address and its owning assistant's address.

`send_message({to, text})` returns durable acceptance, never execution
completion. A recipient's model activity or compaction does not gate inbox
acceptance. Queued correspondence enters the conversation and its receive-message
UI record when the owning serial lane is ready; acceptance alone does not mean
the recipient has seen the message. Control binds the envelope's `from` identity
to the current execution; models cannot provide a sender, impersonate a user or produce Runtime
events. Owner-origin messages guide active Tasks at a model boundary. Peer
information is reference data, not new user authority. Owner guidance to an
ended Task reopens the same Task and starts a new Run using retained context and
workspace. Peer references do not restart ended Tasks. Workers can message their
owning assistant and ordinary local Tasks owned by that same assistant. Full Task
addresses must be supplied through task context, briefs or guidance; workers gain no Task discovery, delegation or management tools.
The receiver validates both Tasks against the assistant-scoped catalogue; guessed
IDs, cross-owner Tasks and wrong-node addresses do not bypass this boundary.
Runtime binds the sender to its current Task/run/generation. Cross-owner and
remote Task peer input are unsupported: the current linked input contract carries
owner guidance and cannot preserve Task peer provenance. Runtime binds the
processing parent from the authenticated execution, never tool arguments. Replies to that parent's sender automatically set wire `reply_to` to
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
Task completion and its event commit in the same transaction. The initial schema
stores node addresses and does not adopt pre-reset development databases or import
legacy queues; see [Database migrations](database-migrations.md).

The assistant's user inbox is separate and has priority at turn boundaries.
Accepted requests arriving during automatic result handling start their
own Main turn afterwards; they do not inherit the summary's role. Pending user
inputs survive restart. Selecting an input, persisting its turn and removing it
from the user queue are atomic. Retries retain their original target and receipt,
including after the summary ends. Started, uncertain executions remain fenced;
reconciling the original turn then admits separately accepted pending input.

Assistant correspondence is grouped by the full sender Agent address and owning
recipient, then enters the owning assistant conversation as reference input.
Correspondence shares its history, configuration, serial execution and allowance.
Different assistants retain separate histories; sender addresses and wire
conversations retain their exact identities.
Task messages are consumed at safe model boundaries; owner guidance accepted
after finalization continues in the next Run of the same Task. Messages
do not interrupt commands. Local Task receipts include `input_state`, a snapshot
of the durable receiver input: `pending` awaits a safe model boundary, `consumed`
has entered the receiver context, `retained` is saved without delivery, and
`cancelled` was discarded by cancellation.
Queued Tasks receive pending references when normal execution starts. Running
Tasks receive them at the next boundary. Waiting (allowance or approval) and
paused Tasks keep pending references until normal execution resumes; a message
does not resume them or approve anything. References arriving during finalization,
stopping or after the run ends are retained only, never a reason to launch a Run.
Even a pending reference can become retained if the run ends before consuming it.
Acceptance and input consumption never prove the requested work ran. Assistant
replies to peers use `send_message`; final
model text can report to the user in the owning conversation and is not
automatically forwarded to the peer. Incoming envelopes appear as collapsed
receive-message records, like tool calls, rather than ordinary chat prose.
Chat and Task tool disclosures label incoming assistant messages as
`Receive message from @name` and outgoing `send_message` calls as `Send message to @name`.
The shared UI resolves names from the existing directory using the complete
node, kind and agent address, with the same mention and linked-source display
used elsewhere. Directory refreshes update names; offline or removed entries can
retain a known name in a bounded, memory-only cache for the current Runtime.
Without a known name the disclosure says `Assistant unavailable`; Task and user
targets retain their type labels. An incomplete target says `Recipient unavailable`.
Expanded execution details retain original tool names and full addresses.
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
| Agent correspondence | Owning recipient and full sender address | Owning assistant conversation |

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
reference data. Pending user inputs have priority; correspondence cannot start a
parallel model session or interrupt a running turn. Runtime selects the batch
member with the highest hop count as
the processing parent; automatic replies to its sender retain that member's wire
conversation and the batch's maximum causal hop. Models do not select individual
reply messages. Allowance resume retains exact membership and does not absorb
later arrivals. The assistant conversation owns allowance recovery and
uncertain-execution
fencing. Previously saved private correspondence checkpoints remain inspectable
and are not automatically replayed into the new shared context.

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
before model invocation. Restart marks incomplete attempts interrupted.
Allowance continuation uses the owning assistant conversation and reuses only a
stopped checkpoint with no started model/tool effects. Uncertain correspondence
is not automatically replayed. Its immutable receipt remains available for
inspection, including the collapsed history record.

A confirmed failed handling turn does not block later queued correspondence from
the same sender, including a new wire conversation. Active, waiting and interrupted
attempts retain their existing receiver fences, and the conversation's uncertain
cleanup gate still prevents execution. A verified continuation or safe allowance
resume (including after restart) restores only its original delivery journal
members before resuming the checkpoint; it does not claim later arrivals.
Cancellation and turn completion retire the old sender
binding. Parent-state validation and new mail insertion share a transaction;
an already accepted request retains its immutable receipt after the parent ends.

## Durable identity receipts

Message IDs, full node addresses, conversations and delivery receipts remain
stable across retries and restarts. New Task input digests use format 2 and bind
the complete sender, text and parent envelope. `task_input_digest_formats` is a
separate receipt table; it is empty in a new database. The first public baseline
does not convert old development message addresses or reconstruct their hashes.
See [Database migrations](database-migrations.md) for the reset boundary.
Ordinary tool output, plugin state, historical model messages and provider replay
remain opaque evidence; restart recovery keeps uncertain executions fenced.

## Conversation presentation

Chat and Task transcripts share the communication tool disclosure. Its summary
keeps the operation and resolved full-address identities, with a compact pair of
avatar or brand endpoints and a static directional line. Names and brands come
from the existing assistant directory/cache; unknown addresses keep a neutral
node. The direction line contains no channel label; channel and tool information
remain in secondary Call details. Offline is an endpoint property and never
changes Task phase. Ordinary send and receive records start collapsed. Manual
choices survive message, directory, stream, locale and theme updates; existing
approval and explicit navigation rules still apply.

Opening a message hides the summary relation diagram and shows a sender avatar
and readable name, recipient and necessary source on a single identity row.
The complete parsed `text` follows through the shared safe Markdown renderer,
without a bubble, outer card, tinted fill or decorative left rule. Full address
JSON stays in the secondary disclosure. Receive batches retain every envelope's own from/to/body;
summary counts distinguish one sender with several messages from several sources.
Incomplete arguments show preparation, and unavailable bodies point to the
secondary Call details disclosure. That disclosure retains the tool name, full
addresses, arguments, result and diagnostic output with copy controls. Tool
failures have a separate error area.

Only an explicit successful `accepted: true` receipt is labeled Accepted. Received
here means the envelope entered this conversation; neither label implies delivery,
reading or business processing. Envelopes have no sender timestamp, so none is
invented. `create_task` is a distinct delegation operation with the existing Task
detail entry; its tool receipt says Task created, while the detail owns current
Task progress and approvals. Ordinary task guidance remains a message.
