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

## Role-scoped filesystem skills follow-up

Date: 2026-10-06. Implemented repo `skills/{shared,main,task}`, Host `~/.pot/agent/skills/{shared,main,task}`, and project `.agents/skills` discovery. Runtime supplies ordered roots and optional task selections; pot-agent uses the same filesystem loader for main and task roles, without mount or Sandbox knowledge. Project overrides user, user overrides builtin, and role-specific overrides shared. Reserved skill-manager overrides are diagnosed and ignored. Metadata refreshes each turn and full instructions remain lazy. No skill registry/content/version database was introduced; the normal task request record preserves an optional selected-name list.

The packaged builtin `main/skill-manager` manages Host user bundles through existing Sandbox tools. Helper shares the user source and readonly builtin source under a root-only guest directory. Broker scopes get private mount views: main can manage all user role directories, tasks see only shared/task as read-only, and builtins remain read-only. Main/task UID separation prevents writable-view access through another role's `/proc` root. Shared workspace group permissions retain desktop access to task files. Root broker mount preparation uses CAP_SYS_ADMIN; tool children have zero effective capabilities and no_new_privs. Failed mount preparation returns a visible error without an unrestricted fallback.

Verification:

- Backend tests: **204 passed**, one existing live-provider test ignored. Added common model skill injection, discovery/priority/YAML/diagnostics/reserved-name/refresh tests and Runtime role-root/task-selection restoration tests. Existing Sandbox adapter tests also check authoritative main/task role transport.
- `mise run test:skills`: **3 passed**, covering role mount views, readonly remount policy, traversable view parents and fail-closed syscall behavior.
- `mise run test:app`: **14 passed**.
- Clippy, source/script lint, deterministic evaluation, Rust/App/Swift build and bundle passed.
- Final real dual-VM acceptance passed using image `pot.local/sandbox:3bb69f0eedb7a69f955c` and Codex `0.159.1`. It used **47 local mock model requests and four read-only summaries**, without paid inference.

Real VM checks confirmed that main-generated shared/main/task bundles persisted on Host; a task in the second VM read the shared/task bundles, received **EROFS** when modifying either, could not see main user/builtin paths or raw source shares, and ran with a distinct UID, no_new_privs and zero effective capabilities. Mock model inspection checked that worker prompts omitted skill-manager/main-only skills. Existing Pot/Codex scope routing, pipe sessions, cancellation, busy-stop, native restart/resume, artifact persistence and desktop/shutdown checks also passed.

Initial real runs exposed missing broker mount capabilities and umask-restricted view parent directories. Both were corrected before the final successful run. The final acceptance stopped its test VMs/Helper and used isolated user/data directories.

Local evidence: `/tmp/pot-skills-acceptance.json`, `/tmp/pot-skills-acceptance.helper.log`, and `/tmp/pot-skills-{all-test,mount-test,app-test,clippy,lint,eval,build,bundle,e2e}.log`.

Limits: explicit `/skill:name` command expansion, a skills UI and mutually isolated task project files are not part of this change. Main project discovery reflects the default environment at turn preparation; default changes or another explicitly targeted environment refresh the catalog next turn. Skills follow current files rather than immutable version snapshots.

## Additional skills unit tests

Date: 2026-10-06. Added nine Rust tests and four portable Python tests without changing production behavior.

- Filesystem discovery: recursive groups, bundle asset boundaries, hidden directories, linked SKILL.md files and linked roots; BOM/CRLF and quoted YAML, fallback names and explicit-only metadata; malformed neighbors; deterministic same-root collisions; missing directories and empty versus unavailable selections.
- Agent model requests: the default filesystem provider for both Main and Worker, metadata refresh between turns, mapped paths, lazy body loading and absence of persistent catalog copies; empty selections; unavailable selected skills failing before inference; the final tool allow-list excluding the skill reader.
- Runtime: repository boundaries represented by either a `.git` directory or a worktree file, ancestor discovery when no repository boundary exists, and fixed task skill roots after the main default Sandbox changes.
- Guest mount setup: namespace creation failure before any mount, readonly remount failure before subsequent sources, direct and parent-directory symlink rejection, and traversable empty views under a restrictive umask without source bindings.

Verification: `mise run test` passed **213 tests**, with one existing live-provider test ignored. `mise run test:skills` passed **7 tests**. `mise run clippy:backend` and `mise run lint` passed. Mount syscall tests use doubles; actual readonly enforcement remains established by the preceding real-VM acceptance, which was not repeated for this test-only follow-up.

Local evidence: `/tmp/pot-skills-extra-{targeted,test,clippy,lint}.log`.

## Builtin creator/installer and readonly mapping follow-up

