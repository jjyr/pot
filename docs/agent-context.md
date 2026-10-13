# Agent context

Control/execution target adapters own AGENTS.md discovery, execution-target selection, refresh and error policy. pot-agent receives caller-provided text through `ExecutionContext.instructions` and `AgentExtensions::instructions`; it performs no AGENTS.md filesystem discovery. Standalone callers can inject their own instructions or leave this field empty.

## Sources and scope

Global Main instructions come from the owning Runtime's
`~/.pot/agent/AGENTS.md` (`POT_AGENT_HOME` overrides the Agent home directory).
For Control-owned assistants this source belongs to Control, not joined execution
nodes. Task Agents do not load global Main or assistant AGENTS.md. The approved
remote launch uses Control-selected global skills/plugins while preserving project
AGENTS.md and project skills at the execution workspace; see
[Architecture](architecture.md) for the execution contract.
Permission storage is separate from instruction discovery; see
[Agent permissions](permissions.md). Project discovery uses the actual tool execution cwd. Main uses Host execution:
its configured workspace without a selected Sandbox, or `product/agent-workspace`
when a Sandbox default exists. Selecting a Sandbox changes future task defaults,
not Main's Host project context. Workers use their fixed Host/Sandbox task workspace.

Discovery starts at the execution cwd and finds the nearest ancestor with a `.git` file or directory. Sandbox discovery stops at its workspace mount boundary. Host discovery uses the nearest project root as its boundary, or cwd when there is no repository marker. It loads AGENTS.md files from that root through cwd in root-to-leaf order. Without a root marker it reads only cwd. Nested repositories stop inheritance from outer repositories. Descendants below cwd are not eagerly scanned; the injected guidance tells the agent to inspect applicable descendant AGENTS.md files before editing there.

Only `AGENTS.md` is read. `AGENTS.override.md` and fallback filenames are ignored. Each chat assistant owns a versioned AGENTS.md text record in its own database. This record is independent of execution targets and travels with the assistant's state. There is no legacy assistant-file import or saved-preference conversion. Project AGENTS.md discovery remains filesystem-based. UI edits and assistant tools operate on the same DB record. Every successful save creates a new revision, including reverting to identical text.

Main contexts load global instructions, assistant rules and applicable project directory instructions. Worker contexts load only applicable project directory instructions: workers have no assistant AGENTS.md and inherit neither assistant rules nor global Agent-home rules. The delegating assistant must include relevant constraints in the task brief. Project AGENTS.md files remain ordinary directory policies, separate from the assistant's own rule record. Assistant rules are snapshotted for each execution and never grant permissions. Deeper directory guidance takes precedence on conflicts, and explicit user instructions take precedence over file instructions. There is no instruction-file size cap or truncation; normal model context limits still apply. Empty/whitespace-only contents are omitted; invalid UTF-8 is decoded lossily.

The Runtime instruction filesystem interface uses execution-visible paths. Its current implementation reads the Host-shared Sandbox workspace mount without starting a VM and resolves relative and absolute symlinks in guest path space. Links outside that mount are omitted with a diagnostic, so Host files cannot be pulled in through a project symlink. Other guest mounts and image files are not instruction sources.

## Refresh and injection

Runtime reloads sources when preparing each execution context. The resulting snapshot is used for that execution's model requests. Global instruction caching is used by Main services in the current Runtime process. A global read failure retains the last successful text and emits a diagnostic; repeated identical failures are reported once until recovery. Confirmed absence or a successful empty read clears the cached value. The cache is not persisted across restarts. Unexpected project read errors fail context preparation rather than reusing stale project instructions.

pot-agent appends the injected text for Main and Worker, including custom system prompts and configurations with no execution reader tool. Summary contexts skip loading and injection. Instruction injection does not copy the text into pot-agent's persistent conversation history.

## Assistant tools

`read_agent_instructions({})` returns this chat assistant's `text` and `revision`.
`update_agent_instructions({text})` replaces the complete text
only after owner approval. The assistant identity comes from the execution owner;
neither tool accepts an assistant ID or execution target selector. These tools are
Main-only and remain subject to the configured tool allow-list. Updates always
request fresh review, even with allow-by-default policy or matching grants. The
Runtime snapshots the DB revision when preparing review and checks it at commit,
so a concurrent UI save cannot be overwritten by a stale approved proposal.
The model does not supply a revision. Permission requests
bind the owner, Thread generation, turn, tool call and exact new content. The
Agent dispatcher executes these calls internally after authorization; they are
not exposed as unaudited MCP writes.

## Reference and tests

Assistant prompt text is kept in `crates/assistant/prompts/`.
Main's built-in delegation prompt lives in
[`crates/assistant/prompts/main_prompt.txt`](../crates/assistant/prompts/main_prompt.txt).
`MAIN_PROMPT` in `crates/assistant/src/orchestration.rs` embeds it at compile time
with `include_str!`; Control supplies it when preparing Main execution context.
Editing the text requires rebuilding the binary; there is no runtime file read. It is not global `AGENTS.md` or a routine scheduler. A custom
`system_prompt` replaces the default pot-agent prompt.

