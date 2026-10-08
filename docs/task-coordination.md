# Task coordination, interruption and archive

Updated: 2026-10-08. Product tools are separate operations. `send_message` and
`wait_task` are initially Main-only; Workers cannot send to or wait for other
Tasks, create Tasks, interrupt or cancel them. Summary sessions only inspect Tasks.

## Messaging and waiting

`send_message({task_id, text})` sends durable reference information from Main to a
pot-agent Task. Running Tasks consume it at a safe model boundary. Idle, completed
or interrupted Tasks retain it for their next explicitly guided turn. Sending a
message does not enqueue work, reopen a Task, resume execution or grant authority.
Use `guide_task` to request new work or explicitly continue an interrupted Task.

The sender is assigned by Runtime, not supplied by the caller. Messages have a
reference-data envelope and cannot authorize permissions, preference changes or
other owner-only operations. Consumption is acknowledged once and recorded in the
Task transcript. Duplicate request receipts return the original acceptance;
conflicting contents fail. Mailboxes allow 100 pending and 1,000 retained entries,
trimming consumed entries first. Delivery to Codex bindings is explicitly rejected;
Codex coordination integration is deferred.

`wait_task({task_ids, after?, timeout_ms?})` waits for any of 1–8 distinct Tasks to
complete, fail, become interrupted, become cancelled or wait for token
allowance. `timeout_ms` defaults to 10,000 and accepts 0–60,000; zero returns an
immediate snapshot. Results contain `tasks` (metadata, `cursor`, `ready`), a
`cursors` map and `timed_out`. Pass that map as `after` on the next call to suppress
unchanged settled results. Cursors describe Task metadata, not transcript positions.
Queued/running Tasks do not end the wait. Permission requests are not a separate
Task phase and do not independently wake this API. Caller interruption or Runtime
shutdown aborts the wait. No transcript is retrieved and no Task is resumed.

`wait` collects JavaScript `exec` output; `write_stdin` manages command sessions;
`wait_task` observes background Task lifecycle. Completion notifications continue
to operate without a wait call. Expanded tool schemas count toward context estimates;
small profile windows or large output reservations may require adjusting profile
limits before the first model request.

## Interruption and explicit resume

`interrupt({task_id})` pauses work while preserving pending guidance. Native App
and Web stop actions, and `pot tasks interrupt ID`, use
`POST /api/v1/tasks/{id}/interrupt`.

`cancel_task({task_id})` abandons work: stop execution, discard pending guidance,
and mark `cancelled` after confirmed cleanup. It is Main-only. The API is
`POST /api/v1/tasks/{id}/cancel`; `pot tasks cancel ID` calls that endpoint.
Interrupted Tasks can be cancelled even when they have no pending guidance.
History and files survive. Completed results remain complete. A cancelled Task
requires explicit new guidance to reopen; discarded guidance is never replayed.
Both actions reject new guidance while shutdown cleanup is underway.

```text
running --interrupt--> running + stopping flag --cleanup confirmed--> interrupted
                                                                          |
                                                       explicit guide_task|
                                                                          v
                                                                  queued --> running

queued / allowance-waiting --interrupt + cleanup--> interrupted

interrupted --cancel_task--> cancelled (Archived; guidance discarded)
running --cancel_task--> stopping --cleanup confirmed--> cancelled
```

A running Task retains its scheduling capacity and resource leases until shutdown
is confirmed. Unconfirmed cleanup marks it failed and retains the active leases
until Runtime restart. Guidance during cleanup is rejected. An interrupted Task
is not scheduled automatically, including after restart. Explicit `guide_task`
resumes its retained pending guidance in FIFO order, followed by the new guidance.

Pending, never-started guidance is preserved. The interrupted executing input is
not replayed: it may already have produced effects. For an allowance-waiting Task,
the suspended executing input is removed, while subsequent guidance survives.
Task context, transcript and files survive; managed live resources are stopped.
Interrupting a completed idle Task closes retained resources without replacing its
completed result or reopening it.

## Current archive rules

Archive is a Task-list grouping derived from execution phase. There is no separate
archived flag, manual archive tool, inactivity timer, deletion or storage cleanup.

| Group | Phases |
|---|---|
| Ongoing (`active`) | `queued`, `running`, `waiting` |
| Needs attention (`attention`) | `failed`, `interrupted` |
| Archived (`archived`) | `complete`, `cancelled` |

Long-inactive unfinished Tasks remain in their current group. Guiding a completed, cancelled
or interrupted Task makes it queued again. Reference messages do not change its
group. Archive does not reclaim storage or add history to the default model context.

If inactivity archiving is later adopted, an independent archived flag would allow
preserving execution state. A conservative candidate is dormant failed/interrupted
Tasks only; queued/running/allowance-waiting Tasks should remain visible as pending
work. Eligibility, age threshold and reopening behavior require product agreement
before implementation. This is a proposal, not current behavior.

## Verification

Runtime fixtures cover Main/Worker/Summary visibility, pending guidance retention,
explicit resume, restart, suspended input removal, unconfirmed cleanup capacity,
message receipts and boundary acknowledgement, cursor suppression, bounded wait
validation and caller interruption. Live model/UI qualification is separate.
