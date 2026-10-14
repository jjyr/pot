# Host pot-agent and Sandbox execution acceptance

## Implementation

Core defines the injected ToolExecutor contract and shared exec_command/write_stdin/apply_patch schemas and validation. Runtime controls roles and Sandbox routing; the guest Rust executor implements patch and pipe sessions behind the authenticated root broker. Product code has no implicit local execution backend. Eval and isolated unit/model tests explicitly inject LocalTools.

Main can manage Sandbox inventory/default selection and choose execution targets. Workers receive one persisted Sandbox, cannot manage/delegate/retarget, and use its task directory. Saved schedules retain their execution target across default changes and omitted-target updates. Invalid persisted assignments fail visibly rather than receiving a replacement. Guest output can only produce bounded stdout/stderr events; it cannot forge Agent lifecycle events, and output IDs are remapped to the model invocation.

Concurrent calls durably initialize one scope before dispatch. Sessions remain bound to owner, turn, Sandbox and VM generation. Active calls include preparation in the cleanup barrier. Cancellation/turn completion drains root-owned cgroups, including setsid descendants. Failed cleanup leaves an unknown-operation record and blocks execution/stop until explicit recovery.

## Verification

- `mise run check`: formatting/script checks, workspace Rust compilation and Swift Helper compilation passed.
- `mise run test`: **193 passed**, one existing live-provider test ignored. Coverage includes model protocols/context/compaction/accounting, preference routing, missing-backend rejection, worker tool restrictions, stale/foreign sessions, generation changes, serialized scope initialization, guest-event validation, schedule execution target persistence and shutdown during active model requests.
- `mise run test:app`: **14 passed**.
- `mise run test:eval`: deterministic suite, short-chat/tool-round/compaction records, validation and repeat comparison passed.
- `mise run clippy`: every workspace target passed with warnings treated as errors. Final Runtime refinements were rebuilt/retested and formatting/script lint passed.

Verified behaviors:

2. Main patch/command tools execute in two selected/explicit execution targets, never the Host Agent directory.
3. Changing the default moves subsequent commands/patches; an existing live stdin session continues in its original Sandbox.
5. Busy stop/settings changes are rejected. Cancellation kills detached setsid descendants before reporting settlement.
7. Stopping execution target A does not stop worker B; artifacts persist.
8. Optional desktop startup/shutdown succeeds; shell code cannot read the root broker ledger/token, and desktop HTTP cannot expose executor credentials.

Local evidence: `/tmp/pot-host-agent-acceptance.json`, `/tmp/pot-host-e2e.log`, `/tmp/pot-host-agent-acceptance.helper.log`, and the corresponding `/tmp/pot-host-{test,app-test,check,clippy,eval,build,bundle,lint}.log` files.

## Limits

No paid inference or real-provider login was performed. Model planning/semantic quality, fresh installation/distribution signing, UI appearance/IME/sleep-wake matrices and a full executor fault-injection matrix are outside this run. Earlier UI observations remain in their dated report.

Linux is the delivered provider. Direct Host tools, macOS/Windows Sandbox providers and mutual filesystem/process isolation between tasks sharing a VM remain out of scope. HTTP tool output is currently batched per execution/poll response; use yield_time_ms and write_stdin for progress polling. Live sessions expire at turn end and cannot resume after cancellation/restart. Retired guest-Agent/Pi product state has no compatibility migration; use the new Host data layout.

## Unit-test follow-up: Agent tool injection and Sandbox dispatch

Date: 2026-10-06. Five additional regression tests cover the requested injection and execution boundaries:

- `main_and_worker_model_loadouts_and_dispatch_use_injected_tools` inspects actual model requests for Chat, Responses and Anthropic protocols. Main receives product control tools plus exactly one copy of each execution tool; workers receive execution tools only. It checks target-selector schemas, policy-derived limits, exact executor arguments/role/owner/call IDs, control-tool dispatch, worker management/delegation rejection, and absence of Host file effects.
- `configured_loadout_restricts_model_tools_and_dispatch` checks that configured builtins and the allow-list narrow both model-visible tools and actual dispatch, even when MCP advertises execution tools.
- `main_context_routes_commands_patch_and_sessions_through_sandbox` uses the default Runtime context injection, an isolated Unix Helper double and two independent authenticated HTTP executor doubles. It verifies lazy preparation, default/explicit routing, command/patch arguments and policy, target-selector removal, guest cwd, output ID remapping and result unwrapping, receipt deduplication, stdin routing after default changes, scope reuse, cleanup and expired-session rejection. Host workspaces receive no command or patch effects.
- `task_context_keeps_assigned_sandbox_and_rejects_control_calls` creates and claims a real Runtime task, checks Worker role injection and task cwd, verifies its persisted execution target after main changes the default, and rejects management/inventory/retarget calls before Helper or guest dispatch.

The doubles exercise production authorization, durable receipts and HTTP dispatch; they do not inject `LocalTools`. Only test builds can override executor addresses to use per-fixture ephemeral ports. Production continues to use guest port 17701.

Verification: `mise run test` passed **198 tests**, with the existing live-provider test ignored. After separating the two mocked executor listeners, both affected Runtime tests passed again. `mise run clippy` passed across all targets with warnings treated as errors, and `mise run lint` passed. No paid model calls or real VMs were required for this follow-up.

