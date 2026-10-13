# Task coordination and lifecycle

Main manages Tasks; Workers cannot create, message, wait for, interrupt or cancel
other Tasks. Summary sessions only inspect Tasks.

## Guidance, messages and waiting

| Tool | Purpose |
|---|---|
| `guide_task` | Queue work after the current turn, or explicitly resume a stopped Task |
| `send_message` | Deliver reference information at a model boundary without starting or resuming work |
| `wait_task` | Bounded wait for any selected Task to complete or need attention; cursors suppress unchanged results |

Messages retain trusted sender identity, are consumed once and remain pending for
idle Tasks until explicitly continued. They do not grant owner authority. Delivery
uses pot-agent.

`wait_task` returns Task metadata, not transcripts. Complete, failed, interrupted,
cancelled and allowance-waiting states can end a wait; queued/running cannot.
Approval requests are not separate Task states. `wait` collects JavaScript results;
`write_stdin` manages commands. Completion notifications also operate without waits.

## Stop or abandon

```text
running --interrupt--> stopping --cleanup confirmed--> interrupted
                                                          |
                                                      guide_task
                                                          v
                                                       queued --> running

interrupted --cancel_task--> cancelled (Archived)
running ----cancel_task----> stopping --cleanup confirmed--> cancelled
```

`interrupt` preserves never-started guidance. Explicit `guide_task` resumes that
queue in order, followed by the new guidance. Interrupted executing input is never
replayed, including suspended allowance-waiting input. Restart does not auto-resume
an interrupted Task. Control restart reconciles an accepted remote run using its
original input identity. It does not relaunch the Agent or change its execution target.
Execution Runtime restart interrupts after known receipts and scopes are settled;
unknown outcomes block replacement. See [failure rules](architecture.md#failure-and-receipt-rules).

`cancel_task` discards pending guidance and abandons the Task, including an
interrupted Task with an empty queue. History/files survive; completed results stay
complete. Explicit new guidance can reopen a cancelled Task without replaying the
discarded queue.

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
