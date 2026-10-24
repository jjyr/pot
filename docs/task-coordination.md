# Task coordination and lifecycle

Task guidance, reference messages and completion events share the Agent inbox;
see [Agent messages](assistant-messages.md). Main manages Tasks. Workers can
message their owning assistant and ordinary local sibling Tasks under that owner
using supplied full addresses. Cross-owner and remote Task peer input are
unsupported. Workers cannot create, inspect, stop or wait for other Tasks.
Task-result and plugin-notification handling retain the
assistant's configured tools and current machine permissions. A batch with
eligible failed work additionally exposes the retry tool described below.

## Agent Link interface

Chat UI, task UI and model task tools use the Agent Link task interface for both
local and linked execution. Location is presentation metadata; effective
capabilities select supported controls. The local owner adapter supports explicit
serialized task configuration without an unexported native-only request field.
Only owner authority can select that configuration; peer callers retain the
receiver-owned execution policy.

Task is independent of execution location. Local Pot, peer Pot and Codex tasks
share one durable catalogue, request receipt, outcome cursor and archive batch.
Every submission calls Agent Link; the local adapter stays in-process. The Pot
receiving adapter accepts a local model configuration through owner-only
`task.configure`, and keeps run/checkpoint/workspace state internally. Linked
Tasks store their original caller-bound execution reference and last observation,
without requiring a local Provider or a Pot model snapshot. Exact retries return
the original Task before consulting mutable directories or defaults.

Execution nodes are selected by target addresses, never a separate machine selector.
Historical raw remote launches retain their original recovery fences and cannot
be converted into a new local execution.

An authenticated user conversation can submit linked work without any local
assistant or Provider. Its durable session owns the directing receipt and result
route; the receiving assistant owns execution. User task results return directly
to the original conversation and task notifications. They do not become an
assistant inbox event or acquire autonomous local processing. The aggregate task
catalogue and original conversation use the same retained task reference.

## Linked execution

`create_task` accepts a single optional `target` address. Omission selects the
current assistant. An assistant address selects that exact assistant; a node
address selects its independent task execution endpoint. A node address uses the
node ID as its `agent_id`. The host resolves transport internally. Task requests
never carry connection IDs, executor modes or machine selectors.

The receiver owns execution, model selection and local permissions. Other
receivers reject local profile, model, Skills and execution requirements before
admission. Assistant targets may accept a destination directory hint; node targets
choose their own workspace. Local credentials and model snapshots never cross
nodes.
The directing node stores an idempotent Task and execution reference rather
than launching a local worker copy. Assistant-directed results notify the
original directing assistant through its inbox and task-outcome projection;
user-directed results follow the conversation route described above.

Linked tasks use the same lists and detail view, with the receiving Adapter’s
source icon.
Cancellation targets the original accepted task; local archive state is supported.
Additional controls follow the receiving adapter's advertised operations. Codex
accepts guidance after a terminal run to continue the same task; live steering,
pause, local allowance controls, remote file reads and execution transcripts are
currently unavailable across nodes.
Disconnected nodes retain the last accepted state without replaying work.

## Agent execution

Every Task invokes the Agent Link port, including the in-process local Adapter.
The assistant copies a target address from the advertised catalogue. Capabilities
are attached directly to each target, independently of its Adapter name. The
sender does not search for a sole remote executor or infer an execution mode from
a connection. Missing, unsupported or disconnected destinations fail without
fallback. Exact retries resolve their original receipt before consulting current
routes, capabilities or defaults.

```text
User <--> directing assistant A
                  |
                  v
             Task catalogue
                  |
            Agent Link port
             /           \
     local Adapter    connected Adapter
                  |      (e.g. Codex)
                  v
       receiver-owned Agent execution
                  |
          Task status / outcome
```

A node may advertise assistant and node endpoints together, each with its own
capability scope. The same address type is used by Task submission and the product
operation port. Task receipts contain one `receiver` identity; a node executor
returns its independently owned Task address. Task identity remains distinct from
run identity. Cancellation, guidance and approval forwarding require the selected
endpoint's operations. Owner guidance cannot be synthesized from a peer reference
or forwarded message. Continuations retain the Task identity and produce distinct
run identities and completion notifications. Restart never replays uncertain work.

The [Codex Adapter](../integrations/codex/README.md) currently implements independent
persistent task sessions and terminal-run continuation. Its workspace, session and
approval implementation belong to that Adapter rather than the shared Task API.
Other Adapters can implement the same contract with their own Agent runtime.

Where approval forwarding is supported, only authenticated user operations in A's
existing permission interface may resolve the original Task's exact pending request
with `allow_once` or `deny`. Decisions bind the original caller, Task and execution,
and expire with that execution. The receiving Adapter owns automatic review;
there is no model approval tool or extra node grant. Assistant messages carry no
approval authority and cannot become a user-approved Task. The receiver declines
operations outside its message permissions and returns the denial.

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
new Task and resolves its selection then. Local model selection does not select a remote execution node.