Tests cover repository/worktree markers, nested repositories, absent roots, ignored overrides, file changes/removal, empty and invalid UTF-8 files, files larger than 32 KiB, guest symlink resolution and Host escape rejection, global failure/recovery/cache clearing, Main Host project context and fixed task assignment. Runtime context tests cover Main/Worker roles plus Summary exclusion; pot-agent request tests cover all three supported model protocols, custom prompts and history separation. Run `mise run test` and `mise run clippy:backend`.


## Global ownership

Control selects global plugins and Skills for connected Tasks. Execution machines
consume the fixed snapshot, never their own global catalogs. Missing resources or
unsupported configuration fail explicitly. Independent local mode uses its own
globals. Project AGENTS.md and Skills remain execution-workspace resources.

## Routing rules

Each conversational assistant owns AGENTS.md text in its database, with an
opaque revision that changes on every save. It is part of the assistant's state,
independent of execution target. Main contexts receive their own rules;
workers receive task briefs and applicable project directory instructions, not
assistant or global Agent-home rules. 
The built-in dispatch instructions and fresh eligible Profile catalog remain
injected independently. A custom system prompt still receives file instructions.

Explicit task choices take precedence over saved rules. Without an explicit target,
Control selects a machine labelled `task-runner`, preferring its own Runtime and
then stable Runtime ID order. The selected machine's execution target is fixed
when the task is created; local execution retains the configured Host/Sandbox
default, while an implicit remote target uses Host. Explicit Host/Sandbox references
remain available. Labels describe routing intent, not a capability guarantee;
execution fails normally if the selected target cannot do the work. Rules cannot enable a
service, allow a Profile or grant execution authority. `create_task` retains
`selection_reason`; dedicated preference tools and `preference_revision` have
been removed with user agreement. Existing tasks retain configuration snapshots.

Click the current assistant name to select/create an assistant or edit its name
and AGENTS.md. Edits remain drafts until Save. Each assistant retains its draft
when switching; saving compares the definition revision and the rules revision. A conflict preserves the draft and reports that the saved version changed.
Agent edits use `read_agent_instructions` and `update_agent_instructions`.
Updates require fresh owner approval; the server captures the review baseline, and existing grants
cannot skip review. UI and model edits share the DB record and conflict checks.
Host file tools no longer edit the assistant's live rule record.

The initial assistant retains the current conversation and Tasks. Assistant
rules are read exclusively from the current DB record. Old assistant files and
saved preference metadata are not imported or converted. Existing DB rules are
preserved. Project AGENTS.md files continue to be discovered independently for
both assistants and workers.

Configured Profile identity/model/capabilities determine eligible candidates.
An unavailable required target must produce clarification or a visible failure;
no extra classifier model call or silent executor substitution is introduced.

Task-routing token accounting remains a subset of the invoking assistant's
chat usage, never an additional allocation. The Runtime usage ledger is shared;
Threads, routing defaults, task ownership and assistant rules are independent.


# Skill bundles

## Directories and discovery

```text
skills/<name>/                    # Packaged builtin bundles in this repo
~/.pot/agent/skills/<name>/        # User global bundles
PROJECT/.agents/skills/<name>/    # Project-specific bundles
```

There are no shared/main/task categories. Standalone Main and task agents discover
their owning Runtime's global user and builtin skills. Connected tasks instead
use the global versions selected by Control. Every bundle contains UTF-8 SKILL.md
with YAML frontmatter and optional scripts, references and assets. Only metadata
is advertised during discovery; full instructions are read on demand.

Runtime supplies ordered roots. Project directories are discovered from the assigned workspace upwards, stopping at a repository root or the Sandbox workspace boundary. The nearest project wins, then global user skills, then builtins. Names discovered in the packaged builtin catalog are reserved: user/project attempts to shadow them are diagnosed and ignored. Bundle names match their directory names in authored and packaged skills.

Malformed bundles produce diagnostics and are omitted. Symlink directories and SKILL.md symlinks are skipped. Supporting assets inside a bundle are not recursively discovered as additional skills. Catalog ordering and conflict resolution are deterministic.

## Unified execution view and permissions

Independent local Host execution advertises the real user/builtin source paths. Local Sandbox execution exposes global bundles at `~/.pot/agent/skills/<name>`. Connected execution exposes the read-only Control cache at the absolute paths injected into context. Runtime injects resolved absolute paths, so models do not have to expand `~`. In the Linux provider, the executor sets HOME to its assigned execution workspace; the scope mount view is installed beneath that home. A main scope uses `/data/workspace`, and a task scope uses `/data/workspace/tasks/<task-id>`. Different providers must implement the same home-relative convention.

