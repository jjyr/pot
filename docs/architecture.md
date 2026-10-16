# Architecture

The current design uses one independently deployed Control for a user's remote
machines. Execution machines join this Control and accept its commands.
Issue [#39](https://github.com/jjyr/pot/issues/39) owns the phase checklist. Validation reports record tested scope and limitations.

```text
App UI + local Control + local Runner (in-process lifecycle)
                 |
          local / remote icon
            /           \
     local Control    remote Control
                      assistants, rules, Tasks, schedules
                      model configuration, budgets, approvals
                         |                |
                  outward connection  outward connection
                     Machine A           Machine B
                     Host/Sandbox        Host/Sandbox
```

## Responsibility and lifecycle

`pot serve` starts neutral infrastructure. Callers create assistants, Host execution
and Sandbox definitions through `/api/v1/runtime/resources`. Creating an assistant
does not request a model or enable Host execution. Creating a Sandbox does not start
its instance. Installed Providers describe supported operations, not permissions.

The App owns its embedded Runtime composition (local Control and Runner) and
assembles Control configuration without creating an assistant. Enabling standalone
Control likewise exposes configuration before assistant registration. Fresh
registries are empty. Saving AI setup persists the default configuration; a separate
explicit creation request registers the first assistant (prefilled as Pot in UI).
The root Service holds setup/default configuration before its first conversational
identity is registered; it cannot accept chat messages until that registration.
Restoring configuration and UI-created identities does not depend on an App startup
resource-creation request. Embedded Control cannot open the remote Control listener, including when a data root
contains a persisted Control listener intent. A standalone Runtime may explicitly
enable Control. A joined execution Runtime cannot become Control simultaneously or create assistants.
A local Control with AI configuration or assistants cannot join as a Runner; use a separate data root.

Control owns remote conversations, assistant instructions, Tasks, schedules,
global plugin and skill sources, model configuration, budgets and approvals. Chat
assistants remain on their local or remote Control. The approved remote
task design starts a new Task Agent on the selected execution Runtime, alongside
its Host or Sandbox, and reads its durable result after completion.
Agent and Task retain separate identities. An execution target is only a value
identifying a Runtime and its Host or Sandbox, not a separately managed resource.
The Task Agent runs on that same Runtime. No live Agent relocation or three-node
placement is included. Execution nodes do not need
assistant databases or provider credentials.

Remote Task Agent loops run on the execution Runtime. Control-selected global
Skill and plugin names travel with the launch; the execution Runtime synchronizes
their current contents through the authenticated outward connection before starting
the Agent. Named remote plugin calls execute the supplied pinned bundle
in Host/Sandbox through the existing owner approval and receipt path. The Task
Agent uses outward signed Control service calls for models, policy, approvals and
guidance; provider secrets remain at Control. Main assistants stay at their owner.

All assistant replies, local Task runs and remote Task runs enter the destination
Runtime's shared execution manager. Control decides placement and fixed
configuration; Runtime performs the actual Agent invocation, with a common queue,
concurrency limit, cancellation and metadata trace. Assistant/Task adapters retain
conversation and durable run semantics. Remote dispatch waiting consumes no local
Agent slot. Runtime composition and its execution kernel are separate crates to
keep business components independent of composition. See [Runtime](../crates/runtime/README.md)
for lifecycle and failure semantics.

The App owns its local Control connection and can also connect to a remote Control.
The shared UI lists assistants from both Spaces with ownership badges. Opening
an assistant selects its conversation Space; Tasks and overall Usage aggregate
both Spaces. This selection does not affect background Assistant message relay. App alone runs
`pot-relay`, reusing those connections to transfer messages between independent
Controls. Same-Space delivery stays within Control. Assistant communication has
no special handoff or task-migration operation: agents coordinate through ordinary
messages. Protocol types live in `pot-protocol::message`, durable mailboxes in
`pot-assistant::message`. See [Assistant messages](assistant-messages.md) for the
single message contract, processing isolation and recovery rules.

`pot-ui::application::Shell` implements the complete product UI for App and Web.
`pot-avatar` supplies portable Pot raster composition and reply-motion frames;
the shared UI owns image caching and live-reply detection.
App supplies native input, system services and local/remote client transports;
Web supplies browser input, URL history and its web-ui-server transport. Neither
host implements a separate set of product pages. Web remains bound to its serving
Control. See [Shared UI](shared-ui.md) for presentation and adapter boundaries.
Data, drafts, event cursors and cached results are scoped
by mode/Runtime/owner. Remote failures never select local execution. Host/Sandbox resource creation
UI is deferred; assistant creation is available from Chat. Machines shows lightweight status cards, expandable Host/Sandbox rows and
Tasks filtered by the exact execution target. Machine settings provide shared-environment start/stop and resource limits.
Advanced native owner APIs provide rebuild and mappings. File delivery uses Chat;
Machines and Task pages do not expose artifact-download lists. File delivery uses clickable Chat links:
Control relays a task file from its execution machine to the current client; App
asks for a save location and Web uses browser downloads. Links use
`pot-file://TASK_UUID/encoded/relative/path`; owner-scoped reads use the task's
immutable execution target, including finished tasks. Reads are bounded to 192 KiB
chunks and 1 GiB files; missing files, offline machines and detected changes fail.
No new model-visible file tool is required. Completion summaries include download
links only when the task has files to deliver; ordinary results need no download.
A desktop entry requires an implemented client transport (currently local App only).
The web-ui-server component serves Web assets, browser authentication and APIs.
It requires `pot serve --ui --control URL`, a server-held
Control credential and explicit browser listener settings. It has no local/remote
switch. App no longer hosts or configures a browser endpoint. Remote desktop
transport is deferred until its connection design is agreed and is outside the
current #39 completion scope. Control remains responsible for session identity;
neither desktop data relay through Control nor direct access is selected. Existing
local desktop support remains available. Web notifications read final replies from Control using server-held credentials,
with per-assistant durable cursors. One browser subscription covers all assistants;
clicking opens the corresponding owner. Synthetic worker reports do not notify
separately. See #39 for acceptance status.

## Membership and transport

Only standalone Control opens the connection listener. Operators choose HTTP
(no certificate/key) or HTTPS (both certificate and key). Web, App and member
connection URLs independently select their transport; deployment may protect
HTTP with Tailscale or another deployment-managed network layer. web-ui-server's
public Origin and listener TLS are independent settings. Business authorization,
signatures and receipts do not depend on the chosen transport. Operators create a
short-lived single-use invitation. An execution machine confirms the Control's
stable identity and joins it. Exact retries keep the request identity; conflicting
retries fail. Persisted credentials and private keys never appear in directory
projections. Native client credentials are separate from member credentials.

The execution machine polls outward over authenticated HTTP or HTTPS. Control queues signed
commands; the machine verifies the bound Control, recipient and time window, then
returns a signed reply bound to the command digest. No execution node needs an
inbound execution listener. Responses are bounded and redirects are forbidden.

An execution Runtime binds one Control. Joining allows that Control to operate all
configured Host/Sandbox resources. There are no discovery, execution, management,
artifact or desktop grants, expiry controls or grant/revoke APIs. Execution policy is machine-wide: Control selects the default execution mode and checks Host operations individually or allows them according to the configured machine policy. There is no assistant-to-machine authorization relationship.
Instruction edits retain fresh owner review. See [machine permissions](permissions.md).

Removing a member invalidates its connection credential. Leaving fences local
leases. A new invitation creates a fresh session; old enrollment cannot silently
reactivate removed membership. Stable signing identities and durable operation
receipts are retained. 

## Execution targets and instructions

Model history uses classified, append-only context sections. State updates never
rewrite previous messages or accepted user text. Compaction installs current
injections, a continuation summary and the last real user message; retained
sections establish the new comparison baseline independently of provider caches.
See [Agent context](agent-context.md#classified-messages-and-append-only-context).

`ExecutionTarget` identifies the execution Runtime and its Host or Sandbox.
The `execution_target` field records this value in Task execution information,
assignments and remote launches. Task and schedule creation select `machine_id`:

```json
{"runtime_id":"RUNTIME-ID","target":{"kind":"host"}}
{"runtime_id":"RUNTIME-ID","target":{"kind":"sandbox","sandbox_id":"build"}}
```

Remote is a relationship to the caller, not a target kind. Host has execution sessions and
working directories; VM instance lifecycle belongs only to Sandbox. Default dispatch
is relative to the Agent Runtime, regardless of which machine renders its UI.
An explicit machine selection must satisfy message routing constraints. Its current
policy determines Host or Sandbox at dispatch. Running contexts remain fixed. Unsupported
Assignment schemas and obsolete selector fields are rejected without migration.
Restart does not rebind tasks or replay effects.
App, Control and execution Runtime use the same API revision; upgrade them together.

Each assistant's AGENTS.md is stored in its database and follows its identity.
`read_agent_instructions({})` and `update_agent_instructions({text})` are self-scoped;
updates request approval. Server-side approval baselines protect intervening user
edits. Main also reads public/project AGENTS.md from the actual working directory.
Task Agents have no assistant-level instructions; they use the task brief and the
execution destination's project rules. Task launches contain skill/plugin names
and model/execution settings, never Skill files or plugin bundles (including
inside the model configuration). After acceptance, the destination Runtime
queries the owning Control by name through its authenticated, run-bound resource
service. It synchronizes the current bundles before constructing tools or sampling
the model. No resource version is requested. Skills come from Control's current
global directories; plugin implementations come from the owning assistant's current
source Profile, limited to the task's selected plugin names. Missing/disabled
resources, unavailable profiles and failed synchronization stop execution.

Each new task execution synchronizes current contents. Ended tasks cannot resume;
reruns create new Tasks and synchronize again. Resource contents remain stable
within one run; an in-flight run does not refresh. Control does not persist a
per-task resource archive. Destination caches use content digests for verification,
retain executable bits, expose files read-only, and keep only the workspace's
active materialization. The execution lease binds the synchronized digest outside
the task workspace; prepare/execute reject changed caches. Digests are internal
integrity checks, not task resource-version selectors.

Independent local tasks use the same pre-execution materialization against their
owning Runtime. Project AGENTS.md and project Skills continue to be discovered
from the actual workspace independently. Omitted/empty task skills load no global
Skills. Agent context contains Skill metadata and ordinary absolute file paths;
cat, scripts and supporting relative paths continue to work without a new tool.
Plugins are synchronized before their tool schemas are registered, so definitions
and implementations come from the same prepared bundle. Plugin sessions remain
run-scoped. Internal plugin envelopes cannot be invoked through public named-tool
RPCs. Resource synchronization currently transfers up to 512 KiB / 2048 Skill
files per response; large-bundle chunked delivery remains unqualified.

Control specifies the exact model, execution target and tool configuration.
The execution Runtime runs that configuration or returns an explicit unsupported
configuration/dependency error. It must not replace models, plugins or targets,
or silently hide tools according to node-local settings.
Resource updates affect later executions, while already running tasks keep their prepared contents. Missing bundles
fail explicitly rather than falling back to a node-local catalog. Standalone App
local mode retains its independent local resources and state.

## Remote Task Agent launch and results

```text
App / Web -> Control
              chat assistant, Task state, global bundles, model gateway, approvals
                 |
                 | durable launch: task/run identity, brief, configuration version,
                 | immutable Host/Sandbox target
                 v
              Machine B
                new Task Agent -> Host/Sandbox on Machine B
                  Control-supplied globals + execution-project rules/skills
                 |
                 | persist terminal result; notify Control
                 v
              Control reads result -> Task view / optional Chat summary
                                        optional file links when files exist
```

A launch starts a task-scoped Agent, not a transferred assistant session. Control
persists launch intent before delivery; the destination persists acceptance,
run identity and terminal results. Exact duplicate launches return the same run
receipt, never start another Agent. During execution only necessary status,
guidance/cancellation, approvals and accountable usage are exchanged. Full live
Agent context replication to Control is not required. Destination-local recovery
records preserve execution evidence; restart/timeout does not imply rerunning a
task. Control reconciles the accepted run and fetches its result after completion,
including when a completion notification was lost.

Task Agents request models through Control's gateway. Provider credentials,
budget decisions and usage accounting remain there. Execution plugins
run on the assigned Host or Sandbox from Control-supplied definitions/bundles;
this does not give the execution node ownership of the global catalog. Model
behavior approval remains separate from machine membership.

UI remains the existing Chat, Tasks and Machines. Task details need only status,
machine and Host/Sandbox location; the co-located Agent does not need a separate
placement selector or Runner management page. Technical resource operations stay
API/Chat based, with concrete model-visible changes requiring separate agreement.

## Failure and receipt rules

Execution leases bind Control session, owner, run, generation, target, destination
boot and deadline. A lease is a liveness fence, not a configurable privilege grant.
Lost connectivity stops new model and tool actions. Expired leases drain execution
sessions without affecting another owner's scope. After all issued tool and Control
service receipts have settled and been read, the same live Task Agent run can
receive a new lease for the unchanged Control session/configuration/target/boot.
The old lease stays fenced; new execution scopes cannot reuse old sessions. Unknown
results block continuation and are never replayed. Execution Runtime reboot does
not restart the Agent: known outcomes and old scopes are reconciled, the Task is
interrupted, and explicit `guide_task` starts a new run using the destination's
saved context. Unresolved outcomes keep replacement blocked.

Control restart preserves the accepted task/run identity and queries its existing
launch/result receipt. It does not send another launch. Both ends persist service
intents/results, including model response bytes, before acknowledgment. Lost model
or approval replies are read from the original receipt. A Control service whose
outcome became unknown after restart blocks automatic continuation. Explicit
`pot_token_limit` refusal occurs before contacting the provider: the same live
Agent waits for allowance, then starts a new accounted request attempt. Other
provider/send failures never trigger automatic resampling. Model replies are
bounded to 16 MiB and the signed response envelope to 24 MiB; oversized responses
fail explicitly. Launches are bounded to 768 KiB and current global bundles to
512 KiB. Acceptance windows must close before absence is considered final.

Callers persist intent before sending effects. Destinations persist acceptance and
terminal outcomes. Transport timeout, reply loss and restart never cause automatic
replay or fallback. Query the original operation receipt. A receipt with uncertain
outcome blocks conflicting replacement work. Management absence is terminal only
after its acceptance window closes, preventing in-flight acceptance races.

Remote Sandbox management supports existing start/stop/recover/update/delete. Create
and select remain local to the destination. Active executions, unresolved receipts,
and references from all destination assistants' Tasks/schedules protect deletion.
Retired Sandbox IDs cannot be silently reused. Host HTTPS, actual Linux Podman
plugins, independent Linux Control/macOS Host processes and native App/Web
qualification are recorded in the [production report](validation/task-agent-production-20261011.md)
and [client report](validation/control-cross-machine-ui-20261011.md). Three-node
Agent/Host/Sandbox placement remains deferred beyond this release.

## Implementation boundaries

Runtime assembles components, manages their lifetime and owns the shared Agent
execution manager. Server hosts native
APIs, outward Control transport and the explicitly enabled web-ui-server. Control coordinates
Tasks/schedules, approval and model policy through component APIs. Assistant owns
identities, SQLite AGENTS.md records, conversation Threads, the chat driver and
durable Assistant mailboxes. Relay owns only cross-Control message transport
coordination and is currently composed by App.
Runner owns destination Task run journals and context adaptation. Assistant and
Task adapters submit to the Runtime execution manager, which invokes pot-agent,
the injected model/tool engine. Model protocol encoding is a pure shared library;
Gateway owns Provider transport and configuration, Usage owns accounting, and
Secrets owns encrypted values. These are libraries in the same process, not extra
services to deploy. Local Agent service calls stay in memory; network adapters
are only required across machines.

Provider definitions and creation defaults are shared per Control; each assistant
owns its concrete model selection. Assistants route Tasks autonomously using
their instructions and available models, without routing approval or model
allowlists. [Model configuration](model-directory.md) defines ownership and
defaults; [Task coordination](task-coordination.md) defines task selection.

Control also owns the shared offline [model directory](model-directory.md): a
bundled seed, persistent cache and independent 24-hour refresh task. App and Web
consume the same provider/model/logo projection and nullable parameter overrides.
Control resolves local model facts into concrete execution specs; Agent and
Runner never fetch catalog data, and running/paused specs remain fixed.

Provider metadata and secrets use Control's SQLite database. Provider snapshots
contain credential references, never plaintext keys. Secrets use AES-256-GCM;
the master key is external to SQLite (macOS Keychain or `~/.pot/secrets/master.key` on other platforms,
with an explicit owner-only key file override). Assistant rules remain SQLite records scoped to the
assistant, while project AGENTS.md remains filesystem based. Runner receives no
Provider credentials. General task-secret distribution is a future protocol;
there is no implicit secret export endpoint. See [secrets](secrets.md).

There are no legacy resource adoption, peer-identity upgrades, assistant-file
imports or plaintext Provider JSON readers. Unsupported historical layouts are
not converted. Existing files are not deleted on startup.
See [backend crates](backend-crates.md) and [Runtime](../crates/runtime/README.md).

A stopped Sandbox does not stop Chat. Shared Sandbox task directories organize
work; they do not establish mutual task isolation. Cancellation settles the target
scope, without stopping unrelated tasks or the shared VM. Unknown outcomes require
reconciliation before new conflicting operations. Restart restores current-schema data; obsolete presets do not create resources.

## Machine policy and execution records

The machine policy is the execution default. Tasks record actual execution
information at dispatch, and reruns create new Tasks using current policy. Active
executions retain their location; Host operations use current authorization.

Control owns routing, machine policy and exact Host approval. Runtime owns one
shared Linux development Sandbox per machine, execution scopes, run leases and
cleanup. Providers preserve the writable environment on stop and destroy it only
on explicit owner rebuild/delete. Tasks share software/caches and the filesystem;
per-Task directories are organization rather than mutual security isolation.
Task termination closes its own sessions and processes, leaving the environment
and other executions alive. Installed Rust tools use shared Cargo/Rustup paths.

Sandbox GUI remains an owner-viewed desktop with the existing viewer takeover
rules. Native Host/Sandbox desktop automation is not implemented. The built-in
Computer Use plugin controls independent headless Chromium sessions; sharing a
container does not require a machine-wide browser lock. See
[execution lifecycle](executor.md#shared-machine-execution-environment) and
[Computer Use](computer-use.md).
