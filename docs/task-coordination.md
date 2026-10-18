# Task coordination and lifecycle

Assistant communication is separate from Task reference messages; see
[Assistant messages](assistant-messages.md). Handoff is agent coordination, not a
Task migration operation.

Main manages Tasks; Workers cannot create, message, wait for, interrupt or cancel
other Tasks. Summary sessions only inspect Tasks.

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

## Guidance, messages and waiting

| Tool | Purpose |
|---|---|
| `guide_task` | Queue guidance for an active Task |
| `send_message` | Deliver reference information at a model boundary without starting or resuming work |
| `wait_task` | Bounded wait for any selected Task to complete or need attention; cursors suppress unchanged results |

Messages retain trusted sender identity, are consumed once and remain pending for
ended Tasks as retained history. They do not grant owner authority. Delivery
uses `pkg/agent`.

Task input history distinguishes the initial `input`, assistant `guidance`,
operator `user_guidance` and peer `reference` records. The task messages HTTP
endpoint stamps operator provenance; `guide_task` stamps assistant provenance.
Clients cannot choose their source through request fields. Both guidance kinds
share the same delivery, pending-count and lifecycle rules. Their ordered UI
projection is described in [UI events](ui-events.md#task-execution-transcripts).

`wait_task` returns Task metadata, not transcripts. Complete, failed, interrupted,
cancelled and allowance-waiting states can end a wait; queued/running cannot.
Approval requests are not separate Task states. `wait` collects JavaScript results;
`write_stdin` manages commands. Completion notifications also operate without waits.

## Pause at a model boundary

`POST /api/v1/tasks/{id}/control` accepts `{run_id, action: "pause" | "resume"}`
for the original accepted run. Task info exposes `paused`; the fixed destination,
configuration and run identity are unchanged. In-flight model/tool operations
settle normally, then the local or connected Worker waits before the next model
request. Pending guidance remains accepted while paused. Cancellation wakes the
wait and retains the usual cleanup and uncertainty rules. This control is
separate from releasing an allowance-waiting run.

## Stop or abandon

```text
running --interrupt--> stopping --cleanup confirmed--> interrupted
running --cancel_task--> stopping --cleanup confirmed--> cancelled
interrupted --cancel_task--> cancelled (Archived)
ended Task --create_task--> new Task ID + current machine policy
```

`interrupt` retains never-started guidance as history. It does not replay executing
input, including suspended allowance-waiting input. Ended Tasks reject guidance;
a rerun is a new Task. Control restart reconciles an accepted remote run by its
original input identity, without relaunching it or changing its execution context.
Execution Runtime restart fences original runs. Explicit owner reconciliation
settles known receipts and proven scope cleanup; unknown outcomes block replacement. See [persistence and updates](architecture.md#persistence-and-updates).

Owner recovery uses `POST /api/v1/tasks/{id}/recovery` with the original
`run_id` and `action: "inspect" | "reconcile"`. A Runtime owner can address the
same original destination journal through `POST /api/v1/runtime/recovery` with
`control_id`, `task_id`, `run_id` and the action. These private/native ports are
excluded from browser publication. Inspection returns receipt states and unresolved
reasons without starting a model or tool. Reconciliation consults the
original Control operations, signed destination command receipts. It accepts no client-supplied result or cleanup
acknowledgment. Known results update the original checkpoint once; only proven
cleanup releases held capacity and ends the run as interrupted. A previous-boot
Host run with issued operations and no recorded cleanup proof remains fenced.
Missing/unknown effects are never converted into failure or retried.

`cancel_task` discards pending guidance and abandons the Task. History/files survive;
completed results stay complete. No terminal phase can be reopened.

Both operations reject guidance during cleanup. Running Tasks hold capacity and
leases until shutdown is confirmed; unconfirmed shutdown fails visibly and retains
active leases until original receipts and cleanup are reconciled. App/Web Stop interrupts. CLI `tasks interrupt`
and `tasks cancel` use the respective `/api/v1/tasks/{id}/interrupt` and `/cancel` APIs.

## Archive

| Group | Phases |
|---|---|
| Ongoing | `queued`, `running`, `waiting` |
| Needs attention | `failed`, `interrupted` |
| Archived | `complete`, `cancelled` |

Archive is currently a phase-derived list grouping. It does not delete data or
reclaim storage. Long-inactive unfinished Tasks are not automatically archived.
An independent archive flag and inactivity rules remain deferred.

Schemas and lifecycle code: `pkg/core/product-tools.json`, `internal/tasks` and `internal/control/task_service.go`.
Checks and live qualification limits: [permission contract](permissions.md).

## Execution policy and reruns

New tasks use the selected machine's current execution authorization for subsequent tool operations.
The actual Runtime destination is retained as execution information for the active
run, receipts and file retrieval. It is not reused as a default for future tasks.
Guidance is accepted only while a task remains active. Complete, failed, cancelled
and interrupted tasks require a new task to run again; retained files are not
implicitly imported into that new task. Task workspaces are separate local
directories on the fixed Runtime. Cleanup closes only that Task's processes and
sessions; files remain. No shared guest environment or automatic project copy is
provided.

Task persistence uses schema version 4. Development builds use an independent
data root; unsupported storage formats are rejected without rewriting files.

## Execution transcript

`GET /api/v1/tasks/{id}/events?after=CURSOR&limit=100` reads a bounded,
append-only page from the original Runner run. Each item carries a durable
`sequence` plus its generation/turn-bound Agent event. The page returns `cursor`
and `has_more`; clients advance only through contiguous sequences. Control
persists fetched remote pages, so an unavailable original Runner returns cached
content with `unavailable: true`. A disconnected target never causes execution
or transcript reads to move to a replacement machine. Terminal runs remain
readable. Changes wake the existing `tasks` invalidation stream.
