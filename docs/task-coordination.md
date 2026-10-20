# Task coordination and lifecycle

Task guidance, reference messages and completion events share the Agent inbox;
see [Agent messages](assistant-messages.md). Main manages Tasks. Workers can
message only their owning assistant; they cannot create, inspect, stop or wait
for other Tasks. Task-result and plugin-notification handling retain the
assistant's configured tools and current machine permissions. A batch with
eligible failed work additionally exposes the bounded retry tool described below.

## Linked execution

`create_task` accepts `link_id` and an optional assistant `target` address.
Explicit targets select that assistant; otherwise the receiving node resolves its
persisted default once. The receiver owns execution, model selection and local
permissions. Local provider credentials and model snapshots are never forwarded.
The directing node stores an idempotent delegation and remote reference rather
than launching a local worker copy. Results notify the original directing
assistant through the existing inbox and task-outcome projection.

Linked tasks use the same lists and detail view, with a Codex or Pot source icon.
Cancellation targets the original accepted task; local archive state is supported.
Steering, pause, local allowance controls, remote file reads and execution
transcripts require additional adapter capabilities and are currently unavailable.
Disconnected nodes retain the last accepted state without replaying work.

## Autonomous model routing

The invoking assistant selects a configured Provider and model using the task,
explicit user instructions, its AGENTS.md and current model capabilities. Routing
requires no user confirmation, Profile allowlist, worker default or Follow Chat
setting. Omitted selection snapshots the invoking assistant's active model
configuration. Explicit unavailable selections fail visibly. Capacity and token
allowances still apply, and choosing a model grants no machine/tool authority.
`create_task` accepts an optional configured connection `profile` ID and concrete
`model` ID. A selected connection without a model uses its default model. When
both are omitted, the invoking assistant's active configuration is copied.
Existing Tasks retain their model snapshots; each schedule occurrence creates a
new Task and resolves its selection then. Machine placement is independent of
model selection.

## Messages and completion

`send_message({to: {node_id, kind: "task", agent_id}, text})` sends an input
to an existing Task through its owning Control. Runtime records the sender from
the authenticated execution and binds any processing parent automatically; the
model supplies only the recipient and text. User inputs and owner-assistant
guidance are processed at safe model boundaries, with pending user guidance
first. Reference messages retain their source and grant no additional authority.
Owner guidance to an ended Task reopens it and starts a new Run with its
retained context and original workspace. Peer references never restart ended
Tasks.

Task history projects initial input, assistant guidance, user guidance and peer
reference from the shared `agent_messages` table. The operator messages HTTP
endpoint binds user provenance; model tools cannot choose their sender. Local workers consume and acknowledge inputs against the original run.
Linked adapters support only their advertised input capabilities.

The model-visible `create_task` requires a title followed by complete execution
instructions, preserving exact user commands and constraints; title-only briefs
are rejected before a Worker is launched.

After arranging all requested work, Main ends its chat turn silently and remains
available for independent requests. Creation failures remain in the current turn
for model-directed repair. Runtime commits a completion event with each finalized terminal result and
processes currently available outcomes together, including scheduled Tasks.
Useful results, unresolved blockers and needed user decisions produce a reply;
routine updates can complete silently.
`get_task` returns the same canonical outcome used for automatic reporting.
There is no model-visible Task wait operation.
An explicit result query and the automatic notification share result-report state
so the same outcome is not reported twice. `wait` collects JavaScript composition
results; `write_stdin` manages a worker's command session.

### Automatic failure recovery

A delivered failed manual Task may receive one model-directed replacement through
`retry_task({task_id, instructions})`. Runtime binds the source to the owner's
currently claimed result batch. The assistant retains its ordinary configured
tools, including commands, messaging, scheduling and task creation. `get_task`
can inspect any task owned by this assistant.
Worker output and plugin events remain untrusted reference data.

The replacement retains the original brief, all additional inputs with their
provenance, machine, resolved model, plugins, Skills and requirements. It runs
under current machine authorization in a new workspace; old files are not imported.
The model supplies only a repair approach and must not repeat owner-denied work
or uncertain effects that it cannot verify. An unresolved blocker or required
decision is reported to the user. Accepted recovery is silent; completion is
processed through the replacement's normal result event under the same reply policy.

