# Agent context

Control/execution target adapters own AGENTS.md discovery, execution-target selection, refresh and error policy. `pkg/agent` receives caller-provided text through `core.ExecutionContext.Instructions`; it performs no AGENTS.md filesystem discovery. Standalone callers can inject their own instructions or leave this field empty.

## Sources and scope

Global Main instructions come from the owning Runtime's
`~/.pot/agent/AGENTS.md` (`POT_AGENT_HOME` overrides the Agent home directory).
For Control-owned assistants this source belongs to Control, not joined execution
nodes. Task Agents do not load global Main or assistant AGENTS.md. The approved
remote launch uses Control-selected global skills/plugins while preserving project
AGENTS.md and project skills at the execution workspace; see
[Architecture](architecture.md) for the execution contract.
Permission storage is separate from instruction discovery; see
[Agent permissions](permissions.md). Project discovery uses the actual tool execution cwd. Main resolves the machine's
current Host/shared-Sandbox policy at the start of each run. Workers use their
recorded Host/Sandbox task workspace throughout an active execution.

Discovery starts at the execution cwd and finds the nearest ancestor with a `.git` file or directory. Sandbox discovery stops at its workspace mount boundary. Host discovery uses the nearest project root as its boundary, or cwd when there is no repository marker. It loads AGENTS.md files from that root through cwd in root-to-leaf order. Without a root marker it reads only cwd. Nested repositories stop inheritance from outer repositories. Descendants below cwd are not eagerly scanned; the injected guidance tells the agent to inspect applicable descendant AGENTS.md files before editing there.

Only `AGENTS.md` is read. `AGENTS.override.md` and fallback filenames are ignored. Each chat assistant owns a versioned AGENTS.md text record in its own database. This record is independent of execution targets and travels with the assistant's state. Project AGENTS.md discovery remains filesystem-based. UI edits and assistant tools operate on the same DB record. Every successful save creates a new revision, including reverting to identical text.

Main contexts load global instructions, assistant rules and applicable project directory instructions. Worker contexts load only applicable project directory instructions: workers have no assistant AGENTS.md and inherit neither assistant rules nor global Agent-home rules. The delegating assistant must include relevant constraints in the task brief. Project AGENTS.md files remain ordinary directory policies, separate from the assistant's own rule record. Assistant rules are snapshotted for each execution and never grant permissions. Deeper directory guidance takes precedence on conflicts, and explicit user instructions take precedence over file instructions. There is no instruction-file size cap or truncation; normal model context limits still apply. Empty/whitespace-only contents are omitted; invalid UTF-8 is decoded lossily.

The Runtime instruction filesystem interface uses execution-visible paths. Its current implementation reads the Host-shared Sandbox workspace mount without starting the execution environment and resolves relative and absolute symlinks in guest path space. Links outside that mount are omitted with a diagnostic, so Host files cannot be pulled in through a project symlink. Other guest mounts and image files are not instruction sources.

## Refresh and injection

Runtime reloads sources when preparing each execution context. The resulting snapshot is used for that execution's model requests. Global instruction caching is used by Main services in the current Runtime process. A global read failure retains the last successful text and emits a diagnostic; repeated identical failures are reported once until recovery. Confirmed absence or a successful empty read clears the cached value. The cache is not persisted across restarts. Unexpected project read errors fail context preparation rather than reusing stale project instructions.

`pkg/agent` records instructions as classified, append-only context sections for Main
and Worker, including custom system prompts and configurations with no execution
reader tool. Summary contexts skip discovery and new injection; they preserve the latest retained sections instead of revoking unloaded resources. See the message
and compaction contract below.

## Classified messages and append-only context

The base model instructions and tool schemas remain request inputs. Mutable
instructions and reference data are separate from accepted user text. Control
supplies `ExecutionContext.context_sections` (reference-data keys; `instructions`,
`plugins` and `skills` are reserved for Agent instruction providers); `pkg/agent`
owns comparison, rendering and durable history. Neither the assistant driver nor the Agent appends catalogs
or task state to a real user's text.

History schema 2 distinguishes conversation items (user/assistant/tool), context
sections, continuation summaries and runtime result notifications. There is no
schema-1 import. Logical kinds and section fingerprints are internal and are never sent as provider fields.
Context and summary items use tagged/text user messages on all four protocols;
provider adapters may merge adjacent user messages without changing their order
or logical identity. Only conversation user items are real user boundaries;
runtime result notifications are reference data and cannot become the retained
last user message.

