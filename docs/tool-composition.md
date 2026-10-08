# Agent tools and JavaScript composition

Updated: 2026-10-08. This contract applies to pot-agent. Native Codex composition
and permissions integration remain deferred.

## Tool interfaces

Changes to model-visible tools require explicit user agreement before implementation:
names, additions/removals, arguments and behavior. Existing tools keep their separate
responsibilities. Composition combines calls without merging the underlying tools.

| Capability | Tools | Roles |
|---|---|---|
| Environment execution | `exec_command`, `write_stdin`, `apply_patch` | Main, Worker |
| Composition | `exec`, `wait` | Main, Worker |
| Tasks | `create_task`, `list_tasks`, `get_task`, `guide_task`, `interrupt`, `cancel_task`, `send_message`, `wait_task` | Main; Summary only list/get |
| Schedules | `list_schedules`, `get_schedule`, `manage_schedule` | Main |
| Execution preferences | `get_execution_preferences`, `update_execution_preferences` | Main |
| Sandbox management | `list_sandboxes`, `manage_sandbox` | Main |
| Reviewed mappings | `set_sandbox_mappings` | Main |

The twelve product tools previously used a `pot_` prefix; their arguments and
responsibilities were preserved in the rename. `interrupt` preserves pending guidance;
`cancel_task` discards it when a Task is abandoned. Model-visible registrations and the product MCP
interface now use the unprefixed names. Old names are not aliases. Update external
MCP consumers, explicit profile `allowed_tools` lists and custom prompts that refer
to them. Persisted receipts/history are not renamed or replayed.

There are no `read_file`, `write_file` or `list_directory` tools. Use
`exec_command` to inspect files/directories, and `apply_patch` or approved commands
to write. Legacy path grants cannot authorize these commands or patches.

Main defaults to 21 tools, Worker to 5, Summary to 2. Profile `builtin_tools`
controls execution and composition tools; its new default includes `exec` and
`wait`. Existing profiles with an explicit saved list retain that list and must
include these names to enable composition. `allowed_tools` further narrows the
loadout. `tools` inside JavaScript exposes exactly the current role/profile tools,
excluding `exec` and `wait`; recursion is unavailable. Task messaging, interruption
and lifecycle waiting are described in [Task coordination](task-coordination.md).

## Composition contract

`exec` takes `{code, yield_time_ms?}`. Code supports top-level `await`, standard
JavaScript data processing and `Promise.all` for independent calls. It receives:

- `tools.NAME(args)`: a Promise containing the ordinary tool's JSON result.
- `text(value)`: publish a string or JSON-serialized value to the model.

```javascript
const results = await Promise.all([
  tools.exec_command({cmd: "git status --short", environment: "sandbox:project"}),
  tools.exec_command({cmd: "git diff --stat", environment: "sandbox:project"})
]);
text(results.map(result => ({stdout: result.stdout, stderr: result.stderr, exit_code: result.exit_code})));
```

Concurrency is supported at the tool bridge. Individual execution backends may
serialize operations sharing process or environment state. Calls with dependencies
or shared mutations should be awaited sequentially.

Runtime/transport/approval errors reject the Promise. A tool may also return a
normal JSON object reporting failure (`isError`, nonzero `exit_code`, etc.); inspect
that result as with direct calls. Product MCP tools retain their normal MCP content
wrapper; parse text content when the underlying product data is needed.

Subcall results remain inside the isolate. Only explicit `text()` output is added
to model history; tool events and owner approval cards still show concrete child
operations. This avoids sending full intermediate results back to the model. It
does not guarantee a fixed token saving; compare usage for equivalent tasks.

## Waiting and lifetime

Both tools return `{output, completed}` plus `exec_id` while running, and
`{isError: true, error}` on failure. Output contains newline-separated publications.
`yield_time_ms` defaults to 1,000 and accepts 1–60,000 ms. Explicit output may be
returned before completion. Call `wait` with `{exec_id, yield_time_ms?}` to collect
new output or completion. Already delivered output is not repeated. A completed
run can be queried again with its previously returned ID, yielding empty output.
Unknown IDs, other-turn IDs and malformed arguments fail.

Running jobs continue being driven while the Agent awaits model responses and
ordinary tools. Runs are scoped to the current accepted turn, not background Tasks.
Await completion before the final answer. Ending/cancelling a turn drops pending
child futures and interrupts the JavaScript worker; completed effects are not
rolled back. Unawaited calls may already have effects and are not kept alive after
script completion. Managed process cleanup follows the existing execution backend
contract. Use `write_stdin` for shell sessions and normal task notifications for
background Tasks; `wait` does not wait for Task lifecycle events.

The Agent persists the parent call as started before launching a run. Subcalls get
unique parent-derived IDs and use existing Runtime/executor receipts. A repeated
parent call returns its saved result. Restart recovery marks interrupted parent
calls as uncertain and does not replay scripts or resume JavaScript memory. This
prevents automatic duplicate effects; inspection is required after interruption.

## Authority and limits

`pot-agent` owns the QuickJS runtime and composition manager. JavaScript has no
Node, filesystem, network, process, module-loader or timer bindings. All external
effects go through the same dispatcher as direct calls:

```text
exec code -> tools.NAME(args)
                  |
             role/profile check
                  |
             resolve environment
                  |
             permission approval
                  |
             injected executor / product MCP
                  |
             JSON result -> JavaScript -> text(selected output) -> model
```

Approvals apply to each concrete child operation, not the script. Host defaults
to ask, Sandbox to allow; mappings still require their explicit approved scope.
A permanent approval stores the actual child operation. Scripts cannot extend
Worker role/assignment boundaries or enable disabled tools.

Limits per turn/run: four runs per turn, 128 calls per run, eight simultaneous
calls per run, 64 KiB code, 4 MiB serialized arguments per child call, 64 MiB JS
memory, 512 KiB JS stack, 64 KiB total published output, two seconds uninterrupted
JavaScript execution and ten minutes wall time (including approval waits).
Cancellation also interrupts synchronous JavaScript. Outputs and result decoding
remain bounded by memory and existing tool limits.

## Sources and verification

Schemas/manager/isolate: `agent/src/orchestration.rs`. Shared dispatch and model
loadouts: `agent/src/lib.rs`. Execution schemas: `core/src/environment_tools.rs`.
Product schemas: `core/src/pot-tools.json`. See [permissions](permissions.md).

Backend fixtures cover parallel calls, private intermediate results, incremental
wait output, unknown handles, role/profile restrictions, owner approval/denial,
removed tool rejection, resource limits and synchronous-loop interruption. Use
`mise run test` with an isolated temporary `POT_AGENT_HOME`. Guest permission tests
remain under `mise run test:permissions`; live VM/UI acceptance is separate.
