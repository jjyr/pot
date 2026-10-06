# Host pot-agent and Sandbox execution acceptance

Date: 2026-10-06. Supersedes the deployment conclusions of the earlier Pi/Codex acceptance; previous observations remain historical.

## Implementation

Rebased onto upstream `48e3358` (native Agent, token evaluation and CI), preserving execution preferences from `74df9ba`. Product engines are Pot and Codex. Pot runs in-process in Host Server; Codex runs Host App Server. Pi/Grok product adapters and distributions are removed. Optional Pi comparison/configuration code remains evaluation tooling, not a product integration.

Core defines the injected ToolExecutor contract and shared exec_command/write_stdin/apply_patch schemas and validation. Runtime controls roles and Sandbox routing; the guest Rust executor implements patch and pipe sessions behind the authenticated root broker. Product code has no implicit local execution backend. Eval and isolated unit/model tests explicitly inject LocalTools.

Main can manage Sandbox inventory/default selection and choose execution targets. Workers receive one persisted Sandbox, cannot manage/delegate/retarget, and use its task directory. Saved schedules retain their environment across default changes and omitted-target updates. Invalid persisted assignments fail visibly rather than receiving a replacement. Guest output can only produce bounded stdout/stderr events; it cannot forge Agent lifecycle events, and output IDs are remapped to the model invocation.

Concurrent calls durably initialize one scope before dispatch. Sessions remain bound to owner, turn, Sandbox and VM generation. Active calls include preparation in the cleanup barrier. Cancellation/turn completion drains root-owned cgroups, including setsid descendants. Failed cleanup leaves an unknown-operation record and blocks execution/stop until explicit recovery.

## Verification

- `mise run check`: formatting/script checks, workspace Rust compilation and Swift Helper compilation passed.
- `mise run test`: **193 passed**, one existing live-provider test ignored. Coverage includes model protocols/context/compaction/accounting, preference routing, missing-backend rejection, worker tool restrictions, stale/foreign sessions, generation changes, serialized scope initialization, guest-event validation, schedule environment persistence and shutdown during active model requests.
- `mise run test:app`: **14 passed**.
- `mise run test:eval`: deterministic suite, short-chat/tool-round/compaction records, validation and repeat comparison passed.
- `mise run clippy`: every workspace target passed with warnings treated as errors. Final Runtime refinements were rebuilt/retested and formatting/script lint passed.
- `mise run build` and `mise run bundle`: passed. Guest image `pot.local/sandbox:681c9a19424721263ed5`, Codex `0.159.1`.

The final `scripts/tests/sandbox-runtime.py` run used isolated temporary Host data/homes, two real Linux VMs (2 CPUs, 2 GiB memory, 8 GiB disk each), in-process pot-agent, the actual bundled Codex binary and local deterministic Chat/Responses HTTP models. It completed **41 model requests and four read-only summaries**, and exited successfully after stopping the test Helper/VMs.

Verified behaviors:

1. Host backend, configuration/settings and Pot/Codex chat remain available with all Sandboxes stopped.
2. Main patch/command tools execute in two selected/explicit environments, never the Host Agent directory.
3. Changing the default moves subsequent commands/patches; an existing live stdin session continues in its original Sandbox.
4. Pot and Codex workers use their fixed environments/loadouts; management and active-turn target-override attempts fail.
5. Busy stop/settings changes are rejected. Cancellation kills detached setsid descendants before reporting settlement.
6. Host restart preserves Codex native identity and worker environment bindings without replaying earlier side effects.
7. Stopping environment A does not stop worker B; artifacts persist.
8. Optional desktop startup/shutdown succeeds; shell code cannot read the root broker ledger/token, and desktop HTTP cannot expose executor credentials.

Local evidence: `/tmp/pot-host-agent-acceptance.json`, `/tmp/pot-host-e2e.log`, `/tmp/pot-host-agent-acceptance.helper.log`, and the corresponding `/tmp/pot-host-{test,app-test,check,clippy,eval,build,bundle,lint}.log` files.

## Limits

No paid inference or real-provider login was performed. Model planning/semantic quality, fresh installation/distribution signing, UI appearance/IME/sleep-wake matrices and a full executor fault-injection matrix are outside this run. Earlier UI observations remain in their dated report.

Linux is the delivered provider. Direct Host tools, macOS/Windows Sandbox providers and mutual filesystem/process isolation between tasks sharing a VM remain out of scope. HTTP tool output is currently batched per execution/poll response; use yield_time_ms and write_stdin for progress polling. Live sessions expire at turn end and cannot resume after cancellation/restart. Retired guest-Agent/Pi product state has no compatibility migration; use the new Host data layout.

## Unit-test follow-up: Agent tool injection and Sandbox dispatch

Date: 2026-10-06. Five additional regression tests cover the requested injection and execution boundaries:

- `main_and_worker_model_loadouts_and_dispatch_use_injected_tools` inspects actual model requests for Chat, Responses and Anthropic protocols. Main receives product control tools plus exactly one copy of each execution tool; workers receive execution tools only. It checks target-selector schemas, policy-derived limits, exact executor arguments/role/owner/call IDs, control-tool dispatch, worker management/delegation rejection, and absence of Host file effects.
- `configured_loadout_restricts_model_tools_and_dispatch` checks that configured builtins and the allow-list narrow both model-visible tools and actual dispatch, even when MCP advertises execution tools.
- `mcp_lists_distinct_main_worker_and_summary_loadouts` exercises authenticated MCP initialization/listing and verifies main, worker and read-only summary tools, including target-selector restrictions. This is the shared MCP surface used by Codex; no Codex binary is launched by this unit test.
- `main_context_routes_commands_patch_and_sessions_through_sandbox` uses the default Runtime context injection, an isolated Unix Helper double and two independent authenticated HTTP executor doubles. It verifies lazy preparation, default/explicit routing, command/patch arguments and policy, target-selector removal, guest cwd, output ID remapping and result unwrapping, receipt deduplication, stdin routing after default changes, scope reuse, cleanup and expired-session rejection. Host workspaces receive no command or patch effects.
- `task_context_keeps_assigned_sandbox_and_rejects_control_calls` creates and claims a real Runtime task, checks Worker role injection and task cwd, verifies its persisted environment after main changes the default, and rejects management/inventory/retarget calls before Helper or guest dispatch.

The doubles exercise production authorization, durable receipts and HTTP dispatch; they do not inject `LocalTools`. Only test builds can override executor addresses to use per-fixture ephemeral ports. Production continues to use guest port 17701.

Verification: `mise run test` passed **198 tests**, with the existing live-provider test ignored. After separating the two mocked executor listeners, both affected Runtime tests passed again. `mise run clippy` passed across all targets with warnings treated as errors, and `mise run lint` passed. No paid model calls or real VMs were required for this follow-up.

Local evidence: `/tmp/pot-injection-{test,agent,runtime,clippy,lint}.log`.