| Resource | Main scope | Task scope |
|---|---|---|
| Global user bundles | Writable | Read-only |
| Builtin bundles | Read-only | Read-only |
| Project bundles | Existing workspace permissions | Existing workspace permissions |

Helper shares the Host user directory and packaged builtin root into root-only guest source directories. The root broker creates a private mount namespace, binds the global user root at the home-relative skill path, and binds each builtin bundle read-only over its corresponding entry. Task scopes also remount the user root read-only. Empty reserved mountpoint directories can exist in the user backing directory; builtin files are never copied there. Symlink sources/mountpoints and failed mount preparation fail the operation without an unrestricted fallback.

Main/task runners remain separate unprivileged Unix users; task scopes cannot access raw writable shares or another main scope's writable view through `/proc`. Tool children drop effective capabilities and set no_new_privs. Shared workspace group permissions retain desktop access to task files, but do not provide mutual isolation between tasks in one Sandbox. Skills grant no extra tools or Sandbox lifecycle authority.

Configuration overrides are POT_AGENT_SKILLS_DIR for the Host user directory and POT_BUILTIN_SKILLS_DIR for packaged builtin files. Helper supplies the latter to Server. Standalone development defaults to this repo's flat skills directory. Changing the configured source root requires restarting the Sandbox; directory contents remain live through the share. No compatibility layer for the previous role-specific directory layout is provided.

## Injection and task selection

ExecutionContext.skill_resources carries ordered SkillRoot entries, reserved names and optional selections. pot-agent discovers supplied filesystem roots without interpreting roles, Sandbox identity or mounts. AgentExtensions::skills(context) is the shared override for main/task callers.

Task creation accepts optional `skills: ["name", ...]`: omitted discovers all skills, a list narrows discovery, and an empty list disables injection. Local tasks preserve selections; connected tasks additionally pin Control's global bundle snapshot across turns and restart. Unavailable selected skills fail visibly when preparing a turn.

Catalogs reload each turn and append to the system prompt without entering persistent history. Summary turns omit the catalog, and pot-agent does not advertise skills if the final tools lack exec_command. Chat `/skill:name` references explicitly select Skills for new tasks delegated from that user message; they are also visible to the assistant as user instructions. Main project discovery reflects its actual Host working directory; task execution target bindings remain fixed.

## Creation, installation and validation

Builtin skill-creator describes format, concise instruction design, supporting resources and validation. Builtin skill-installer handles local/GitHub installation, updates, listing and removal. Their instructions expose only the unified skill path, project paths and usable commands; they do not describe source storage or mounts. Tasks can read these same instructions, but readonly global mounts prevent global mutations.

The creator's scripts/quick_validate.py validates YAML metadata and scaffolds, and checks ordinary resource files and inline local Markdown links. Authored bundles require explicit names and matching directory names. Standard license/metadata/allowed-tools fields may be preserved but do not grant tool permissions. Shared format fixtures exercise Rust discovery and Python validation. Validation does not execute scripts or establish behavioral quality.

The installer uses the adjacent creator validator. It accepts a local bundle, GitHub repo/path/ref or tree URL. Downloads use only credentials available in the current execution target. Archive extraction rejects traversal and special files. Installation preserves resource contents and executable permissions, normalizes readable permissions, validates before publishing, and refuses replacement without --replace. Ordinary publication failures restore the previous directory; crash recovery and concurrent installers are not transactional.

apply_patch remains workspace-scoped. Global installation/update is performed on the owning Control, or the independent local Runtime in App local mode. Connected tasks cannot modify supplied global bundles. Use injected bundle paths for helper scripts, and `--dest PROJECT/.agents/skills` for project installation.

## Message references and machine labels

App and Web autocomplete standalone `/` words at the caret using authenticated,
assistant-scoped `/api/v1/completions` metadata. Candidates include registered
machines, Control-managed labels, and the assistant's current Skill catalog.
References are editable message text: `/machine:RUNTIME_ID`, `/tag:desktop`,
`/skill:skill-creator`. Their identifiers survive drafts, retries and history.
Use spaces around tokens; backtick-quoted examples are not selectors.

A message may specify one machine and multiple labels (AND). A machine plus labels
must satisfy both; conflicting explicit tool destinations are rejected. Absent
matching machines is an explicit routing error. Offline or incapable targets are
not silently replaced. Only the current user turn constrains new delegated tasks;
previous messages, system task reports, existing tasks and scheduled occurrences
never inherit new selections. Selected Skills are persisted with the new task.
Global/project Skill discovery and project AGENTS.md still use the existing rules.

Control initializes its labels to `task-runner`, `desktop` and its OS. Newly joined
machines receive only their declared OS label. Control owns all label edits;
heartbeats, repeated enrollment and restarts do not restore removed labels.
Labels use lowercase ASCII letters, digits, hyphens and underscores.
