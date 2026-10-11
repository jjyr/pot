# Architecture

The current design uses one independently deployed Control for a user's remote
machines. Execution machines join this Control and accept its commands.
Issue [#39](https://github.com/jjyr/pot/issues/39) owns the phase checklist. Validation reports record tested scope and limitations.

```text
App + local Runtime (start and exit together)
                 |
          local / remote icon
            /           \
     local owners     one Control
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

The App owns its embedded Runtime and creates its default assistant through the
resource API. Embedded Runtime cannot enable Control, including when a data root
contains a persisted Control listener intent. A standalone Runtime may explicitly
enable Control. A joined execution Runtime cannot become Control simultaneously.

Control owns remote conversations, assistant instructions, Tasks, schedules,
global plugins and skills, model configuration, budgets and approvals. Chat
assistants remain on their owning local Runtime or Control. The approved remote
task design starts a new Task Agent on the selected execution Runtime, alongside
its Host/Sandbox environment, and reads its durable result after completion.
Agent, Task and Environment retain separate identities, but the first release
requires Agent and Environment to belong to the same execution Runtime. No live
Agent relocation or three-node placement is included. Execution nodes do not need
assistant databases or provider credentials.

Currently Task Agent loops still run on the task-owning Runtime and forward
environment operations. Control-selected global Skills and pinned plugin
configuration are now delivered through the authenticated outward connection.
Named remote plugin calls now execute the supplied pinned bundle in Host/Sandbox
through the existing owner approval and receipt path. Remote Task Agent startup
remains implementation work in #39; the Agent loop still runs at Control.

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
switch. App no longer hosts or configures a browser endpoint. Remote desktop and
remote notification transport remain pending; see #39 for implementation status.

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

## Remote Task Agent launch and results (approved, pending implementation)

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
sessions without affecting another owner's scope.

Callers persist intent before sending effects. Destinations persist acceptance and
terminal outcomes. Transport timeout, reply loss and restart never cause automatic
replay or fallback. Query the original operation receipt. A receipt with uncertain
outcome blocks conflicting replacement work. Management absence is terminal only
after its acceptance window closes, preventing in-flight acceptance races.

Remote Sandbox management supports existing start/stop/recover/update/delete. Create
and select remain local to the destination. Active executions, unresolved receipts,
and references from all destination assistants' Tasks/schedules protect deletion.
Retired Sandbox IDs cannot be silently reused. Remote plugin Host TLS fixtures
are qualified; real Sandbox plugin/platform acceptance, desktop transport,
co-located remote Task Agent startup and actual
multi-machine qualification remain tracked in #39. Three-node Agent/Environment
placement is deferred beyond this release; successful Host tests do not establish
the pending features.

## Implementation boundaries

Server hosts native APIs, outward Control transport and explicit Web hosting.
Runtime owns assistants, task/schedule state, model approvals and environment routing.
pot-agent executes model/tool loops using injected context and services; it has no
implicit executor. Storage persists owner Threads and product records; Gateway
handles model protocols and credentials. HostEnvironment supplies production Host
execution. Sandbox Manager owns definitions, provider registration, execution
scopes, lifecycle, receipts and recovery. Providers implement platform mechanics.
See [backend crates](backend-crates.md) and the [Runtime contract](../crates/runtime/README.md).

A stopped Sandbox does not stop Chat. Shared Sandbox task directories organize
work; they do not establish mutual task isolation. Cancellation settles the target
scope, without stopping unrelated tasks or the shared VM. Unknown outcomes require
reconciliation before new conflicting operations. Restart preserves current data
through bounded upgrades; obsolete presets do not create resources.
