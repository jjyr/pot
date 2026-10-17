# Architecture

Pot uses Go for product logic, Wails v3 with the system WebView for the macOS
App, and a shared HTML/CSS/JS frontend. The main window, tray panel and browser
use the same components, styles and state contracts. Go executes local commands
and Node tool plugins. Runtime manages no VMs, containers or guest brokers.
See [component boundaries](backend-components.md).
Reusable Go libraries live in `pkg/`, command entry points in `cmd/`, and product
implementation in `internal/`. [Go libraries](go-libraries.md) describes the
independent Agent loop and complete-host embedding APIs.

```text
macOS App: Wails windows/tray + embedded Runtime + shared frontend
                      |
              owner-scoped Spaces
               /             \
       local Control      remote Control
                          assistants, rules, Tasks, schedules,
                          provider keys, approvals, accounting
                              ^                 ^
                      outward signed     outward signed
                      WebSocket RPC      WebSocket RPC
                         Runner A           Runner B
                         local tools        local tools

independent web-ui-server -> one explicitly selected Control -> browser frontend
```

## Hosts and roles

`pot serve` hosts standalone Runtime; App composes its local Runtime in process.
App's local UI dispatches directly through Wails bindings, without a loopback UI
listener. Embedded Runtime cannot enable a remote Control listener or join a
Control. Closing a window hides it while background work continues. Quitting App shuts
down its local executions.

A standalone root starts neutral. An explicitly configured listener enables
Control; a joined execution Runtime becomes Runner. One root cannot hold both
roles. A Runtime owning assistants or AI configuration cannot join a Control.
Fresh registries contain no assistants. First-use Chat explicitly bootstraps Pot
once a default Provider exists; later assistants use the creation form. Provider
storage itself creates no identity. Use independent roots for independent roles.

CLI owns a private authenticated Unix socket. Remote native clients authenticate
with a separate Control owner credential. Runner enrollment credentials are not
owner credentials. Only standalone Control opens a membership/owner listener;
Runners connect outward and need no inbound execution port.

The browser service is an independent process. Its destination, browser root,
public origin and transport must be explicit. It verifies the selected Control
identity, keeps upstream owner credentials server-side and never falls back to
another backend. Browser access credentials, local operator credentials and
Control membership credentials have separate responsibilities. App does not
publish a browser endpoint. See [Deployment](deployment.md).

## Product ownership

Control owns assistant identities, rules, conversations, Tasks, schedules, model
configuration, provider keys, external-service accounts, accounting, approvals and
global resources. The independent Integrations component manages third-party
login and credentials through injected account and Secrets stores. Control
provides authentication, routing and composition. [Integrations](credentials.md)
defines browser authorization, run-bound credential injection and direct GitHub
CLI access. An
assistant remains on its owning Control. Main tools run there; new Tasks default
to the same Runtime. An explicit machine selects an enrolled Runner. The actual
Runtime remains fixed for the run, including receipts and file delivery. Missing
or disconnected targets are never replaced. Labels describe machines but do not
implicitly select a default destination or grant capabilities.

Model selection is independent: an explicit configured
Provider/model takes precedence; omission snapshots the invoking assistant's
active configuration. Saved capacity rules admit or queue that route without
choosing another model. Existing Tasks keep their resolved specification.

Main and Worker invocations share the destination Runtime execution manager.
Workers cannot coordinate other Tasks. Summary sessions only inspect Tasks and
produce owner-chat summaries. Synthetic results remain reference data rather
than new user authority. See [Tasks](task-coordination.md),
[schedules](scheduled-tasks.md) and [usage](token-usage.md).

Assistant communication uses separate durable model contexts and transcripts.
An inbound peer message is untrusted reference data. Replies require an explicit
send tool; final model text is not automatically forwarded. Native App composes
cross-Space relay endpoints from existing owner connections. Control, CLI and
browser do not independently relay between Controls. See
[assistant messages](assistant-messages.md).

## Membership and remote execution

Control and Runner retain stable Ed25519 identities. Enrollment uses a short-lived
single-use invitation and a pinned Control Runtime ID/fingerprint. Exact join
retries keep the original request identity. Per-machine epochs fence removed or
re-enrolled sessions. Removing a machine closes its active connection; leaving
persists a reconnect fence until outstanding execution cleanup is confirmed.

