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

`send_message({to, text, reply_to?})` returns durable acceptance, never execution
completion. Control binds the envelope's `from` identity to the current execution;
models cannot provide a sender, impersonate a user or produce Runtime events.
Owner-origin messages guide active Tasks at a model boundary. Peer information
is reference data, not new user authority. Ended Tasks retain reference without
restarting; user requests for more work require a new Task. Workers can message
only their owning assistant and cannot delegate or acquire management tools.
`reply_to` binds assistant correspondence participants; Task inputs bind to their
original run instead of a correspondence reply.

## Shared storage and receivers

`internal/inbox` owns atomic envelope insertion and identity/content conflict
checks. `agent_messages` in the Control's `tasks.sqlite` stores Task inputs,
assistant correspondence and task-result events. Task input and notification
views are transcript/receiver projections over this table, not separate queues.
Task completion and its event commit in the same transaction. Schema 5 does not
import legacy Task queues or the former standalone assistant mailbox.

The assistant's user inbox is separate and has priority at turn boundaries.
Accepted requests arriving during an automatic read-only summary start their
own Main turn afterwards; they do not inherit the summary's role. Pending user
inputs survive restart. Selecting an input, persisting its turn and removing it
from the user queue are atomic. Retries retain their original target and receipt,
including after the summary ends. Started, uncertain executions remain fenced;
reconciling the original turn then admits separately accepted pending input.

Each assistant correspondence retains independent model context and transcript.
Task messages are consumed by the original run at safe model boundaries; they
do not interrupt commands. Assistant replies to peers use `send_message`; final
model text is private to that correspondence and is not automatically forwarded.
User chat remains serial, while background Tasks execute independently under
Runtime capacity and shared-resource policy.

## Completion events

Main dispatches background work, acknowledges its accepted state and ends its
turn. It exposes no Task waiting tool and does not poll for completion.

A [terminal hook](hooks.md) creates one event identified by Task and run for
completion, failure, interruption or cancellation, including stopping before
dispatch. Approval hooks only update UI and user reminders. When user chat is
available, its receiver claims that event and produces one concise report using
the Task title and actual outcome. Summaries have no tools, do not infer other
Tasks' states and omit identifiers from prose. Real Task identifiers remain
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

Inbox states include queued, processing, waiting, processed, failed and
interrupted. A processing attempt is persisted before model invocation. Restart
marks incomplete attempts interrupted. Allowance continuation reuses only a
stopped checkpoint with no started model/tool effects. A fenced correspondence
does not replay its input; other contexts remain independent.
