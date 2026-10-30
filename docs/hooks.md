# Runtime lifecycle hooks

`internal/hooks` defines trusted lifecycle events and their delivery routes.
Hooks carry committed structured business facts; they carry no new user authority and are
not exposed as model tools. `send_message` remains the Agent messaging primitive.

```text
PermissionStore --> approval hook --> UI invalidation --> cards + user reminder

Ordinary / scheduled result --> prepare --> terminal transaction
                                               |  final Task + outcome hook
                                               |  schedule history + policy
                                               +--> shared Agent inbox --> assistant report
                                               +--> UI invalidation --> current Task status
```

| Event | UI topics | Agent inbox |
| --- | --- | --- |
| `approval_requested` | `permissions`, `tasks` | No |
| `approval_resolved` | `permissions`, `tasks` | No |
| `approval_expired` | `permissions`, `tasks` | No |
| `task_started` | `tasks` | No |
| `task_completed` | `tasks`, `assistant-mail` | Owning assistant |
| `task_failed` | `tasks`, `assistant-mail` | Owning assistant |
| `task_interrupted` | `tasks`, `assistant-mail` | Owning assistant |
| `task_cancelled` | `tasks`, `assistant-mail` | Owning assistant |

## Approval lifecycle

PermissionStore publishes hooks after changing its pending decision state.
Events identify the approval, owner, Task if any, original turn/run and generation.
Creating a pending decision emits requested once. Owner decisions emit resolved,
including other pending requests settled by the same exact-command grant.
Cancellation, run expiration and machine policy invalidation emit expired for
unresolved requests. Repeated decisions and cleanup do not repeat these events.

The receiver invalidates existing UI projections. Cards, assistant approval counts
and user reminders read current owner-scoped pending decisions. A delayed event
therefore cannot restore an obsolete approval. Pending decisions are transient
and are not restored at restart; lifecycle hooks create no durable approval queue.
No approval hook starts a model request or writes an Agent inbox entry.

Owner decisions continue through the permission API, which checks exact request
identity and saves permanent grants before permitting the original operation.
See [Permissions](permissions.md) and [UI events](ui-events.md).

## Task execution start

`task_started` is published once when the local task service first observes the original run's
nonzero execution start time. Enqueueing or accepting a queued launch does not
emit it. The start time commits before publication and prevents repeats across
reconciliation and restart. This hook only invalidates Task UI; it never creates
an assistant summary or inbox entry.

## Task outcomes

Task coordination prepares the outcome before its first active-to-terminal
transition. Control collects optional `.pot-artifacts/` deliverables for both
ordinary and scheduled Tasks; scheduled results additionally apply the saved
occurrence policy. File collection runs outside the transaction. The original
Task/run, generation and record revision fence the prepared result at commit.
Collection errors are attachment metadata and retain the original final reply,
execution status and diagnostics.

The Task state-change transaction detects the first active-to-terminal transition
and atomically enqueues its hook as a Runtime result envelope in `agent_messages`.
The ID binds the Task and original run. This covers all terminal outcomes,
including cancellation or interruption before dispatch. Failure reports preserve
the actual execution error alongside available text; an empty result still has
a concrete terminal status. Hook insertion failure rolls back the state change,
schedule history. No mandatory result report or scheduler format validation can
turn completed execution into failure.

Task records carry Control-assigned `manual` or `schedule` origins. Scheduled
origins bind the schedule and occurrence explicitly; request ID prefixes have no
routing meaning. Result envelopes preserve the final reply, execution error, artifact
paths, collection diagnostics and notification suppression reason. Silent schedule outcomes enter the
same inbox as `suppressed`, retain their history and do not wake a model.

After commit, the shared typed lifecycle feed publishes the full fact independently
of UI invalidations. The authenticated owner lifecycle WebSocket subscription carries it to
notification conversion; notification consumers never infer these events from
version hints or resource snapshots. UI invalidations still wake current projections. The existing assistant
receiver claims the durable result when user chat is available. Eligible failed
manual Tasks first allow model-directed recovery; accepted recovery is
silent. The assistant replies only with a useful result, an unresolved blocker or
a needed user decision; routine updates can finish with empty text. Failed outcome
transitions do not send an immediate system notification ahead of recovery;
unresolved failures notify through the assistant's reply. Reply acknowledgment, explicit-query deduplication, user-input
priority and uncertainty fences follow [Agent messages](assistant-messages.md).
Acknowledged results cannot be requeued by repeated reconciliation. Archiving an
already interrupted run does not create a second execution outcome.

Hooks add no queue beyond the existing user inbox and shared Agent inbox. Task
outcomes use the bounded, cross-Task summary policy in
[Agent messages](assistant-messages.md#category-specific-batches). Approval hooks
remain outside Agent batching.

## Schedule before-run scripts

`spec.before_run` is a user-configured script executed by the schedule trigger
pipeline before Task/session creation. It is **not** one of the trusted lifecycle
events above, a model tool, or an additional event-delivery system. Configure it
through `manage_schedule` or the schedule editor. Its strictly parsed stdout
selects only `run` or `skip`, with an optional reason. Valid skip creates only
schedule history; it publishes no Task outcome hook or assistant inbox entry.
Known errors default to normal Task admission only after confirmed cleanup;
unknown execution/cleanup stays blocked under the existing operation receipt
fence. See [before-run probes](scheduled-tasks.md#before-run-probes) for the
copyable script, protocol, directory/policy rules, manual triggers and recovery.

## Completed replies and independent consumers

The first eligible completed reply/outbox insert publishes only after its
transaction commits; rollback and repeated completion do not publish. Silent
reports use the existing non-notifying save path. Linked user replies publish
from their incoming reply transaction. Caller-owned remote Task execution already
commits terminal state through the same Task hooks. Remote executor human approval
requests carry IDs and the original delegating address through Agent Link; they
never send tool arguments or broadcast another owner's private events.

`hooks.Feed` has independent bounded subscriptions and no historical replay.
Cross-process consumers use the authenticated `/api/v1/lifecycle` typed stream,
with current access epochs and no business-state baseline reads. The feed is not
a durable delivery broker. Notification persistence, per-device uncertainty,
foreground suppression and consumer lifetime are described in
[System notifications](shared-ui.md#system-notifications).
