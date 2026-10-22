# Agent context

Local execution adapters own AGENTS.md discovery, execution-target selection, refresh and error policy. `pkg/agent` receives caller-provided text through `core.ExecutionContext.Instructions`; it performs no AGENTS.md filesystem discovery. Standalone callers can inject their own instructions or leave this field empty.

Main handles simple searches, focused research and design discussions directly in
chat, using available tools to verify current information. Tool use, multiple
lookups, reasoning steps and diagrams alone do not require a Task. Main delegates
explicit background requests, prolonged execution, extensive investigation,
substantial file/report creation and code changes to Tasks.

Main arranges all requested background work, then ends its turn without polling,
waiting or a routine confirmation. Task cards provide acceptance feedback. Creation
errors go back to the model for diagnosis and repair; only an unresolved blocker
or required user decision warrants an interruption. Successful schedule creation
also needs no chat recap. Runtime completion events use the shared Agent inbox;
see [Agent messages](assistant-messages.md).

Main replies describe background work with task titles and concrete progress in
the user's language. Task IDs remain in tool arguments and artifact download URLs;
prose includes them only when requested or needed for troubleshooting. When a
reply is needed, it reflects the reported state, including queues and approval
waits, without claiming that execution has started. Empty successful replies
produce neither a completed chat bubble nor a reply notification.

## Sources and scope

Global Main instructions come from the owning Runtime's
`~/.pot/agent/AGENTS.md` (`POT_AGENT_HOME` overrides the Agent home directory).
Task Agents do not load global Main or assistant AGENTS.md. The owning node
prepares selected global Skills/plugins and discovers project instructions in
its local execution workspace. A linked node prepares its own resources; see
[Architecture](architecture.md).

Permission storage is separate from instruction discovery; see
[Agent permissions](permissions.md). Project discovery uses the actual tool execution cwd. Main resolves the machine's
local workspace at the start of each run. Workers use their recorded destination
Runtime workspace throughout an active execution; see [workspace selection](task-workspaces.md).
Worker workspace context contains only the prepared `cwd`, without source project,
branch/base or allocation metadata. Applicable project instructions and Skills
remain ordinary injected instructions.

Discovery starts at the execution cwd and finds the nearest ancestor with a `.git` file or directory. Discovery uses the nearest project root as its boundary, or cwd when there is no repository marker. It loads AGENTS.md files from that root through cwd in root-to-leaf order. Without a root marker it reads only cwd. Nested repositories stop inheritance from outer repositories. Descendants below cwd are not eagerly scanned; the injected guidance tells the agent to inspect applicable descendant AGENTS.md files before editing there.

Only `AGENTS.md` is read. `AGENTS.override.md` and fallback filenames are ignored. Each chat assistant owns a versioned AGENTS.md text record in its own database. This record is independent of execution targets and travels with the assistant's state. Project AGENTS.md discovery remains filesystem-based. UI edits and assistant tools operate on the same DB record. Every successful save creates a new revision, including reverting to identical text.

Main contexts load global instructions, assistant rules and applicable project directory instructions. Worker contexts load only applicable project directory instructions: workers have no assistant AGENTS.md and inherit neither assistant rules nor global Agent-home rules. The delegating assistant must include relevant constraints in the task brief. Project AGENTS.md files remain ordinary directory policies, separate from the assistant's own rule record. Assistant rules are snapshotted for each execution and never grant permissions. Deeper directory guidance takes precedence on conflicts, and explicit user instructions take precedence over file instructions. There is no instruction-file size cap or truncation; normal model context limits still apply. Empty/whitespace-only contents are omitted; invalid UTF-8 is decoded lossily.

Instruction discovery reads the destination Runtime's local filesystem. It does
not start an execution process or translate paths through a guest namespace.

## Refresh and injection

Runtime reloads sources when preparing each execution context. The resulting snapshot is used for that execution's model requests. Global instruction caching is used by Main services in the current Runtime process. A global read failure retains the last successful text and emits a diagnostic; repeated identical failures are reported once until recovery. Confirmed absence or a successful empty read clears the cached value. The cache is not persisted across restarts. Unexpected project read errors fail context preparation rather than reusing stale project instructions.

