# Agent instructions

Runtime owns AGENTS.md discovery, execution-environment selection, refresh and error policy. pot-agent receives caller-provided text through `ExecutionContext.instructions` and `AgentExtensions::instructions`; it performs no AGENTS.md filesystem discovery. Standalone callers can inject their own instructions or leave this field empty.

## Sources and scope

Global instructions come from `~/.pot/agent/AGENTS.md` (`POT_AGENT_HOME` overrides the Agent home directory). Current permanent permissions are stored alongside it in `permissions.json`; see [Agent permissions](permissions.md). Project instructions come from the assigned Sandbox workspace, not the Host agent session directory. Main uses the default Sandbox at context creation; a task uses its fixed assignment and task working directory. Selecting another default affects subsequent Main contexts and does not move existing tasks.

Discovery starts at the execution cwd and finds the nearest ancestor with a `.git` file or directory, stopping at the Sandbox workspace mount boundary. It loads AGENTS.md files from that root through cwd in root-to-leaf order. Without a root marker it reads only cwd. Nested repositories stop inheritance from outer repositories. Descendants below cwd are not eagerly scanned; the injected guidance tells the agent to inspect applicable descendant AGENTS.md files before editing there.

Only `AGENTS.md` is read. `AGENTS.override.md` and fallback filenames are ignored. Global instructions precede project instructions. Deeper directory guidance takes precedence on conflicts, and explicit user instructions take precedence over file instructions. There is no instruction-file size cap or truncation; normal model context limits still apply. Empty/whitespace-only contents are omitted; invalid UTF-8 is decoded lossily.

The Runtime instruction filesystem interface uses execution-visible paths. Its current implementation reads the Host-shared Sandbox workspace mount without starting a VM and resolves relative and absolute symlinks in guest path space. Links outside that mount are omitted with a diagnostic, so Host files cannot be pulled in through a project symlink. Other guest mounts and image files are not instruction sources.

## Refresh and injection

Runtime reloads sources when preparing each execution context. The resulting snapshot is used for that execution's model requests. Global state is shared across Main and task services in the current Runtime process. A global read failure retains the last successful text and emits a diagnostic; repeated identical failures are reported once until recovery. Confirmed absence or a successful empty read clears the cached value. The cache is not persisted across restarts. Unexpected project read errors fail context preparation rather than reusing stale project instructions.

pot-agent appends the injected text for Main and Worker, including custom system prompts and configurations with no execution reader tool. Summary contexts skip loading and injection. Instruction injection does not copy the text into pot-agent's persistent conversation history.

Managed Codex receives the same Runtime text through its developer instructions. Native Host project discovery is disabled with `project_doc_max_bytes = 0`, preventing the Host session cwd from supplying an unrelated project document. This setting disables Codex discovery; it does not cap the Runtime-injected text. Codex's native account home global instructions remain owned by the CLI: the upstream App Server does not expose a global instruction-provider replacement through the public protocol. This change does not isolate or modify that account home, so native Codex-home global instructions may still be present. Pot's own loader never reads overrides.

## Reference and tests

Main's built-in task coordination/delegation prompt is the `MAIN_PROMPT` constant
in `runtime/src/orchestration.rs`. Runtime injects it through
`runtime/src/agent/driver/feedback.rs`; the Pot engine receives its Host-permission
wording there. pot-agent uses it in its default system prompt in `agent/src/lib.rs`.
A configured `system_prompt` replaces that default. This built-in prompt is not
stored in the global `AGENTS.md` and is not an independent routine scheduler.

The discovery order, nearest repository root, no-root cwd behavior and global last-good refresh policy follow the local Codex implementation reviewed on 2026-10-07. Pot intentionally omits Codex override filenames, configurable fallback/root markers and the project byte cap. It selects one execution environment per agent context.

Tests cover repository/worktree markers, nested repositories, absent roots, ignored overrides, file changes/removal, empty and invalid UTF-8 files, files larger than 32 KiB, guest symlink resolution and Host escape rejection, global failure/recovery/cache clearing, default Sandbox switching and fixed task assignment. Runtime context tests cover both engines and roles plus Summary exclusion; pot-agent request tests cover all three supported model protocols, custom prompts and history separation. Run `mise run test` and `mise run clippy:backend`.

Dated validation: [2026-10-07](validation/agent-instructions-20261007.md).