Sections have stable keys and deterministic lexical ordering:

| Key | Source and update behavior |
|---|---|
| `environment` | Caller-provided Runtime clock and execution workspace; refreshed for each execution, independently of task state |
| `instructions` | Applicable global, assistant and project instructions; append a complete replacement on change |
| `models` | Main-only configured routing summary; append a complete bounded replacement on change |
| `plugins` | Enabled plugin instructions; append a complete replacement on change |
| `skills` | Lazy-load metadata only, with authorized reader; append a complete replacement on change |
| `tasks` | Main-only compact active/attention metadata; append a complete replacement on change |

Every section message explicitly replaces all earlier contents of its key. A
removed or newly empty section emits one revocation; repeated absence emits
nothing. Unchanged normalized text (CRLF to LF and trimmed outer whitespace)
emits nothing. Earlier messages, user text,
tool results and base instructions are not rewritten to refresh a section.
Instruction sections remain subject to explicit user requests; reference sections
and summaries are data and never grant execution authority. Resources and
instructions are snapshotted for the execution, not refreshed mid-request.

The authoritative comparison baseline is reconstructed from the latest retained
classified message for each key, including revocations. Restart, provider cache
expiry, or loss of the optional token-usage calibration does not discard this
baseline or append duplicate context. No client TTL claims to expire server KV
state. A cold provider request replays the same prefix; only new input or real
state changes append content. Provider/model/tool changes can independently
prevent cache reuse.

The routing summary is capped at 3,500 serialized profile-list bytes plus its
fixed explanatory wrapper. Each profile advertises its default and at most five
saved models, never the complete provider-wide model directory. `get_model_catalog`
lists configured profiles when `profile` is omitted, or queries complete facts
for one profile on demand; `models` and
`configured_models` use offset/limit pagination and report their totals.

### Compaction and continuation

Compaction runs only with settled tool calls. It summarizes conversation items,
including the latest completed tool effects and any earlier continuation summary;
runtime context sections are excluded and regenerated from current sources.
The summary must preserve goals, user constraints, confirmed effects, artifact
paths, failures and unfinished work. Tool schemas are not offered to the summary
request. Empty summaries and summaries containing tool calls are rejected.

The next request has this exact logical layout:

```text
base instructions + current tool schemas
current nonempty context sections (one full injection in stable key order)
continuation summary
last real user message, including its original attachments
```

Old assistant/tool messages and obsolete section versions are replaced by the
summary. The last real user message is retained exactly; a context update or old
summary cannot be mistaken for it. This also permits compacting a long single
user turn after its tools settle. Later requests append replies, actual section
changes and new real input, without adding a second full injection.

The replacement history is durably saved before becoming the live history. It
establishes its own section baseline and clears the old usage anchor. Failed
sampling or checkpoint persistence leaves the previous history intact. The
ordinary input/output-window guard applies before continuation sampling; a
summary is not assumed to fit merely because its byte size decreased. The UI
transcript remains separate from the replaced model history.

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

## Conversational control

Each Host assistant prepares its workspace under `agent-workspace/assistants/ID`.
Task workspaces remain under `agent-workspace/tasks/ID`. A Main tool explicitly
selecting Host uses the assistant’s Host directory even when its default context
was prepared in Sandbox. Correspondence execution retains the recipient’s Host
directory and separate conversation history. These directories organize files;
they do not add a security boundary inside authorized Host execution.

`POST /api/v1/turn/steer` binds `request_id`, `user_message_id` and `text` to the
original active turn. The pending input and visible user message commit together.
A steering message has `steering: true`, `accepted` status while queued and
`complete` status once its IDs are durably incorporated in the Agent checkpoint.
An exact retry returns the original receipt and never retargets another turn.

`POST /api/v1/turn/control` binds `user_message_id` to `pause` or `resume`.
`turn.paused` is persisted and streamed in snapshots. Pausing waits at safe model
boundaries; in-flight work settles normally. It neither recreates a turn nor
relaxes tool or machine permissions.

## Reference and tests

