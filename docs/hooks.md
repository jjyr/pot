# Runtime lifecycle hooks

`internal/hooks` defines trusted lifecycle events and their delivery routes.
Hooks reference authoritative state; they carry no new user authority and are
not exposed as model tools. `send_message` remains the Agent messaging primitive.

```text
PermissionStore --> approval hook --> UI invalidation --> cards + user reminder

Task transaction --> terminal hook --> shared Agent inbox --> assistant report
                              |
                              +-----> UI invalidation --> current Task status
```

| Event | UI topics | Agent inbox |
| --- | --- | --- |
| `approval_requested` | `permissions`, `tasks` | No |
| `approval_resolved` | `permissions`, `tasks` | No |
| `approval_expired` | `permissions`, `tasks` | No |
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

## Task outcomes

The Task state-change transaction detects the first active-to-terminal transition
and atomically enqueues its hook as a Runtime result envelope in `agent_messages`.
The ID binds the Task and original run. This covers all terminal outcomes,
including cancellation or interruption before dispatch. Failure reports preserve
the actual execution error alongside available text; an empty result still has
a concrete terminal status. Hook insertion failure rolls back the state change.

After commit, UI invalidations wake the current projections. The existing assistant
receiver claims the durable result when user chat is available and reports its
title and outcome. Reply acknowledgment, explicit-query deduplication, user-input
priority and uncertainty fences follow [Agent messages](assistant-messages.md).
Acknowledged results cannot be requeued by repeated reconciliation. Archiving an
already interrupted run does not create a second execution outcome.

Hooks add no queue beyond the existing user inbox and shared Agent inbox. Task
outcomes use the bounded, cross-Task summary policy in
[Agent messages](assistant-messages.md#category-specific-batches). Approval hooks
remain outside Agent batching.