## Messages and completion

`send_message({to: {node_id, kind: "task", agent_id}, text})` sends an input
to an existing Task through its owning node. Runtime records the sender from
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
Linked adapters support only their advertised input capabilities. The current
linked guidance contract carries owner instructions, so inputs bound to incoming
correspondence are rejected rather than upgraded from reference data to owner
authority.

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

## Results and artifacts

Manual and scheduled Tasks use their final assistant reply as the summary.
Earlier model replies and execution commentary remain in the transcript rather
than being concatenated into that summary. An empty final reply does not reuse
earlier commentary; ordinary terminal status reporting still applies.
Workers need no structured result file. Evidence and limitations can be ordinary
reply text or part of a deliverable.

Put optional deliverables in `.pot-artifacts/` under the Task workspace. Control
collects nested regular-file paths when the run ends and carries them in the
ordinary outcome's `artifacts` list. Missing/empty directories mean no attachments.
Collection is bounded to 64 files, 64 MiB total, 256 directory entries and 16 nested
directory levels; paths must be valid workspace-relative paths of at most 1000
bytes. Symlinks and special files are excluded. Files elsewhere in the workspace
remain working files. Collection uses the original, identity-checked workspace;
it never falls back to another directory or machine. Existing files in a continued
Task's artifact directory are included in the next result; workers should keep
that directory limited to the intended deliverables.

Collection problems are separate `artifacts_error` metadata. Valid attachments,
final reply, execution status and execution diagnostics survive; an attachment
problem never reruns or changes the success/failure of the work. The task view
shows the delivery diagnostic separately. Attachment paths do not snapshot bytes;
subsequent downloads retain the original workspace and file revision checks.
The owning assistant can reference an actual attachment with an explicit
`[Title](pot-file://TaskID/relative-path)` Markdown link. Completed-message
references capture the original Task binding and snapshot through the
[display resource service](display-resources.md).

During execution, workers may send an owner message when useful. The owner may
inspect or guide a Task during its own turns, with no required polling cadence.
Only explicit worker communication and terminal results enter the owner inbox;
ordinary model/tool rounds do not automatically push progress updates.

### Automatic failure recovery

A delivered failed manual Task may receive a model-directed replacement through
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
the same Task for that source, including after restart. If the replacement also
fails, its own delivered result may receive another model-directed recovery.
Neither the recovery chain nor result handling has a fixed attempt-count limit.
Cancelled/interrupted Tasks are not restarted. Scheduled failures retain their
existing occurrence and notification policy rather than launching a manual copy.

Pending approvals still expose the exact reviewed operation in Chat.
Approval waiting retains the execution lease and route capacity until the owner
resolves the request.

## Execution time

The Agent continues model/tool rounds until it produces a final response without
pending tool calls or guidance. There is no fixed model-round count limit.
Main conversations, automatic result handling and Agent correspondence also have
no implicit per-turn model-request count limit.
Cancellation, configured request/token allowances, context limits and individual
operation deadlines still apply; context compaction preserves continuation.

The local execution journal persists `started_at_ms` when it first claims an accepted
run. Subsequent claims after allowance resume retain that timestamp. The Task service
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
original Operations journal and verified archived execution evidence. It accepts no client-supplied result or cleanup
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

Chat omits routine `set_task_dispositions` tool cards while preparing, running or
successfully completed. Failed, denied, interrupted, unconfirmed or diagnostic
receipts remain inspectable. This presentation rule leaves model history,
persisted tool results and the task list's archive status intact.

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
The session generation stays fixed. Previous terminal Task records are retained
in `task_run_history`; local execution journals retain every Run. Task transcripts
use one monotonic cursor across Runs. Original input is not executed again; only
the new owner guidance starts the continuation. Scheduled occurrences still create
independent Tasks; a user continuation becomes manual work without changing that
occurrence's result or baseline.

A missing workspace or unavailable original machine is reported rather than
silently selecting another directory or destination. A previous run with uncertain
effects must be reconciled before a continuation can execute. Subsequent tools
always use the machine's current execution authorization.

Task and execution record payloads use schema versions 13 and 7. Database schemas and
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

After a committed outcome advances the indexed head, the node invalidates
`task-outcomes`. Ordinary transcript updates still invalidate `tasks` but do not
wake the client notification reader. See [System notifications](shared-ui.md#system-notifications).

## Execution transcript

`GET /api/v1/tasks/{id}/events?after=CURSOR&limit=100` reads a bounded,
append-only page from the original execution journal across all of the Task's Runs. Each item carries a durable
`sequence` plus its generation/turn-bound Agent event. The page returns `cursor`
and `has_more`; clients advance only through contiguous sequences. Historical remote pages remain cached with `unavailable: true`; retired
distributed destinations cannot execute or fetch new output. Agent Link task
details expose their canonical result. Clients request transcripts only when
the task advertises `task.events`; unsupported reads return a capability error.
Changes wake the existing `tasks` invalidation stream.