The replacement and its retry receipt commit atomically. Repeated calls return
the same Task, and a replacement cannot itself be retried automatically, including
after restart. Result handling admits at most eight model requests per delivery.
Cancelled/interrupted Tasks are not restarted. Scheduled failures retain their
existing occurrence and notification policy rather than launching a manual copy.

Pending approvals still expose the exact reviewed operation in Chat.
Approval waiting retains the execution lease and route capacity until the owner
resolves the request.

## Execution time

The Agent continues model/tool rounds until it produces a final response without
pending tool calls or guidance. There is no fixed model-round count limit.
Main conversations also have no default per-turn model-request count limit.
Cancellation, configured request/token allowances, context limits and individual
operation deadlines still apply; context compaction preserves continuation.

The destination Runner persists `started_at_ms` when it first claims an accepted
run. Subsequent claims after allowance resume retain that timestamp. Control
copies it into Task info from the original run receipt, including when the first
observed receipt is waiting or terminal. Zero means execution has not started or
its original start time is unavailable; creation and update timestamps are never
substituted. Chat and ongoing Task-list rows use this timestamp for total elapsed
wall time, including later waits, while Task details show the local start date
and time. Ended Task-list rows do not retain a live clock.

## Pause at a model boundary

`POST /api/v1/tasks/{id}/control` accepts `{run_id, action: "pause" | "resume"}`
for the original accepted run. Task info exposes `paused`; the fixed destination,
configuration and run identity are unchanged. In-flight model/tool operations
settle normally, then the local Worker waits before the next model
request. Pending guidance remains accepted while paused. Cancellation wakes the
wait and retains the usual cleanup and uncertainty rules. This control is
separate from releasing an allowance-waiting run.

## Stop or abandon

```text
running --interrupt--> stopping --cleanup confirmed--> interrupted
running --cancel_task--> stopping --cleanup confirmed--> cancelled
interrupted --cancel_task--> cancelled (Archived)
ended Task --owner guidance--> open Task + new Run ID + original workspace
```

