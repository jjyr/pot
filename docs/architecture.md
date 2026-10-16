# Architecture

Pot uses Go for product logic, Wails v3 with the system WebView for the macOS
App, and a shared HTML/CSS/JS frontend. The main window, tray panel and browser
use the same components, styles and state contracts. Swift retains the macOS
VM implementation; Linux Sandboxes use the same Go Podman provider on macOS and
Linux. Node/JavaScript plugins and Python guest services retain
their existing responsibilities. See [component boundaries](backend-components.md).

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
                         Host/Sandbox       Host/Sandbox

independent web-ui-server -> one explicitly selected Control -> browser frontend
```

## Hosts and roles

`pot serve` hosts standalone Runtime; App composes its local Runtime in process.
App's local UI dispatches directly through Wails bindings, without a loopback UI
listener. Embedded Runtime cannot enable a remote Control listener or join a
Control. Closing a window hides it while background work continues. Quitting App shuts
down its local executions and Helpers.

A standalone root starts neutral. An explicitly configured listener enables
Control; a joined execution Runtime becomes Runner. One root cannot hold both
roles. A Runtime owning assistants or AI configuration cannot join a Control.
Fresh registries contain no assistants. Saving Provider defaults does not create
one; assistant creation is explicit. Use independent roots for independent roles.

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
configuration, provider keys, accounting, approvals and global resources. An
assistant remains on its owning Control. Task placement selects a machine; that
machine's policy resolves Host or Sandbox when dispatch starts. The resulting
execution target remains fixed for the run. An unavailable target fails visibly.

Without an explicit machine, routing selects a `task-runner` label, preferring
local Runtime then stable Runtime ID order. Labels describe routing intent and
never grant capabilities. Model selection is independent: an explicit configured
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
Host approvals bind the original assistant/Task, generation, call, exact operation
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

Sandbox definitions, selected Sandbox, VM/container lifecycle and execution scopes
are separate. Creating/selecting a definition does not boot a guest. Main and
Workers may share a Sandbox; task workspaces and processes are scoped separately,
but they are not mutual security boundaries. Provider/mapping/resource changes
and deletion obey stopped-state, active-lease, receipt and retained-reference
checks. Unknown cleanup requires explicit recovery. On macOS, Linux containers
share a dedicated Pot-managed Podman machine; Linux hosts use rootless local
Podman. Container boundaries share the underlying Linux kernel. The retained
Swift Helper owns native macOS VMs. See [Sandbox providers](sandbox-providers.md).

## Persistence and updates

A Go data root has a stable `identity.json`, exclusive process lock and private
component stores. The release uses new data and machine registration; previous
Rust databases/configuration are not imported. The same root cannot be opened by
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
