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

The App owns its embedded Runtime composition (local Control and Runner) and creates its default assistant through the
resource API. Embedded Control cannot open the remote Control listener, including when a data root
contains a persisted Control listener intent. A standalone Runtime may explicitly
enable Control. A joined execution Runtime cannot become Control simultaneously or create assistants.
A local Control with assistants cannot join as a Runner; use a separate data root.

Control owns remote conversations, assistant instructions, Tasks, schedules,
global plugins and skills, model configuration, budgets and approvals. Chat
assistants remain on their local or remote Control. The approved remote
task design starts a new Task Agent on the selected execution Runtime, alongside
its Host/Sandbox environment, and reads its durable result after completion.
Agent, Task and Environment retain separate identities, but the first release
requires Agent and Environment to belong to the same execution Runtime. No live
Agent relocation or three-node placement is included. Execution nodes do not need
assistant databases or provider credentials.

Remote Task Agent loops run on the execution Runtime. Control-selected global
Skills and pinned plugin configuration are delivered through the authenticated
outward connection. Named remote plugin calls execute the supplied pinned bundle
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

The App connects to one Control as a client. The accepted UI has one icon to switch
between local and remote. Data, drafts, event cursors and cached results are scoped
by mode/Runtime/owner. Remote failures never select local execution. Resource creation
UI is deferred. Machines shows lightweight status cards, expandable environments and
Tasks filtered by the exact execution target. Technical management and file transfers
belong in ordinary Chat; there is no assistant-management shortcut or Machines/Task
artifact-download UI. File delivery uses clickable Chat links:
Control relays a task file from its execution machine to the current client; App
asks for a save location and Web uses browser downloads. Links use
`pot-file://TASK_UUID/encoded/relative/path`; owner-scoped reads use the task's
immutable environment, including finished tasks. Reads are bounded to 192 KiB
chunks and 1 GiB files; missing files, offline machines and detected changes fail.
No new model-visible file tool is required. Completion summaries include download
links only when the task has files to deliver; ordinary results need no download.
A desktop entry requires an implemented client transport (currently local App only).
Web UI requires `pot serve --ui --control URL`, a server-held
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

Only standalone Control opens the HTTPS connection listener. Operators create a
short-lived single-use invitation. An execution machine confirms the Control's
stable identity and joins it. Exact retries keep the request identity; conflicting
retries fail. Persisted credentials and private keys never appear in directory
projections. Native client credentials are separate from member credentials.

The execution machine polls outward over authenticated HTTPS. Control queues signed
commands; the machine verifies the bound Control, recipient and time window, then
returns a signed reply bound to the command digest. No execution node needs an
inbound execution listener. Responses are bounded and redirects are forbidden.

An execution Runtime binds one Control. Joining allows that Control to operate all
configured Host/Sandbox resources. There are no discovery, execution, management,
artifact or desktop grants, expiry controls or grant/revoke APIs. Model operation
approval remains independent: assistants still request approval for protected
behavior and edits to their own instructions.

Removing a member invalidates its connection credential. Leaving fences local
leases. A new invitation creates a fresh session; old enrollment cannot silently
reactivate removed membership. Stable signing identities and durable operation
receipts are retained. 

## Execution targets and instructions

Environment references distinguish the owning Runtime and actual target:

```json
{"runtime_id":"RUNTIME-ID","target":{"kind":"host"}}
{"runtime_id":"RUNTIME-ID","target":{"kind":"sandbox","sandbox_id":"build"}}
```

Remote is a relationship, not an Environment kind. Host has execution sessions and
working directories; VM instance lifecycle belongs only to Sandbox. Default dispatch
is relative to the Agent Runtime, regardless of which machine renders its UI.
An explicit task target takes precedence over routing instructions and local defaults.

Each assistant's AGENTS.md is stored in its database and follows its identity.
`read_agent_instructions({})` and `update_agent_instructions({text})` are self-scoped;
updates request approval. Server-side approval baselines protect intervening user
edits. Main also reads public/project AGENTS.md from the actual working directory.
Task Agents have no assistant-level instructions; they use the task brief and the
execution destination's project rules. In Control-connected execution, global
plugins, skills and other global Agent resources are selected and versioned by
Control. Execution nodes receive the selected bundles and may cache immutable
versions; they do not discover global resources from their own home directories
or packaged catalogs. Project AGENTS.md and project skills continue to be read
from the actual execution workspace with their existing scope/precedence rules.
Control snapshots global bundles before the first remote context and persists
the snapshot with the task; follow-up turns and restart reuse it. Cache files
retain executable bits and are exposed read-only; digest/contents are verified
before reuse. Snapshots also bind Shell settings and the model observation mode.
The execution lease stores the accepted resource digest outside the task workspace;
prepare/execute reject a different cache revision, even if its own digest is valid.
Changing configuration on an accepted run fails. Model-visible tool definitions
remain Control-selected; the destination capability list does not filter them. Plugin sessions are scoped to
the accepted run; a new run cannot reuse a previous run's session ID.
Internal plugin envelopes are created only from supplied enabled bundles and cannot
be invoked through the public named-tool RPC. The current inline snapshot is bounded to 512 KiB and 2048 files
within the existing 1 MiB transport limit. Oversized or symlinked bundles fail
explicitly; large-bundle chunked delivery remains unqualified.
Control specifies the exact model, execution environment and tool configuration.
The execution Runtime runs that configuration or returns an explicit unsupported
configuration/dependency error. It must not replace models, plugins or targets,
or silently hide tools according to node-local settings.
Control updates affect later launches, not already bound tasks. Missing bundles
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
budget decisions and usage accounting remain there. Environment-dependent plugins
execute at the assigned environment from Control-supplied definitions/bundles;
this does not give the execution node ownership of the global catalog. Model
behavior approval remains separate from machine membership.

UI remains the existing Chat, Tasks and Machines. Task details need only state
and execution machine/environment; the co-located Agent does not need a separate
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
Agent/Environment placement remains deferred beyond this release.

## Implementation boundaries

Runtime assembles components, manages their lifetime and owns the shared Agent
execution manager. Server hosts native
APIs, outward Control transport and explicit Web hosting. Control coordinates
Tasks/schedules, approval and model policy through component APIs. Assistant owns
identities, SQLite AGENTS.md records, conversation Threads and the chat driver.
Runner owns destination Task run journals and context adaptation. Assistant and
Task adapters submit to the Runtime execution manager, which invokes pot-agent,
the injected model/tool engine. Model protocol encoding is a pure shared library;
Gateway owns Provider transport and configuration, Usage owns accounting, and
Secrets owns encrypted values. These are libraries in the same process, not extra
services to deploy. Local Agent service calls stay in memory; network adapters
are only required across machines.

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