Assistant prompt text is kept in `internal/assistant/assets/`.
Main's built-in delegation prompt lives in
[`main_prompt.txt`](../internal/assistant/assets/main_prompt.txt).
The Go assistant package embeds it at compile time
with `go:embed`; Control supplies it when preparing Main execution context.
Editing the text requires rebuilding the binary; there is no runtime file read. It is not global `AGENTS.md` or a routine scheduler. A custom
`system_prompt` replaces the default `pkg/agent` prompt.

Tests cover repository/worktree markers, nested repositories, absent roots, ignored overrides, file changes/removal, empty and invalid UTF-8 files, files larger than 32 KiB, guest symlink resolution and Host escape rejection, global failure/recovery/cache clearing, Main Host project context and fixed task assignment. Runtime context tests cover Main/Worker roles plus Summary exclusion; `pkg/agent` request tests cover all four supported model protocols, custom prompts, append-only history, cold-cache prefix replay and compaction reconstruction. Run `mise run test` and `mise run check:go`.


## Global ownership

Control selects global plugins and Skills for connected Tasks. Execution machines
synchronize the selected names from Control before each run, never using their
own global catalogs. Requests contain names, not versions. Missing resources or
unsupported configuration fail explicitly. Independent local mode uses its own
globals. Project AGENTS.md and Skills remain execution-workspace resources.

## Pre-execution resource synchronization

```text
Control task launch
  brief + model settings + skill names + plugin names
                         |
                         v
Execution Runtime accepts the run
                         |
             authenticated request by name
                         |
                         v
Control current Skills / owning assistant's plugin Profile
                         |
                         v
Runtime verifies and materializes the selected bundles
  + discovers local project Skills and AGENTS.md
  + registers synchronized plugin tool definitions
                         |
                         v
Task Agent starts: ordinary cat / scripts / tool calls
```

No Skill-loading tool, virtual filesystem or resource version parameter is
introduced. Synchronization failure stops the run before model sampling. The destination Runner records the prepared content and digest once for the
original run. Allowance resumes reuse that evidence; queued Tasks still read the
current sources on first preparation. Control keeps no per-Task resource archive.
Cache digests identify content integrity and bind one execution; they are not
Task resource selectors. Local tasks prepare the same resource layout without a
network hop. Global Skill metadata and paths are injected; full instructions
remain lazy reads of ordinary files.

## Routing rules

Each conversational assistant owns AGENTS.md text in its database, with an
opaque revision that changes on every save. It is part of the assistant's state,
independent of execution target. Main contexts receive their own rules;
workers receive task briefs and applicable project directory instructions, not
assistant or global Agent-home rules. 
The built-in dispatch instructions and fresh configured Provider/model catalog remain
injected independently. A custom system prompt still receives file instructions.

Explicit task choices take precedence over saved rules. Without an explicit target,
Control selects a machine labelled `task-runner`, preferring its own Runtime and
then stable Runtime ID order. Task and schedule requests select `machine_id`.
At dispatch, the selected machine's current policy determines direct Host or
shared Sandbox execution. The actual context is recorded for this execution;
reruns create new Tasks. Same-machine Host exceptions require machine policy
approval and do not change the Task's normal context. Labels describe routing intent, not a capability guarantee;
execution fails normally if the selected target cannot do the work. Rules guide autonomous model selection from configured Providers; they cannot
enable an unconfigured service or grant execution authority. `create_task` retains
`selection_reason`; dedicated preference tools and `preference_revision` have
been removed with user agreement. Existing tasks retain configuration snapshots.

Use the persistent Chat assistant pane to select assistants and its + action to
create one. Creation selects the owning Space's default Provider and that
Provider's concrete default model. The assistant editor changes name, Provider,
model, parameters and its own instructions without opening global settings.
Instructions and model parameters start collapsed. Edits remain drafts until
Save; conflicts preserve the draft. UI and agent edits share the DB instruction
record and revision checks. `read_agent_instructions` and
`update_agent_instructions` remain owner-scoped; instruction updates retain their
owner-approval contract. Host file tools do not edit the live rule record.

Task model routing is autonomous and requires no routing confirmation or user
allowlist. The invoking assistant uses explicit task instructions, its AGENTS.md,
the available configured Provider/model catalog and task requirements to select a
route. With no explicit route, Control snapshots that assistant's active model
configuration. No separate user-managed task default, Follow Chat toggle or
worker-model configuration is required. Model selection and execution-machine
placement remain independent. Unavailable explicit routes fail visibly rather
than silently changing models. Provider/model definitions are shared within a
Space; chat choices and instructions belong to each assistant.