`pkg/agent` records instructions as classified, append-only context sections for Main
and Worker, including custom system prompts and configurations with no execution
reader tool. Notification contexts refresh the same instructions, plugins, Skills and reference sections as user chat. See the message
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
| `environment` | Optional caller-provided Runtime clock and execution workspace |
| `execution-environment` | Main-only destination policy/revision, workspace and file/temp/network boundaries. Workers receive their prepared cwd through `environment`; tools still enforce current Control-owned policy |
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
instructions are snapshotted for the execution, not refreshed mid-request. Execution
environment snapshots never authorize an operation; dispatch rechecks current
policy, and tool results report the actual execution mode. Tool instructions explain
`request_unsandboxed` and partial-effect inspection before requesting new authority.
When `exec_command` is available, shared Agent system instructions tell Main and
Workers to request narrowly scoped host approval if a sandbox restriction blocks
the user's task. The tool call opens the approval UI without preliminary chat
confirmation. An owner refusal ends that operation. This guidance also applies
with custom model/system prompts; it does not grant execution authority.

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

Both window pressure and idle maintenance use one append-only compaction path.
The stable system protocol is present from normal chat onward. Compaction retains
the same system, tool definitions, resolved references, attachments, ordered
runtime/history and native replay, then appends a temporary compact request.
Native `tool_choice: none` (Gemini `NONE`) disables calls without removing schemas;
no proposed call is dispatched. Empty, truncated, non-reducing or tool-producing
summaries are rejected. The summary preserves concrete goals, prohibitions,
authorization boundaries, confirmed effects, failures and pending work. Regenerable
runtime context and temporary compact controls must not become persistent rules.

```text
normal:  [stable system | tools | runtime + full history]
compact: [same prefix                               | compact request]
resume:  [stable system | tools | current runtime | summary | last user batch]
```

Window pressure reserves space for the full appended instruction and summary
output. Idle maintenance becomes due at 90% of the estimated cache lifetime
(15 minutes by default), with context above 30%. It requires positive reported
cache reads or writes on the last ordinary response. Unknown/zero cache activity
or an expired window skips maintenance without a standalone fallback. It requires
settled current work, no queued user input and no completed-but-unprocessed Task results.
Unfinished background Tasks do not block it. The canonical Task completion writer
boundary and the conversation lane serialize the final start check. Results or
user input arriving after start remain queued until compaction finishes.

The start of the last successful ordinary model request anchors the persisted
cache window. Timing edits recalculate from that anchor; routing changes discard the old timer until a
new request completes. No new conversation work means no self-compaction, and an
unchanged history gets only one idle attempt. Restart abandons interrupted idle
sampling without replaying tools; failed maintenance retains the original history
and releases queued input. Idle allowance refusal leaves no resumable foreground
wait; the next real user turn is checked independently. Idle summaries must save
at least 1,024 estimated tokens.

The next request has this exact logical layout:

```text
base instructions + current tool schemas
current nonempty context sections (one full injection in stable key order)
continuation summary
last real user batch, including original attachments and references
```

Old assistant/tool messages and obsolete section versions are replaced by the
summary. The last real user batch is retained exactly; a context update or old
summary cannot be mistaken for it. This also permits compacting a long single
user turn after its tools settle. Later requests append replies, actual section
changes and new real input, without adding a second full injection.

The replacement history is durably saved before becoming the live history. It
establishes its own section baseline and clears the old usage anchor. Failed
sampling or checkpoint persistence leaves the previous history intact. The
ordinary input/output-window guard applies before continuation sampling; a
summary is not assumed to fit merely because its byte size decreased. The UI
transcript remains separate from the replaced model history. Compact controls are never
saved in the checkpoint; normal requests restore ordinary tool policy. No cache
flush is sent: replacing history changes the prefix, so the next normal request
builds a smaller cache. Actual provider usage determines cache hits.

