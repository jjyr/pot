# Task coordination and lifecycle

Assistant communication is separate from Task reference messages; see
[Assistant messages](assistant-messages.md). Handoff is agent coordination, not a
Task migration operation.

Main manages Tasks; Workers cannot create, message, wait for, interrupt or cancel
other Tasks. Summary sessions only inspect Tasks.

## Guidance, messages and waiting

| Tool | Purpose |
|---|---|
| `guide_task` | Queue guidance for an active Task |
| `send_message` | Deliver reference information at a model boundary without starting or resuming work |
| `wait_task` | Bounded wait for any selected Task to complete or need attention; cursors suppress unchanged results |

Messages retain trusted sender identity, are consumed once and remain pending for
ended Tasks as retained history. They do not grant owner authority. Delivery
uses pot-agent.

`wait_task` returns Task metadata, not transcripts. Complete, failed, interrupted,
cancelled and allowance-waiting states can end a wait; queued/running cannot.
Approval requests are not separate Task states. `wait` collects JavaScript results;
`write_stdin` manages commands. Completion notifications also operate without waits.

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
Execution Runtime restart settles known receipts and scopes; unknown outcomes
block replacement. See [failure rules](architecture.md#failure-and-receipt-rules).

`cancel_task` discards pending guidance and abandons the Task. History/files survive;
completed results stay complete. No terminal phase can be reopened.

Both operations reject guidance during cleanup. Running Tasks hold capacity and
leases until shutdown is confirmed; unconfirmed shutdown fails visibly and retains
active leases until Runtime restart. App/Web Stop interrupts. CLI `tasks interrupt`
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

Schemas and lifecycle code: `crates/core/src/pot-tools.json`, `crates/control/src/control/tasks.rs`.
Checks and live qualification limits: [permission contract](permissions.md).

## Execution policy and reruns

New tasks use the selected machine's current execution policy when dispatched.
The actual Host/Sandbox context is retained as execution information for the active
run, receipts and file retrieval. It is not reused as a default for future tasks.
Guidance is accepted only while a task remains active. Complete, failed, cancelled
and interrupted tasks require a new task to run again; retained files are not
implicitly imported into that new task. Sandbox executions reuse the machine-owned shared development environment.
Execution cleanup closes only that Task's processes and sessions; the environment,
installed tools and files remain. A new Task receives a new workspace directory
but observes existing shared software and cache state.

Task persistence uses schema version 4. Earlier per-task-container records are rejected without implicit migration; use an independent
data directory for development builds of this schema. Existing files are not deleted.
