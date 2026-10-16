# Agent tools and composition

`internal/agent` keeps tools focused on individual operations. `exec` combines calls
without merging their interfaces. Model-visible interface changes require explicit
user agreement; see the repository `AGENTS.md`.

## Tool roles

| Capability | Tools | Roles |
|---|---|---|
| Execution | `exec_command`, `write_stdin`, `apply_patch` | Main, Worker |
| Computer use | `cua_repl` | Main, Worker; Host or Linux Sandbox |
| Composition | `exec`, `wait` | Main, Worker |
| Tasks | `create_task`, `list_tasks`, `get_task`, `guide_task`, `interrupt`, `cancel_task`, `send_message`, `wait_task` | Main; Summary only list/get |
| Assistant messages | `list_assistants`, `send_assistant_message` | Main |
| Schedules | `list_schedules`, `get_schedule`, `manage_schedule` | Main |
| Assistant rules | `read_agent_instructions`, `update_agent_instructions` (always reviewed) | Main |
| Execution targets | `list_execution_targets` | Main |

`list_execution_targets` discovers authorized Host/Sandbox destinations without
starting them. Execution tools use `execution_target` with `"host"`, `"sandbox:ID"`
or a typed Runtime + Host/Sandbox reference. `create_task` and
`manage_schedule.spec` use `machine_id`; machine policy resolves the execution mode at dispatch. These are addresses,
not independently managed resources. Runtime owns automatic Sandbox lifecycle;
`manage_sandbox` is absent from the model tool catalog.

Profile filters can narrow visibility. Target defaults and approval rules are in
[Agent permissions](permissions.md).
There are no separate file/list tools: inspect with commands and edit with patches
or approved commands. Task semantics are in [Task coordination](task-coordination.md).

## JavaScript composition

`exec({code, yield_time_ms?})` runs goja in an isolated Go worker process with top-level `await`, ordinary
JavaScript processing, `tools.NAME(args)` and `text(value)`. Only `text()` output
enters model history; child operations remain visible in events and approval cards.

```javascript
const results = await Promise.all([
  tools.exec_command({cmd: "git status --short", execution_target: "sandbox:project"}),
  tools.exec_command({cmd: "git diff --stat", execution_target: "sandbox:project"})
]);
text(results.map(r => ({stdout: r.stdout, exit_code: r.exit_code})));
```

Await dependent calls sequentially. Inspect tool results for failures; transport
and approval errors reject their Promises. Product tools retain their MCP wrappers.
Unawaited calls may have effects, but do not survive script completion.

Results contain `output` and `completed`, plus `exec_id` while running.
`wait({exec_id, yield_time_ms?})` returns new output or completion without repeating
previous output. `wait_task` observes Tasks; `write_stdin` manages shell sessions.

Runs belong to the current accepted turn and continue while the Agent awaits other
work. Await completion before finishing the turn. Ending the turn stops pending
calls; completed effects remain. Restart does not restore JavaScript memory or
replay uncertain calls. Existing receipts prevent duplicate execution.

## Boundaries

The isolate has no direct filesystem, network, Node or process APIs. Its tools are
limited to the current role/profile, with recursive `exec`/`wait` excluded. Each
child call uses the ordinary dispatcher and checks the assistant’s current machine capability. Composition
cannot broaden authority. Code, output, concurrency and execution time are bounded; limits and schemas live
in `internal/composition`. Worker memory uses a 64 MiB Go runtime soft limit plus
a sampled heap watchdog; this is not a hard per-isolate allocation cap. Crossing
the watchdog threshold terminates the worker and retains ordinary child receipts.

Saved explicit `builtin_tools` lists must include `exec`/`wait` to enable composition.
Expanded schemas count toward context limits. Validation and qualification limits:
[permission contract](permissions.md).

Operator management endpoints remain separate from model tools. They require
Control membership and an existing resource. Lost operations are inspected by
original identity and never resent; unresolved receipts block new effects.