The transcript displays the compact operation as a plain line of text, without a
bubble or disclosure control.
It includes trigger, status, elapsed time and estimated before/after size. Its
private diagnostic record retains request/response evidence and links to the
actual metered attempts for investigation; usage is never added twice. Chat and
usage pages do not expose a download action.

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

Each assistant prepares `agent-workspace/assistants/ID` on its owning Control.
Tasks prepare `agent-workspace/tasks/ID` on their fixed Runtime. Correspondence
uses the recipient's directory and independent history. Directories organize
files and do not add an OS security boundary.

`POST /api/v1/turn/steer` binds `request_id`, `user_message_id` and `text` to the
original active turn. The pending input and visible user message commit together.
A steering message has `steering: true`, `accepted` status while queued and
`complete` status once its IDs are durably incorporated in the Agent checkpoint.
An exact retry returns the original receipt and never retargets another turn.

`POST /api/v1/turn/control` binds `user_message_id` to `pause` or `resume`.
`turn.paused` is persisted and streamed in snapshots. Pausing waits at safe model
boundaries; in-flight work settles normally. It neither recreates a turn nor
relaxes tool or machine permissions.

## Prompt sources and resource preparation

Main's built-in delegation prompt is embedded from
[`main_prompt.txt`](../internal/assistant/assets/main_prompt.txt). Updating it
requires explicit authorization and rebuilding; a custom `system_prompt`
replaces the default `pkg/agent` prompt.

Before a local Task first executes, the node prepares the selected current global
Skills and source assistant's plugin Profile. Missing resources fail before model
sampling. The executor records prepared content and digests once for that run;
allowance resumes reuse them. Queued Tasks read current sources on first
preparation. Project Skills and AGENTS.md come from the fixed execution workspace.
Global Skill metadata includes ordinary file paths; full instructions load lazily.
See [Plugin packages](plugins.md) for tool bundles.

## Routing rules

Each conversational assistant owns AGENTS.md text in its database, with an
opaque revision that changes on every save. It is part of the assistant's state,
independent of execution target. Main contexts receive their own rules;
workers receive task briefs and applicable project directory instructions, not
assistant or global Agent-home rules. 
The built-in dispatch instructions and fresh configured Provider/model catalog remain
injected independently. A custom system prompt still receives file instructions.