A fresh registry has no assistants. AI settings can be saved before explicitly
creating the first assistant; configuration saves and restarts never register one.
Existing registered assistants retain their conversations and Tasks. Assistant
rules are read exclusively from the current DB record. Old assistant files and
saved preference metadata are not imported or converted. Existing DB rules are
preserved. Project AGENTS.md files continue to be discovered independently for
both assistants and workers.

Configured Provider identity/model/capabilities determine available candidates.
An unavailable required target must produce clarification or a visible failure;
no extra classifier model call or silent executor substitution is introduced.

`GET`/`POST /api/v1/agent/routing` manages only execution admission capacity:
`revision`, `profile_limits` and `provider_limits`. Saves compare the current
revision; each limit is 1–1000 for a configured identity. A Task keeps its pinned
model while queued. Running and uncertain original runs hold their route limits
until their existing execution settles; capacity never selects a different model.
Provider saves retain an explicit `models` list, exposed as `configured_models`
in the Provider snapshot and through paginated `get_model_catalog` queries.

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

Main discovers its owning Runtime's global user and builtin skills. Tasks load
only explicitly selected global skills. Connected tasks use the selected global
versions supplied by Control. Every bundle contains UTF-8 SKILL.md
with YAML frontmatter and optional scripts, references and assets. Only metadata
is advertised during discovery; full instructions are read on demand.

Runtime supplies ordered roots. Project directories are discovered from the assigned workspace upwards, stopping at a repository root or the Sandbox workspace boundary. The nearest project wins, then global user skills, then builtins. Names in the included builtin catalog are reserved: user/project attempts to shadow them are diagnosed and ignored. Bundle names match their directory names in authored and packaged skills.

Malformed bundles produce diagnostics and are omitted. Symlink directories and SKILL.md symlinks are skipped. Supporting assets inside a bundle are not recursively discovered as additional skills. Catalog ordering and conflict resolution are deterministic.

## Unified execution view and permissions

Main Host execution advertises the real user/builtin source paths. Tasks materialize their selected global bundles under the execution workspace’s `.pot/control-resources/<digest>/` before starting; both local and connected execution advertise these read-only files through injected absolute paths. The digest identifies cache content, not a requested version. Local Sandbox global source mounts remain at `~/.pot/agent/skills/<name>`. Runtime injects resolved absolute paths, so models do not have to expand `~`. In the Linux provider, the executor sets HOME to its assigned execution workspace; the scope mount view is installed beneath that home. A main scope uses `/data/workspace`, and a task scope uses `/data/workspace/tasks/<task-id>`. Different providers must implement the same home-relative convention.

| Resource | Main scope | Task scope |
|---|---|---|
| Global user bundles | Writable | Read-only |
| Builtin bundles | Read-only | Read-only |
| Project bundles | Existing workspace permissions | Existing workspace permissions |

Helper shares the Host user directory and packaged builtin root into root-only guest source directories. The root broker creates a private mount namespace, binds the global user root at the home-relative skill path, and binds each builtin bundle read-only over its corresponding entry. Task scopes also remount the user root read-only. Empty reserved mountpoint directories can exist in the user backing directory; builtin files are never copied there. Symlink sources/mountpoints and failed mount preparation fail the operation without an unrestricted fallback.

Main and task runners use the same unprivileged guest account so installed toolchains remain reusable. Execution scopes have independent processes and sessions, but are not mutual security boundaries. Tool children drop effective capabilities and set no_new_privs. Shared workspace group permissions retain desktop access to task files, but do not provide mutual isolation between tasks in one Sandbox. Skills grant no extra tools or Sandbox lifecycle authority.

Configuration overrides are POT_AGENT_SKILLS_DIR for the Host user directory and POT_BUILTIN_SKILLS_DIR for packaged builtin files. Helper supplies the latter to Server. Standalone development defaults to this repo's flat skills directory. Changing the configured source root requires restarting the Sandbox; directory contents remain live through the share.

## Injection and task selection