Local evidence: `/tmp/pot-injection-{test,agent,runtime,clippy,lint}.log`.

## Additional skills unit tests

Date: 2026-10-06. Added nine Rust tests and four portable Python tests without changing production behavior.

- Filesystem discovery: recursive groups, bundle asset boundaries, hidden directories, linked SKILL.md files and linked roots; BOM/CRLF and quoted YAML, fallback names and explicit-only metadata; malformed neighbors; deterministic same-root collisions; missing directories and empty versus unavailable selections.
- Agent model requests: the default filesystem provider for both Main and Worker, metadata refresh between turns, mapped paths, lazy body loading and absence of persistent catalog copies; empty selections; unavailable selected skills failing before inference; the final tool allow-list excluding the skill reader.
- Runtime: repository boundaries represented by either a `.git` directory or a worktree file, ancestor discovery when no repository boundary exists, and fixed task skill roots after the main default Sandbox changes.
- Guest mount setup: namespace creation failure before any mount, readonly remount failure before subsequent sources, direct and parent-directory symlink rejection, and traversable empty views under a restrictive umask without source bindings.

Verification: `mise run test` passed **213 tests**, with one existing live-provider test ignored. `mise run test:skills` passed **7 tests**. `mise run clippy:backend` and `mise run lint` passed. Mount syscall tests use doubles; actual readonly enforcement remains established by the preceding real-VM acceptance, which was not repeated for this test-only follow-up.

Local evidence: `/tmp/pot-skills-extra-{targeted,test,clippy,lint}.log`.

## Builtin creator/installer and readonly mapping follow-up

A shared JSON corpus exercises Rust discovery and Python validation. It exposed acceptance of a closing delimiter prefix such as `---suffix`; the loader now requires a complete delimiter line. Guest PyYAML is pinned through Alpine packages, local tests pin the same version, and CI runs the authoring and mount suites. Packaging excludes Python bytecode caches.

Verification:

- Backend: **215 passed**, one existing live-provider test ignored. Includes shared format fixtures and Runtime builtin locations/override rejection. After adding the final delimiter corpus case, the shared-loader test passed again.
- Authoring/installer: **13 passed**, including CLI exit codes, valid packaged skills, resource checks, metadata limits, conflicting/invalid updates, rollback, preserved permissions, mocked GitHub download, archive traversal/symlink rejection and download failure.
- Mount policy: **8 passed**, including actual syscall-argument inspection for main builtin readonly remounts and writable user mounts. These unit tests use syscall doubles.
- Backend Clippy, source/script lint, Rust/App/Swift build and bundle passed.

Evidence: `/tmp/pot-skill-tools-acceptance.json`, `/tmp/pot-skill-tools-acceptance.helper.log`, `/tmp/pot-skill-tools-{test,clippy,lint,build,bundle,e2e}.log`, `/tmp/pot-skill-authoring.log` and `/tmp/pot-skill-format-test.log`.

Limits: remote GitHub behavior uses a local HTTP response double in unit tests, not a live/private repository. The format validator checks inline resource links and does not establish script behavior or instruction quality. Installation rollback is tested for an ordinary publication error; crash recovery and concurrent installers are not transactional.

## Unified global skills directory follow-up

Date: 2026-10-06. Verified the flat global skill bundle layout. Both agent roles now discover global user and builtin skills in one execution view: `~/.pot/agent/skills/<name>`. Project `.agents/skills` discovery and explicit task selections remain. Runtime resolves injected paths against the execution home and derives reserved names from the builtin catalog. The installer defaults to the execution home using a flat destination; skill instructions use the unified path and omit source/mount details.

Helper creates only the global user root. Each guest scope binds that root under its assigned HOME, then overlays each builtin bundle with a readonly bind mount. Task scopes also remount the entire user root readonly. Empty builtin mountpoints in the user backing store contain no builtin content. Setup rejects missing/symlink sources or redirected views and fails on syscall errors. Main/task Unix identities and authorization remain unchanged.

Verification:

- Backend: **215 passed**, one existing live-provider test ignored. Updated common global catalogs, home-relative locations, project precedence, reserved names, task selection restoration and fixed Sandbox bindings. The simplified discovery-priority test passed again after its final update.
- Authoring/installer: **14 passed**, including a default-destination regression that changes HOME and checks direct installation into `.pot/agent/skills` without category directories.
- Mount policy: **9 passed**, covering identical bundle sets for both roles, readonly builtin binds, task user-root readonly remount, missing sources, failed syscalls, symlink sources/mountpoints/home paths and restrictive umask handling.
- Backend Clippy, source/script lint, Rust/App/Swift build and bundle passed.

Evidence: `/tmp/pot-flat-skills-acceptance.json`, `/tmp/pot-flat-skills-acceptance.helper.log` and `/tmp/pot-flat-skills-{test,authoring,discovery,clippy,lint,build,bundle,e2e}.log`.

Limits: global task skills are readonly, but project skill files retain existing workspace permissions. Direct Host execution and additional Sandbox providers remain outside this acceptance; they must implement the same home-relative path contract when introduced.
