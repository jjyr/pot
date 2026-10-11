# Agent instructions

Runtime owns AGENTS.md discovery, execution-environment selection, refresh and error policy. pot-agent receives caller-provided text through `ExecutionContext.instructions` and `AgentExtensions::instructions`; it performs no AGENTS.md filesystem discovery. Standalone callers can inject their own instructions or leave this field empty.

## Sources and scope

Global Main instructions come from the owning Runtime's
`~/.pot/agent/AGENTS.md` (`POT_AGENT_HOME` overrides the Agent home directory).
For Control-owned assistants this source belongs to Control, not joined execution
nodes. Task Agents do not load global Main or assistant AGENTS.md. The approved
remote launch uses Control-selected global skills/plugins while preserving project
AGENTS.md and project skills at the execution workspace; see
[Composable runtimes](runtime-composition.md) for pending implementation scope.
Permission storage is separate from instruction discovery; see
[Agent permissions](permissions.md). A Sandbox uses its shared guest-visible
workspace; a Host-only assistant uses its configured working directory, and a Host
task uses its fixed task working directory. Main uses the default Sandbox at
context creation; a task uses its fixed assignment and task working directory.
Selecting another default affects subsequent Main contexts and does not move tasks.

Discovery starts at the execution cwd and finds the nearest ancestor with a `.git` file or directory. Sandbox discovery stops at its workspace mount boundary. Host discovery uses the nearest project root as its boundary, or cwd when there is no repository marker. It loads AGENTS.md files from that root through cwd in root-to-leaf order. Without a root marker it reads only cwd. Nested repositories stop inheritance from outer repositories. Descendants below cwd are not eagerly scanned; the injected guidance tells the agent to inspect applicable descendant AGENTS.md files before editing there.

Only `AGENTS.md` is read. `AGENTS.override.md` and fallback filenames are ignored. Each chat assistant owns a versioned AGENTS.md text record in its own database. This record is independent of execution environments and travels with the assistant's state. There is no legacy assistant-file import or saved-preference conversion. Project AGENTS.md discovery remains filesystem-based. UI edits and assistant tools operate on the same DB record. Every successful save creates a new revision, including reverting to identical text.

Main contexts load global instructions, assistant rules and applicable project directory instructions. Worker contexts load only applicable project directory instructions: workers have no assistant AGENTS.md and inherit neither assistant rules nor global Agent-home rules. The delegating assistant must include relevant constraints in the task brief. Project AGENTS.md files remain ordinary directory policies, separate from the assistant's own rule record. Assistant rules are snapshotted for each execution and never grant permissions. Deeper directory guidance takes precedence on conflicts, and explicit user instructions take precedence over file instructions. There is no instruction-file size cap or truncation; normal model context limits still apply. Empty/whitespace-only contents are omitted; invalid UTF-8 is decoded lossily.

The Runtime instruction filesystem interface uses execution-visible paths. Its current implementation reads the Host-shared Sandbox workspace mount without starting a VM and resolves relative and absolute symlinks in guest path space. Links outside that mount are omitted with a diagnostic, so Host files cannot be pulled in through a project symlink. Other guest mounts and image files are not instruction sources.

## Refresh and injection

Runtime reloads sources when preparing each execution context. The resulting snapshot is used for that execution's model requests. Global instruction caching is used by Main services in the current Runtime process. A global read failure retains the last successful text and emits a diagnostic; repeated identical failures are reported once until recovery. Confirmed absence or a successful empty read clears the cached value. The cache is not persisted across restarts. Unexpected project read errors fail context preparation rather than reusing stale project instructions.

pot-agent appends the injected text for Main and Worker, including custom system prompts and configurations with no execution reader tool. Summary contexts skip loading and injection. Instruction injection does not copy the text into pot-agent's persistent conversation history.

## Assistant tools

`read_agent_instructions({})` returns this chat assistant's `text` and `revision`.
`update_agent_instructions({text})` replaces the complete text
only after owner approval. The assistant identity comes from the execution owner;
neither tool accepts an assistant ID or environment selector. These tools are
Main-only and remain subject to the configured tool allow-list. Updates always
request fresh review, even with allow-by-default policy or matching grants. The
Runtime snapshots the DB revision when preparing review and checks it at commit,
so a concurrent UI save cannot be overwritten by a stale approved proposal.
The model does not supply a revision. Permission requests
bind the owner, Thread generation, turn, tool call and exact new content. The
Agent dispatcher executes these calls internally after authorization; they are
not exposed as unaudited MCP writes.

## Reference and tests

Main's built-in delegation prompt is `MAIN_PROMPT` in `crates/runtime/src/orchestration.rs`,
injected by Runtime. It is not global `AGENTS.md` or a routine scheduler. A custom
`system_prompt` replaces the default pot-agent prompt.

Tests cover repository/worktree markers, nested repositories, absent roots, ignored overrides, file changes/removal, empty and invalid UTF-8 files, files larger than 32 KiB, guest symlink resolution and Host escape rejection, global failure/recovery/cache clearing, default Sandbox switching and fixed task assignment. Runtime context tests cover Main/Worker roles plus Summary exclusion; pot-agent request tests cover all three supported model protocols, custom prompts and history separation. Run `mise run test` and `mise run clippy:backend`.

Dated validation: [2026-10-07](validation/agent-instructions-20261007.md).
