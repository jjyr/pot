# Agent tools and composition

`pkg/agent` keeps tools focused on individual operations. `exec` combines calls
without merging their interfaces. Model-visible interface changes require explicit
user agreement; see the repository `AGENTS.md`.

## Tool roles

| Capability | Tools | Roles |
|---|---|---|
| Execution | `exec_command`, `write_stdin`, `apply_patch` | Main, Worker |
| Computer use | `cua_repl` | Main, Worker; fixed Runtime |
| Composition | `exec`, `wait` | Main, Worker |
| Tasks | `create_task`, `list_tasks`, `get_task`, `interrupt`, `cancel_task` | Main |
| Agent messages | `send_message`; `list_assistants` discovers assistants | Main; Worker may message its owner |
| Schedules | `list_schedules`, `get_schedule`, `manage_schedule` | Main |
| Assistant rules | `read_agent_instructions`, `update_agent_instructions` (always reviewed) | Main |
| Assistant profile | `update_agent_profile` | Main, including result handling |
| Execution targets | `list_execution_targets` | Main |

`list_execution_targets` lists the local execution target. Local `create_task`
and `manage_schedule.spec` accept that `machine_id`; omission uses the current
node. Linked task delegation uses `link_id` and an optional assistant `target`. Execution tools remain fixed to their run and accept no destination
selector. Target authorization is defined in [Permissions](permissions.md).

Commands request external-service credentials through the `credentials`
environment-variable mapping and explicitly reread them with `refresh_credentials`.
Plugins declare bindings in their manifest. See [Credentials](credentials.md).
There are no separate file/list tools: inspect with commands and edit with patches
or approved commands. Task semantics are in [Task coordination](task-coordination.md).

## Assistant display profile

`update_agent_profile` replaces the former name-only tool. Its optional `name`
and `avatar` fields update one identity atomically; omitted fields stay unchanged
and an empty update fails. It does not change model configuration or agent rules.
Workers and peer conversations cannot update the profile. Main retains its
configured tools when processing task results and plugin notifications, including
profile updates that complete an existing user request.

```javascript
await tools.update_agent_profile({name: "Research"});
await tools.update_agent_profile({avatar: {path: "avatar.png"}});
await tools.update_agent_profile({name: "Research", avatar: {task_id: "TASK_ID", path: "images/avatar.png"}});
await tools.update_agent_profile({avatar: {path: ""}}); // restore default
```

Avatar paths are relative to the assistant's own workspace, or to an owned Task's
original workspace when `task_id` is supplied. Task imports reuse the authenticated
original local task file channel, including completed Tasks; cross-owner
Tasks and paths escaping the selected workspace fail. The tool imports existing
files rather than generating images, fetching URLs or accepting model-encoded
image bytes. Source images must be PNG/JPEG/WebP, at most 8 MiB, 8192 pixels per
dimension and 32 megapixels. Control center-crops and resizes them to a 128×128
PNG, persisted with the assistant registry independently of source-file lifetime.
An empty path without a Task restores the default Companion image.

The owning Control publishes the normalized image in authenticated assistant
directory responses, so browser and native views use the same bytes.
Profile tools return only identity, revision and `has_avatar`, keeping image bytes
out of model context. The update receipt binds the source arguments and is checked
before reading a file again; retries return the original result without overwriting
later edits, even after the original source was removed. Successful changes notify
the existing `assistants` topic, refreshing the directory and chat title.

## JavaScript composition

`exec({code, yield_time_ms?})` runs goja in an isolated Go worker process with top-level `await`, ordinary
JavaScript processing, `tools.NAME(args)`, `text(value)` and `yield_control()`. Only `text()` output
enters model history; child operations remain visible in events and approval cards.

```javascript
const results = await Promise.all([
  tools.exec_command({cmd: "git status --short"}),
  tools.exec_command({cmd: "git diff --stat"})
]);
text(results.map(r => ({stdout: r.stdout, exit_code: r.exit_code})));
```

Await dependent calls sequentially. Inspect tool results for failures; transport
and approval errors reject their Promises. Product tools retain their MCP wrappers.
Unawaited calls may have effects, but do not survive script completion.

`text()` accumulates output without returning control. Short scripts return their
complete result immediately when they finish, including execution cleanup.
`yield_control()` explicitly returns accumulated output while the script keeps
running. Otherwise `exec` waits up to 30 seconds by default before returning a
running ID; `wait` defaults to 10 seconds. Both accept `yield_time_ms` (1–60000).
Output arriving during either wait does not itself cause an early return.

Up to four runs may be active at once; there is no cumulative per-turn run quota.
At most 64 uncollected runs/results are retained. Collect finished results with
`wait` to release retained state. Returning a completed result consumes its ID;
later waits for that ID fail rather than replaying the result.

Results contain `output` and `completed`, plus `exec_id` while running.
`wait({exec_id, yield_time_ms?})` returns new output or completion without repeating
previous output. Call it only after a running result supplies `exec_id`.
`write_stdin` manages shell sessions. Task completion is delivered
through Runtime inbox events; automatic summaries expose no composition tools.
Eligible failures may use the separate batch-scoped inspection and retry tools
documented in [Task coordination](task-coordination.md#automatic-failure-recovery).

Runs belong to the current accepted turn and continue while the Agent awaits other
work. Await completion before finishing the turn. Ending the turn stops pending
calls; completed effects remain. Restart does not restore JavaScript memory or
replay uncertain calls. Existing receipts prevent duplicate execution.

## Boundaries

The isolate has no direct filesystem, network, Node or process APIs. Its tools are
limited to the current role/profile, with recursive `exec`/`wait` excluded. Each
child call uses the ordinary dispatcher and checks the current machine policy, exact command grant and selected execution mode. Composition
cannot broaden authority. Code, output, concurrency and execution time are bounded; limits and schemas live
in `pkg/composition`. Worker memory uses a 64 MiB Go runtime soft limit plus
a sampled heap watchdog; this is not a hard per-isolate allocation cap. Crossing
the watchdog threshold terminates the worker and retains ordinary child receipts.

Saved explicit `builtin_tools` lists must include `exec`/`wait` to enable composition.
Expanded schemas count toward context limits. Validation and qualification limits:
[permission contract](permissions.md).

Tool schemas use omitted optional fields to preserve defaults or request no
operation. Responses function definitions explicitly set `strict: false` so the
provider does not normalize those fields into required arguments. Direct and
composed calls retain the same argument contract.

Operator management endpoints remain separate from model tools. They require
local operator authentication and an existing resource. Lost operations are inspected by
original identity and never resent; unresolved receipts block new effects.

## Optional tool parameters

The Responses encoder explicitly sends `strict: false` for function tools so
optional properties remain optional. Omission is meaningful for execution tools
(for example, creating a REPL without a session ID); the provider must not force
placeholder values into every property. Local schema and authorization checks
still validate all supplied arguments.
