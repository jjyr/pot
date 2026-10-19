# Machine and command execution authorization

Control owns policy for each Runtime membership in its Space. A trusted server
and a protected personal Mac may coexist. Space membership does not grant host
execution on every machine. Main uses its owning Control; Tasks retain their
fixed Runtime. No denial or disconnect moves work to another destination.

## Machine defaults and command rules

| Machine setting | Behavior |
| --- | --- |
| Protected (`ask`, default) | Ordinary operations run in the SRT sandbox; host exceptions require authorization |
| Trusted (`allow`) | Host execution without SRT, except matching explicit host-deny rules |
| Blocked (`deny`) | No new execution; owned output polling and cleanup remain available |

New machines are Protected, including Controls. Trust is an explicit owner choice.
Machine settings belong to one enrollment; re-enrollment clears old rules/grants.

A permanent command rule matches the full command source, canonical effective
working directory, canonical shell, login/snapshot settings, terminal and
interactive mode, and explicit credential references. It also binds the machine
and enrollment. Output/yield limits are not authority. There are no prefix,
command-family or wildcard rules. `npm test`, `npm install`, `npm test -- --watch`
and `npm test && curl ...` are different commands. A changed directory, shell or
credential binding needs another matching rule.

Matching is authorization to execute code, not proof of its effects: scripts,
executables, Git configuration and dependencies can change under the same text.
An interactive host grant permits subsequent input to that process and must be
explicitly identified as such; a noninteractive rule cannot create a host REPL.

Rule policies are `allow_host`, `default` and `deny_host`. Explicit host denial
wins over machine trust and task grants. It still permits a separately requested
sandboxed operation. `default` and rule deletion restore the machine default;
they do not remove broader machine trust. Machine Blocked overrides all grants.

## Approval decisions

`exec_command.request_unsandboxed: true` requests permission to execute outside
SRT. It requires a nonempty string `justification`, displayed in the approval.
The reason and request flag are not part of a permanent command rule's identity.
Omission/`false` follows machine policy and saved grants; it does not force a
sandbox when the owner already permits host execution. Known host-only access may
be requested directly without first running a doomed sandbox command.

The model requests authority; Control selects execution mode. `execution_mode` is
not an accepted tool input. Results/events report the actual `host`/`sandbox`
boundary; owned process polling retains the original process mode. The model
cannot supply sandbox profiles, upgrade a live session or change destinations.

Main context includes an `execution-environment` snapshot of the Control-owned
destination policy and file/temp/network boundaries. Worker workspace context
contains its prepared cwd; policy remains enforced by Runtime. Neither context
grants authorization. Every operation is checked against current policy.

| Decision | Scope |
| --- | --- |
| `allow_always` | Persist this exact command/context on this machine, then execute |
| `allow_task` | Repeated matching commands in the current work/run only |
| `deny` | Reject the original host request, without creating a permanent deny |

Current work means one foreground processing turn (which may aggregate pending
user requests), one Task run, or one
scheduled occurrence. Grants do not extend to other commands, subsequent user
requests, child/sibling Tasks, scheduled occurrences, machines or generations.
Completion/cancellation, restart, enrollment changes and policy invalidation clear
temporary grants. Background scheduling grants no authority.

Instruction edits always require independent exact `allow_once`/`deny` review,
even on Trusted or Blocked machines; command/task grants never approve them.

Every pending decision binds owner, Task/run/turn, generation, call ID, Runtime,
enrollment, tool and full arguments/context. A later grant cannot reverse a denial
already given to that exact request. Persistent grants are saved before permitting
execution; write failure leaves approval pending.

Approval creation, resolution and expiration publish [Runtime hooks](hooks.md).
They refresh current UI cards and user reminders without messaging the assistant
or making model calls. The UI reads authoritative pending decisions, so delayed
updates cannot restore an expired approval. Waiting-call cancellation also expires
its pending decision. Hooks never perform or authorize approval decisions.

## Sandbox and host boundaries

Protected execution uses the pinned SRT SDK adapter: macOS Seatbelt or Linux
bubblewrap/seccomp. Workspaces are writable; necessary system/toolchain assets are
read-only; unrelated user files and Runtime authority are excluded. Private homes,
temporary files and caches are scoped to the execution. Unix sockets are blocked
apart from necessary narrowly defined system support. Apple Events remain disabled.