`interrupt` retains never-started guidance as history. It does not replay
executing input, including suspended allowance-waiting input. New owner guidance
can continue an ended Task after cleanup is confirmed. Inputs retained by an
interruption are history and are not automatically replayed. Node restart reconciles accepted local runs by their original input identity,
without relaunching them or changing context. Historical remote runs remain
fenced at the retired destination. Owner reconciliation settles known receipts
and proven cleanup; unknown outcomes block replacement. See
[Execution and recovery](architecture.md#execution-and-recovery).

Owner recovery uses `POST /api/v1/tasks/{id}/recovery` with the original
`run_id` and `action: "inspect" | "reconcile"`. A Runtime owner can address the
same original destination journal through `POST /api/v1/runtime/recovery` with
`control_id`, `task_id`, `run_id` and the action. These private/native ports are
excluded from browser publication. Inspection returns receipt states and unresolved
reasons without starting a model or tool. Reconciliation consults the
original coordinator operations and local command receipts. It accepts no client-supplied result or cleanup
acknowledgment. Known results update the original checkpoint once; only proven
cleanup releases held capacity and ends the run as interrupted. A previous-boot
Host run with issued operations and no recorded cleanup proof remains fenced.
Missing/unknown effects are never converted into failure or retried.

`cancel_task` discards pending guidance and abandons the Task. History/files survive;
completed Run results stay complete. Continuing creates a new Run; it never
rewrites a terminal receipt or replays interrupted operations.

Both operations reject guidance during cleanup. Running Tasks hold capacity and
leases until shutdown is confirmed; unconfirmed shutdown fails visibly and retains
active leases until original receipts and cleanup are reconciled. App/Web Stop interrupts. CLI `tasks interrupt`
and `tasks cancel` use the respective `/api/v1/tasks/{id}/interrupt` and `/cancel` APIs.

## Archive and continuation

Archive is an independent `TaskInfo.archived` flag, not a Run phase. A successful
Worker result ends the Run and releases execution resources, leaving the Task
open for the owning assistant's decision. Without a model decision, it stays open.

| Group | Selection |
|---|---|
| Ongoing | Non-archived `queued`, `running`, `waiting`, `waiting_approval` |
| Needs attention | Non-archived ended Runs, including successful work kept for follow-up |
| Archived | `archived: true`, independent of the last Run's outcome |

The assistant's `set_task_dispositions({decisions})` accepts 1–8 decisions in one
atomic batch. Each decision supplies `task_id`, current `run_id`, `archived` and
`reason`. `POST /api/v1/tasks/dispositions` exposes the same owner-scoped operation
for clients. A running Run, pending owner guidance, or a different current Run
rejects the batch without partial changes. Result handling uses the same
owner-scoped archive operation as user chat. Workers cannot make archive
decisions. Explicit cancellation archives after confirmed cleanup.

The owning assistant judges whether delivery is complete or feedback is needed.
Runtime imposes no Git cleanliness, commit, push, PR or merge requirement. Archive
retains files, model context and history; it never deletes a workspace or reclaims
storage. Inactivity does not archive a Task.

Owner guidance, including `POST /api/v1/tasks/{id}/messages`, removes the archive
flag and durably queues input. An active Run consumes guidance at a safe boundary.
Guidance arriving after its finalization boundary starts the next Run after the
terminal result commits. Multiple arrivals join that Run's input queue. A reference
message alone never starts execution, and stopping Runs reject new guidance until
cleanup is confirmed.

Continuation preserves Task identity/address, creation brief, model selection,
original machine and execution target, workspace, and settled model history. A
new Run ID has separate execution receipts, approvals, start time and outcome.
The session generation stays fixed. Previous terminal Control records are retained
in `task_run_history`; destination Runner journals retain every Run. Task transcripts
use one monotonic cursor across Runs. Original input is not executed again; only
the new owner guidance starts the continuation. Scheduled occurrences still create
independent Tasks; a user continuation becomes manual work without changing that
occurrence's result or baseline.

A missing workspace or unavailable original machine is reported rather than
silently selecting another directory or destination. A previous run with uncertain
effects must be reconciled before a continuation can execute. Subsequent tools
always use the machine's current execution authorization.

Task and Runner record payloads use schema versions 9 and 7. Database schemas and
automatic upgrades are owned by the [DB migration layer](database-migrations.md).
Development builds use an independent data root; unsupported pre-migration formats
are rejected.

## Client notification outcomes

`GET /api/v1/tasks/outcomes` is an owner-scoped, read-only projection for running
clients. Without `after`, it returns the current outcome `cursor` with empty
`items`, establishing a baseline without reading the archive. With `after=CURSOR`,
it returns up to 100 transitions ordered by durable `sequence`, plus `cursor`
and `has_more`. Advance to the returned cursor and continue while `has_more`.
Sequences are safe JSON integers; gaps belonging to other owners are expected.

Terminal phase changes append to `task_outcomes` in the same transaction as the
Task. Rows contain only sequence, owner, Task ID, title, phase and update time;
they include no goal, result text, tool arguments or credentials. Repeated
same-phase metadata writes add no row. A complete-to-failed schedule validation
is finalized before an outcome is recorded. Suppressed schedule outcomes do not
enter the client notification projection. Owner/sequence indexes bound each incremental read.
The projection has no subscriptions, delivery workers or acknowledgments and
does not change Agent result reporting. Initial startup and reconnect establish
a fresh baseline rather than replaying old outcomes.

After a committed outcome advances the indexed head, Control invalidates
`task-outcomes`. Ordinary transcript updates still invalidate `tasks` but do not
wake the client notification reader. See [System notifications](shared-ui.md#system-notifications).

## Execution transcript

`GET /api/v1/tasks/{id}/events?after=CURSOR&limit=100` reads a bounded,
append-only page from the original Runner across all of the Task's Runs. Each item carries a durable
`sequence` plus its generation/turn-bound Agent event. The page returns `cursor`
and `has_more`; clients advance only through contiguous sequences. Historical remote pages remain cached with `unavailable: true`; retired
Control/Runner destinations cannot execute or fetch new output. Agent Link task
details expose their canonical result and return an empty transcript until the
adapter advertises transcript support. Changes wake the existing `tasks` invalidation stream.