Date: 2026-10-06. Replaced skill-manager with main-only skill-creator and skill-installer. Their instructions describe Agent-visible locations, format, audience, validation and next-turn discovery; they omit Host persistence and product source layout. Runtime reserves both builtin names. The creator's Python validator follows Codex's YAML/metadata/unfinished-scaffold checks, adapted to Pot, and additionally validates ordinary resource files and inline local resource links. Authored bundles require explicit names and matching directories. The installer handles local/GitHub sources, preserves resources and executable scripts, validates before publication, rejects implicit replacement, and restores a previous directory on publication failure. Imported scripts are not executed during installation.

A shared JSON corpus exercises Rust discovery and Python validation. It exposed acceptance of a closing delimiter prefix such as `---suffix`; the loader now requires a complete delimiter line. Guest PyYAML is pinned through Alpine packages, local tests pin the same version, and CI runs the authoring and mount suites. Packaging excludes Python bytecode caches.

Verification:

- Backend: **215 passed**, one existing live-provider test ignored. Includes shared format fixtures and Runtime builtin locations/override rejection. After adding the final delimiter corpus case, the shared-loader test passed again.
- Authoring/installer: **13 passed**, including CLI exit codes, valid packaged skills, resource checks, metadata limits, conflicting/invalid updates, rollback, preserved permissions, mocked GitHub download, archive traversal/symlink rejection and download failure.
- Mount policy: **8 passed**, including actual syscall-argument inspection for main builtin readonly remounts and writable user mounts. These unit tests use syscall doubles.
- Backend Clippy, source/script lint, Rust/App/Swift build and bundle passed.
- Real dual-VM acceptance passed with image `pot.local/sandbox:8cfc1117bfcff16f330d` and Codex `0.159.1`, **47 local mock model requests and four summaries**. Main attempted writes to both builtin SKILL.md files and received **EROFS**. Both packaged bundles passed the packaged validator; the packaged installer installed a local bundle into shared user skills. Worker readonly shared/task mapping, hidden main directories and existing routing/restart/cancellation/desktop checks passed. Test VMs and Helper were stopped afterward.

Evidence: `/tmp/pot-skill-tools-acceptance.json`, `/tmp/pot-skill-tools-acceptance.helper.log`, `/tmp/pot-skill-tools-{test,clippy,lint,build,bundle,e2e}.log`, `/tmp/pot-skill-authoring.log` and `/tmp/pot-skill-format-test.log`.

Limits: remote GitHub behavior uses a local HTTP response double in unit tests, not a live/private repository. The format validator checks inline resource links and does not establish script behavior or instruction quality. Installation rollback is tested for an ordinary publication error; crash recovery and concurrent installers are not transactional.

## Unified global skills directory follow-up

Date: 2026-10-06. Removed shared/main/task resource categories and flattened repo builtin bundles. Both agent roles now discover global user and builtin skills in one execution view: `~/.pot/agent/skills/<name>`. Project `.agents/skills` discovery and explicit task selections remain. Runtime resolves injected paths against the execution home and derives reserved names from the builtin catalog. The installer defaults to the execution home without an audience parameter; skill instructions use the unified path and omit source/mount details.

Helper creates only the global user root. Each guest scope binds that root under its assigned HOME, then overlays each builtin bundle with a readonly bind mount. Task scopes also remount the entire user root readonly. Empty builtin mountpoints in the user backing store contain no builtin content. Setup rejects missing/symlink sources or redirected views and fails on syscall errors. Main/task Unix identities and authorization remain unchanged.

Verification:

- Backend: **215 passed**, one existing live-provider test ignored. Updated common global catalogs, home-relative locations, project precedence, reserved names, task selection restoration and fixed Sandbox bindings. The simplified discovery-priority test passed again after its final update.
- Authoring/installer: **14 passed**, including a default-destination regression that changes HOME and checks direct installation into `.pot/agent/skills` without category directories.
- Mount policy: **9 passed**, covering identical bundle sets for both roles, readonly builtin binds, task user-root readonly remount, missing sources, failed syscalls, symlink sources/mountpoints/home paths and restrictive umask handling.
- Backend Clippy, source/script lint, Rust/App/Swift build and bundle passed.
- Real dual-VM acceptance passed with image `pot.local/sandbox:ca7bc86b80ad909747c8`, Codex `0.159.1`, **47 local mock model requests and four summaries**. Packaged helpers ran using Path.home's unified directory; installer publication used its default destination. Main created global user bundles and builtin writes returned EROFS. Task read the same user and builtin bundles, all attempted writes returned EROFS, and creating a new global directory returned EROFS. Both roles' model contexts advertised the builtin creator/installer. Existing routing, cancellation, restart, artifacts and desktop/stop checks passed. All test VMs and Helper were stopped.

Evidence: `/tmp/pot-flat-skills-acceptance.json`, `/tmp/pot-flat-skills-acceptance.helper.log` and `/tmp/pot-flat-skills-{test,authoring,discovery,clippy,lint,build,bundle,e2e}.log`.

Limits: global task skills are readonly, but project skill files retain existing workspace permissions. There is no migration for prior audience directories. Direct Host execution and additional Sandbox providers remain outside this acceptance; they must implement the same home-relative path contract when introduced.