Outward WebSocket RPC signs every request/reply with sender, audience and epoch.
Bounded signed fragments carry larger payloads. Receivers validate order, size,
checksum and assembly lifetime before dispatch. HTTP/HTTPS selection does not
replace these business identities. HTTPS uses ordinary certificate/hostname
verification; a configured private CA augments system trust and persists with
membership. Redirects are disabled.

Control persists Task launch intent before sending it. Runner persists immutable
acceptance, original run identity, tool-command admission and terminal results.
Duplicate launches return the original receipt. Lost acknowledgements, transport
timeouts and restart never trigger a second launch, model sample, tool effect or
replacement target. Control inspects the original run and reconciles its receipts.
Uncertain effects and unconfirmed cleanup retain admission/capacity fences.

Models execute through Control's Gateway and usage ledger. Provider credentials
never cross to Runner or guest tools. Remote model streams are bounded signed
chunks with strict sequence; losing a stream read does not resample the model.
Execution approvals bind the original assistant/Task, generation, call, exact operation
and target. Runner independently validates the signed approved operation against
its run before execution. See [permissions](permissions.md).

Task events persist on the original Runner. Control reads bounded pages and
caches fetched remote pages for disconnected views. Files resolve beneath the
same original task workspace, including ended Tasks. Downloads use revision-bound
192 KiB chunks, a 1 GiB limit and confined filesystem roots; revisions include
file identity and mutation metadata. No whole-file hash is repeated per chunk.

## Context and resources

Agent receives explicit execution services and context; it does no product or
machine discovery. Main loads global, assistant and destination project rules.
Worker loads its task brief and destination project rules, with no Main/global
instruction inheritance. Deeper project skills take priority; selecting global
skills never filters destination project skills. Omitted/empty selections load
no globals. See [Agent context](agent-context.md).

Control fixes model configuration and selected plugin/Skill names at Task creation.
Immediately before the original Runner prepares execution, it synchronizes current
Control global Skills and the source Profile's current selected plugin directories
through a run-bound authenticated request. Queued Tasks observe source updates.
The destination Runner persists the prepared content and digest once for that
original run, reusing it for allowance resumes and authorization checks. Control
keeps no per-Task resource archive. Project Skills and AGENTS.md are discovered at
the fixed execution workspace; Main instructions do not cross this boundary.

Model context uses classified append-only sections. State refreshes do not rewrite
user input. Compaction durably installs current sections, continuation summary
and the last real user message before continuing. Provider cache lifetime does
not reset section comparison. Raw HTTP/token observations, estimates and budget
reservations remain distinct.

Tool plugins use the package format in `pkg/plugin` and bounded Node sessions in
`pkg/pluginhost`. Packages contain schema-2 `contributes.tools` with
`execution: "runtime"`. Provider/resource capabilities are unsupported. Runtime
locates tool/Skill assets independently of execution preparation. See
[Plugin packages](plugins.md).

All execution uses the destination Runtime service account. Default authorization
is `ask`; allow-once binds exact call arguments, while always-allow persists for
one enrolled machine in one Space across assistants and Tasks. Instruction edits
retain independent review. See [Permissions](permissions.md).

Main and Task workspaces live in the destination filesystem with no mapping or
implicit project copy. Commands use local OS paths. Process-group cleanup and
unknown-result fences remain; this path provides no guest cgroup containment or
VM isolation. See [Executor](executor.md). Optional VM management, automatic node
placement and remote Main tools remain deferred under issue #74.

## Persistence and updates

A Go data root has a stable `identity.json`, exclusive process lock and private
component stores. Initialize each root independently and retain its machine
identity across restarts. The same root cannot be opened by
App and standalone Runtime concurrently. Provider keys are encrypted using a
separately retained master key; see [Secrets](secrets.md).

Owner resource commands persist pending admission and original terminal receipts.
A repeated request returns its original response, including after later policy
edits. A pending command after process failure blocks conflicting replacement;
inspection never silently replays it.

UI state is owner-scoped by Space and assistant. Navigation generations reject
late replies; drafts and selections retain their owner. Healthy clients subscribe
to changes and stream conversation/Task output. Reconnect uses snapshots and
contiguous durable cursors. There is no healthy-state UI data polling loop.
See [shared UI](shared-ui.md), [multi-Space UI](multi-space-ui.md) and
[UI events](ui-events.md).
