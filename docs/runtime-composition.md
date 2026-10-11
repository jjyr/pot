# Control and composable runtimes

The current design uses one independently deployed Control for a user's remote
machines. It replaces peer-to-peer pairing and per-environment machine grants.
Issue [#39](https://github.com/jjyr/pot/issues/39) owns the phase checklist. Historical
reports in `validation/` describe their dated implementation, not current support.

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
configuration and approvals. By default its assistants run there and dispatch Tasks
to joined environments. Agent, Task and Environment have separate identities and
bindings; remote Agent runners are a later phase. Execution-only nodes do not need
assistant databases or model credentials.

The App connects to one Control as a client. The accepted UI has one icon to switch
between local and remote. Data, drafts, event cursors and cached results are scoped
by mode/Runtime/owner. Remote failures never select local execution. Resource creation
UI is deferred. Machines shows lightweight status cards, expandable environments and
Tasks filtered by the exact execution target. Technical management and file transfers
belong in ordinary Chat; there is no assistant-management shortcut or download UI.
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
receipts are retained. Old peer enrollment/grant state is not imported.

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
execution destination's project rules. Destination skills are discovered there;
caller-private skills are not injected into another machine.

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
Retired Sandbox IDs cannot be silently reused. Remote plugins, artifact transport,
desktop transport, remote Agent runners and three-node qualification remain tracked
in #39; successful Host tests do not establish those features.