`core.ExecutionContext.SkillResources` carries ordered SkillRoot entries, reserved names and optional selections. `pkg/agent` discovers supplied filesystem roots without interpreting roles, Sandbox identity or mounts. Callers provide `core.SkillResources` for Main and Task contexts.

Task creation accepts optional `skills: ["name", ...]` selecting global user or builtin bundles only. Omitted or empty lists load no global skills. Project skills are always discovered independently and are not filtered by this list. Selections persist, but each new execution synchronizes current contents before starting the Agent. Contents remain stable for that run; Control does not retain per-task bundle versions. Unavailable selected global skills fail visibly when preparing a turn, even if a project skill has that name.

Catalogs reload each execution and use the classified `skills` history section. Unchanged rendered metadata is not appended again; changes replace the complete section through a new message. Removal appends an explicit revocation. Summary executions add no catalog, and `pkg/agent` does not advertise skills if the final tools lack exec_command. Chat `/skill:name` references explicitly select global Skills for new tasks delegated from that user message; they are also visible to the assistant as user instructions. Main project discovery reflects its actual execution directory; active tasks retain their recorded execution location.

## Creation, installation and validation

Builtin skill-creator describes format, concise instruction design, supporting resources and validation. Builtin skill-installer handles local/GitHub installation, updates, listing and removal. Their instructions expose only the unified skill path, project paths and usable commands; they do not describe source storage or mounts. Tasks can read these same instructions, but readonly global mounts prevent global mutations.

The creator's scripts/quick_validate.py validates YAML metadata and scaffolds, and checks ordinary resource files and inline local Markdown links. Authored bundles require explicit names and matching directory names. Standard license/metadata/allowed-tools fields may be preserved but do not grant tool permissions. Shared format fixtures exercise Go discovery and Python validation. Validation does not execute scripts or establish behavioral quality.

The installer uses the adjacent creator validator. It accepts a local bundle, GitHub repo/path/ref or tree URL. Downloads use only credentials available in the current execution target. Archive extraction rejects traversal and special files. Installation preserves resource contents and executable permissions, normalizes readable permissions, validates before publishing, and refuses replacement without --replace. Ordinary publication failures restore the previous directory; crash recovery and concurrent installers are not transactional.

apply_patch remains workspace-scoped. Global installation/update is performed on the owning Control, or the independent local Runtime in App local mode. Connected tasks cannot modify supplied global bundles. Use injected bundle paths for helper scripts, and `--dest PROJECT/.agents/skills` for project installation.

## Message references and machine labels

App and Web autocomplete standalone `/` words at the caret using authenticated,
assistant-scoped `/api/v1/completions` metadata. Candidates include registered
machines, Control-managed labels, and the assistant's current global Skill catalog. Project Skills are discovered by the
task and are not offered as global selectors.
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

Local Controls, including the embedded App, initialize with `task-runner`, `local`
and their OS. Newly joined machines receive `runner` and their declared OS label.
Each Space must retain at least one enabled machine labelled `task-runner`;
label edits and member removal cannot remove the last one. Offline members count
toward this configuration constraint, without guaranteeing execution availability.
Control owns all label edits; heartbeats, repeated enrollment and restarts do not
restore removed labels while another enabled machine retains `task-runner`.
Opening a Space with no configured task runner adds the local `task-runner` default.
Labels use lowercase ASCII letters, digits, hyphens and underscores.

## Recovering a stopped Main turn

After a crash or uncertain termination, Main retains the original stopped turn
and blocks new input. The private/native owner endpoint
`POST /api/v1/turn/recovery?assistant_id=ID` accepts `action: "inspect"` to expose
the original `user_message_id`, receipt states and unresolved reasons. Reconcile
requires `action: "reconcile"` and that exact `user_message_id`. Browser
publication excludes this route, and clients cannot supply results or cleanup
acknowledgments.

Control reads original operation receipts and durable Sandbox request bindings.
Actual original cleanup is recorded before terminal acknowledgment; otherwise
cleanup must be independently proven. A safe stopped boundary with no uncertain
effects can be closed, and proven tool results enter the original checkpoint
once. Remaining never-started calls are closed as interrupted. Recovery does not
replay accepted input, model samples or tools. Unknown model/tool outcomes and
previous-boot Host effects without cleanup proof remain fenced. Successful
reconciliation clears only the original execution and allowance wait; exact
retries return its retained completion receipt without affecting a later turn.