Network access is open, including ordinary localhost TCP. No SRT domain-filtering
proxy is injected. Accessible local services must enforce their own authentication.
Sandboxing cannot prevent transmission of files a workload is allowed to read.

Host mode bypasses SRT and uses the Runtime account's OS privileges, including
its socket access. It is not described as file/socket isolated. OS administrative
and privacy permissions remain independent.

Shell initialization, patches and Node plugins must follow the selected mode.
Protected plugin sessions remain sandboxed; command rules do not grant plugin
host execution. Protected plugin host exceptions are not currently exposed.
Trusted machines can run ordinary host plugin sessions. See [executor](executor.md).

Sandbox errors do not trigger automatic host execution or command replay. Native
filesystem denials cannot generally pause for approval. The Agent receives the
failure and is instructed to submit a new, narrowly scoped host request when a
sandbox restriction blocks the user's task, accounting for partial effects first.
An explicit user restriction against host access or approval requests takes precedence.
A sandbox denial is distinct from an owner denial: if the owner refuses the host
request, the Agent must stop that operation without trying another route. These
instructions guide model behavior; Control still enforces every approval.

## Owner management and applying changes

Machines groups authority by Space and machine. Each machine shows its default
execution policy directly, with permanent command rules collapsed behind a count.
Active task grants appear only when present; application failures remain visible.
There is no separate execution-permissions tab in Settings.
Owners can manually create/edit/delete rules, change machine policy, inspect and
remove task grants, and retry a pending policy application. Default-policy edits
require explicit Save or Cancel and explain the execution interruption before
saving. Unsaved choices survive live refresh and appearance/language changes;
navigation asks before discarding them. A newer policy revision disables stale
saves until the owner cancels and reloads the current policy. Cached permissions
are read-only when their owning Space or policy service is unavailable. An offline
execution machine can still be configured through its connected Control. Owner
edits change current authority; deleting a rule is not deleting a history record.

Owner edits persist a new revision, invalidate pending/task grants and close the
machine's old execution Engines. This conservatively stops existing work on that
machine. Processes and persistent plugin sessions do not silently inherit new
permissions or retain old host authority. Work is not automatically restarted.

Control and Runner serialize revision checks with actual process start. Runner
receives authenticated revisions, rejects stale requests and does not admit host
work from a disconnected cache. Offline/failed application is visible as pending
and fences new admission. A stopped process does not undo prior file/network
side effects; uncertain cleanup retains existing receipt/capacity fences.

An application interrupted by Runtime restart, or a missing cleanup receipt,
requires owner recovery. Retry cannot manufacture proof after old Engines have
disappeared. Reconcile the original execution scopes and deliberately replace the
affected enrollment/root; preserve its old state for inspection. A Runner with
unreconciled prior runs also refuses policy acknowledgment after restart.

## Storage and API

Permission format 6 stores machines, enrollment epochs, exact command rules and
durable policy-application fences. Restart retains pending/uncertain application.
Temporary grants and pending decisions are not restored after restart. Unsupported
old formats fail without rewriting the file or inferring new grants. Runner
journal schema 7 retains execution mode/context-bearing requests and separates
original Run receipts from same-Task continuations.

`GET /api/v1/permissions` returns `policy` (machines, rules, task_grants,
application states) and conversation-owner-scoped pending approvals.
`POST /api/v1/permissions` accepts:

- `{id, action: "allow_always" | "allow_task" | "deny"}` for command approval.
- `{action:"configure", machine_id, machine_policy:{execution_access, revision}}`.
- `{action:"save_rule", rule, old_id, expected_revision}`; identity/enrollment and
  canonical destination context are resolved by the owner service.
- `{action:"delete_rule", machine_id, id, expected_revision}`.
- `{action:"remove_task_grant", id}`.
- `{action:"retry", machine_id, expected_revision}`.

Edits carry the observed policy revision and reject stale drafts. Command rules
record updater/time; credential values never enter rules or durable receipts.

Sources: `internal/control/permissions.go`, `permission_application.go` and
`dispatch.go`. Protocol/lifetime rules remain in [Task coordination](task-coordination.md).