Explicit task choices take precedence over saved rules. Local `machine_id`
selections must identify the current node. Remote collaboration uses `link_id`
and an optional assistant `target`; the receiver resolves its default once and
owns execution. See [Task lifecycle](task-coordination.md#linked-execution).

The fixed Runtime and workspace are recorded for the run; reruns create new
Tasks. Each tool uses current machine execution authorization. Labels never grant
capabilities or implicitly move work; unsupported execution fails visibly.
Rules guide autonomous model selection from configured Providers; they cannot
enable an unconfigured service or grant execution authority. `create_task` retains
`selection_reason`; dedicated preference tools and `preference_revision` have
been removed with user agreement. Existing tasks retain configuration snapshots.

Use the persistent Chat assistant pane to select assistants and its + action to
create one. Creation selects the owning node's default Provider and that
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
only explicitly selected global skills. Each node supplies its own selected global resources. Every bundle contains UTF-8 SKILL.md
with YAML frontmatter and optional scripts, references and assets. Only metadata
is advertised during discovery; full instructions are read on demand.

Runtime supplies ordered roots. Project directories are discovered from the assigned workspace upwards, stopping at the nearest repository root, or cwd when no repository exists. The nearest project wins, then global user skills, then builtins. Names in the included builtin catalog are reserved: user/project attempts to shadow them are diagnosed and ignored. Bundle names match their directory names in authored and packaged skills.

Malformed bundles produce diagnostics and are omitted. Symlink directories and SKILL.md symlinks are skipped. Supporting assets inside a bundle are not recursively discovered as additional skills. Catalog ordering and conflict resolution are deterministic.

## Unified execution view and permissions

Main execution advertises actual user/builtin source paths. Tasks materialize
selected local bundles beneath `.pot/control-resources/<digest>/` in their
fixed workspace. The digest identifies prepared content, not a user-requested
version. Injected paths are absolute destination-local paths; no mount or HOME
translation is performed. Project Skills remain independently discovered.

Skills grant no execution authority. Instructions and Skills use the same local
filesystem rules for execution on the owning node. Runtime asset
paths are set by host composition and remain independent of guest management.
See [Permissions](permissions.md) and [Executor](executor.md).

## Injection and task selection

`core.ExecutionContext.SkillResources` carries ordered SkillRoot entries, reserved names and optional selections. `pkg/agent` discovers supplied filesystem roots without interpreting product roles or machine discovery. Callers provide `core.SkillResources` for Main and Task contexts.

Task creation accepts optional `skills: ["name", ...]` selecting global user or builtin bundles only. Omitted or empty lists load no global skills. Project skills are always discovered independently and are not filtered by this list. Selections persist, but each new execution prepares current contents before starting the Agent. Contents remain stable for that run; Control does not retain per-task bundle versions. Unavailable selected global skills fail visibly when preparing a turn, even if a project skill has that name.

Node-owned [extension settings](extensions.md) filter disabled bundles before Main discovery and reject disabled explicit global Task selections. Builtin names remain reserved even when disabled.

Catalogs reload each execution and use the classified `skills` history section. Unchanged rendered metadata is not appended again; changes replace the complete section through a new message. Removal appends an explicit revocation. Notification executions load the same catalog as user chat; `pkg/agent` does not advertise skills if the final tools lack exec_command. Chat `/skill:name` references explicitly select global Skills for new tasks delegated from that user message; they are also visible to the assistant as user instructions. Main project discovery reflects its actual execution directory; active tasks retain their recorded execution location.

## Creation, installation and validation

Builtin skill-creator describes format, concise instruction design, supporting resources and validation. Builtin skill-installer handles local/GitHub installation, updates, listing and removal. Their instructions expose only the unified skill path, project paths and usable commands; they do not describe source storage or mounts. Tasks can read these same instructions, but readonly global mounts prevent global mutations.

The creator's scripts/quick_validate.py validates YAML metadata and scaffolds, and checks ordinary resource files and inline local Markdown links. Authored bundles require explicit names and matching directory names. Standard license/metadata/allowed-tools fields may be preserved but do not grant tool permissions. Shared format fixtures exercise Go discovery and Python validation. Validation does not execute scripts or establish behavioral quality.

The installer uses the adjacent creator validator. It accepts a local bundle, GitHub repo/path/ref or tree URL. Downloads use only credentials available in the current execution target. Archive extraction rejects traversal and special files. Installation preserves resource contents and executable permissions, normalizes readable permissions, validates before publishing, and refuses replacement without --replace. Ordinary publication failures restore the previous directory; crash recovery and concurrent installers are not transactional.

apply_patch remains workspace-scoped. Global installation/update is performed on the owning node. Tasks cannot modify supplied global bundles. Use injected bundle paths for helper scripts, and `--dest PROJECT/.agents/skills` for project installation.

## Message references, Skills and assistant mentions

Selected Chat excerpts use structured message IDs and quote data, independently
of the slash selectors below. Main can read referenced original messages by ID
after compaction. See [Chat references](chat-references.md).

App and Web autocomplete standalone `/` and `@` words at the caret using
authenticated, assistant-scoped `/api/v1/completions` metadata. `/` offers only
the assistant's current global Skill catalog. Project Skills are discovered by
the task and are not offered as global selectors. Skills have no icons.
`@` offers the other local assistants and the connected node directories, with
saved avatars, names and sources. Cached offline assistants are visible but
cannot be selected. Opening the picker reads snapshots without connecting to peers.

Selections appear as removable composer chips. They remain ordinary message
text internally: `/skill:skill-creator` and
`[@Display name](pot-assistant:NODE_ID/AGENT_ID)`. The selected prefix is followed
by a newline and the typed message. Full assistant identity survives duplicate
display names, drafts, retries and history; mention links are displayed as
non-navigable names. They identify collaborators to the current assistant and
never change the receiving conversation or automatically send peer messages.
The existing `list_assistants` and `send_message` tools handle collaboration.
There are no added prompt instructions or model-side lookup tools.

Desktop arrows select candidates and Enter inserts one; Escape dismisses the
picker. Mobile Enter remains a newline and candidates are selected by touch.
IME candidate confirmation never selects or sends. URLs, paths and email
addresses do not open the pickers. The menus, chips and hints follow UI locale.

Machine references `/machine:RUNTIME_ID` and `/tag:desktop` are not autocomplete
candidates. Use spaces around textual tokens; backtick-quoted examples are not
selectors.

A message may specify one machine and multiple labels (AND). A machine plus labels
must satisfy both; conflicting explicit tool destinations are rejected. Absent
matching machines is an explicit routing error. Offline or incapable targets are
not silently replaced. Only the current user turn constrains new delegated tasks;
previous messages, system task reports, existing tasks and scheduled occurrences
never inherit new selections. Selected Skills are persisted with the new task.
Global/project Skill discovery and project AGENTS.md still use the existing rules.

Runtime labels describe the local machine and never implicitly place Tasks.
New roots carry their OS label. Owner edits persist across restarts; labels use
lowercase ASCII letters, digits, hyphens and underscores.

## Recovering a stopped Main turn

After a crash or uncertain termination, Main retains the original stopped turn
until its execution records are reconciled. Chat exposes **Continue task** for
the latest failed, cancelled or recovery-required turn, including after restart.
Interrupted task-result summaries expose **Restore conversation**, using their
original notification execution with its configured assistant tools instead of rerunning completed background
tasks. The `summary` flag distinguishes this presentation.
The snapshot's `continuation` carries the original `user_message_id`, exact
`attempt_id`, and `recovery_required`. `POST /api/v1/turn/continue` accepts these
two identities plus a fresh `request_id`; browser and native clients use the
same owner-scoped endpoint. A stale attempt cannot continue a later interruption.
Exact command retries retain their receipt even if the original acknowledgment
was lost. New composer drafts and uploads remain separate from continuation.

Control first reconciles an interrupted execution's original receipts and
cleanup, then resumes its saved model context under the original input identity.
Confirmed tool results remain in history; never-started calls are closed without
execution. Failed and cancelled turns resume directly once cleanup is confirmed.
Continuation neither recreates the original tools nor resets request/token caps.
Queued user input keeps priority if recovery has already advanced the conversation.
Unknown tool effects or unproven process shutdowns produce a localized explanation
with diagnostic details; they cannot be silently retried through a new call ID.

The private/native owner endpoint
`POST /api/v1/turn/recovery?assistant_id=ID` accepts `action: "inspect"` to expose
the original `user_message_id`, receipt states and unresolved reasons. Reconcile
requires `action: "reconcile"` and that exact `user_message_id`. Browser
publication excludes this route, and clients cannot supply results or cleanup
acknowledgments.

Control reads original operation receipts and original execution receipts.
Actual original cleanup is recorded before terminal acknowledgment; otherwise
cleanup must be independently proven. A safe stopped boundary with no uncertain
effects can be closed, and proven tool results enter the original checkpoint
once. Remaining never-started calls are closed as interrupted. Recovery does not
replay accepted input, model samples or tools. Interrupted model output is
discarded once original operation receipts and cleanup are confirmed: tools from
an uncommitted response have never been dispatched. Unknown tool outcomes and
previous-boot Host effects without cleanup proof remain fenced. Successful
reconciliation clears only the original execution and allowance wait and retains
the original input as a continuation candidate; exact
retries return its retained completion receipt without affecting a later turn.

A model transport error, truncated stream or cancellation during a live turn
closes the stream and saves the last committed context without an active model
marker. Failed turns can use the existing retry action with the original input
and confirmed tool receipts; each new sample gets a new request ID. Partial
model text and proposed tool calls are not replayed. Cleanup failures and failed
checkpoint writes still require recovery. Provider usage for an interrupted
request remains unknown/conservatively reserved independently of retryability.
