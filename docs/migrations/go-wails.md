# Go/Wails migration plan and rules

This is a temporary, user-authorized migration document. It defines the agreed
scope, implementation order and acceptance rules; it does not describe delivered
Go functionality. [Issue #66](https://github.com/jjyr/pot/issues/66) owns progress,
completion evidence, decisions and validation limits. Code and the regular
[architecture](../architecture.md) describe current behavior.

Delete this document when migration is complete, after moving lasting contracts
and operating instructions into the regular documentation. Remove its index and
development-plan links and the temporary exception in the root AGENTS.md in the
same cleanup. Do not add test reports, dated progress entries or a second status
checklist here.

## Agreed scope

Replace Pot's Rust business and infrastructure logic with Go. Replace GPUI native
and GPUI/WASM presentation with shared HTML/CSS/JavaScript hosted by Wails v3 on
the desktop and by the existing independently configured browser service model.

The first platform scope is the existing macOS desktop support, Linux services
and execution nodes, and browsers. Windows desktop is outside this migration's
first scope. Preserve current product behavior and visual appearance.

Keep Swift macOS sandbox Helpers and the Apple Containerization/Virtualization
implementation. Keep Node plugins, Computer Use, persistent JavaScript REPLs,
Python guest services and brokers. Adapt their Go callers, interfaces and build
integration as needed. Do not replace the macOS sandbox with Lima or introduce a
new VM manager as part of this migration.

Use goja preferentially for the tool-composition JavaScript currently hosted by
Rust/QuickJS. This is separate from Node plugin and REPL execution.

Fresh databases, configuration, identities and machine registration are allowed.
Historical data import and mixed Rust/Go production-node compatibility are not
required. Preserve useful existing interfaces; update all affected callers,
tests and documentation if an interface changes. Permission to recreate data does
not authorize deleting existing user data.

## Migration organization

Keep old and new sources in the same repository during development, with
independent runnable entry points and separate data roots. Do not share an active
database, dual-write state, or enroll the same machine into both versions. Do not
build a transitional Rust sidecar, Rust/Go FFI layer or mixed-version transport.

Deliver small reviewable changes within each phase. Integrate an observable Go
business flow before expanding to the next layer. Keep the old implementation
available as a behavioral reference until its replacement has passed the relevant
acceptance criteria. Final cutover uses the complete Go deployment.

Add Go build and relevant checks with the first Go code. Retain old checks until
their corresponding entry points retire. Build and packaging integration happens
throughout the migration, not only in the final phase.

## Component boundaries and proposed layout

Use one root Go module and focused packages rather than a separate module for
every Rust crate. The names below are a proposed organization, not implemented
interfaces:

```text
go.mod / go.sum
cmd/
  pot/                       CLI, serve, Control and Runner entry points
  pot-app/                   Wails desktop entry point
  pot-tool-executor/          Host and guest execution program
internal/
  core/ protocol/            Domain types and transport DTOs
  modelprotocol/             Model codecs without networking or storage
  storage/ secrets/ usage/ gateway/
  agent/ composition/        Agent engine and goja tool composition
  assistant/ relay/
  execution/ runner/         Shared execution manager and destination Task runs
  control/ runtime/          Business coordination and lifecycle composition
  executor/ sandbox/ resource/
  transport/                 Local, CLI, Control and browser adapters
  desktop/                   Wails host adapter
web/
  assets.go                  Shared embedded frontend resources
  src/
    components/ pages/ state/ adapters/ locales/
helper/                      Retained Swift implementation
plugins/                     Retained Node/JavaScript plugins
```

Preserve the responsibilities in [Backend components](../backend-crates.md):

- Runtime composes components and manages lifecycle; it does not own business
  databases, HTTP handlers or provider configuration.
- Control owns coordination, policy, approvals and budgets. Gateway and Secrets
  stay at Control. Agent, Runner and Execution receive narrow model and policy
  interfaces and do not acquire provider credentials or reverse dependencies on
  Control/Runtime/Server.
- Every product Agent invocation enters the destination's shared Execution
  manager. Assistant and Task adapters supply context and consume results.
- Relay remains transport-independent and is composed by App only.
- Sandbox Manager owns definitions, leases, generation fences, receipts and
  recovery. Platform providers implement lifecycle and authenticated guest access.
- Wails belongs only to desktop composition and adapters. Linux CLI, Control,
  Runner and guest executor must build without GUI/WebView dependencies.

Use Go domain/DTO validation, net/http transport, explicit SQLite transactions,
AES-GCM secrets and structured cancellation as appropriate. Validate
modernc.org/sqlite as the initial SQLite candidate. Reuse existing Tailscale Go
code and evaluate direct tsnet integration. Pin library versions after the early
probes; candidate libraries are not yet qualified project dependencies.

Preserve non-Rust resources currently inside crates before removing their Rust
owners: plugin-host.mjs, guest scripts, image recipes, locales, icons, avatar
assets and viewer files need explicit new ownership and updated consumers.

## Shared frontend and visual consistency

Use ES Modules, semantic CSS variables and small reusable components. Main window
and browser share product pages, forms, navigation, reducers and business state.
The tray panel uses compact composition of the same visual system. Put Wails,
browser history, downloads, authentication and system capabilities behind host
adapters; product pages do not scatter direct platform calls.

Desktop binds through Wails services/events; browser binds through HTTP/SSE and
its independently configured web-ui-server. Both reach the same application
service contracts. Wails server build is optional and must not expose desktop
or operator APIs to browsers. Preserve browser authentication, server-held
Control credentials and the browser route allowlist.

Visual consistency is a completion condition. Use the current product,
[Design language](../design-language.md), and the existing design.rs/pages.rs
values as the baseline. Resolve discrepancies between code and documentation
explicitly. Preserve:

- Semantic light/dark/system colors, typography, code fonts, spacing, content
  widths, density, borders, radii, icons, avatars, charts and navigation geometry.
- Mostly unboxed assistant replies, soft blue user messages, restrained cards and
  fine dividers. Do not introduce a separate tray theme or unrelated redesign.
- The 720 px navigation breakpoint, 900 px compact-layout breakpoint and current
  narrow/touch density rules. Tray density is an explicit compact layout, not a
  mechanical application of phone rules.
- Hover, active, focus, disabled, selection, approval, unread and status states;
  existing restrained motion and Reduced Motion behavior.

Establish shared CSS tokens and base controls before migrating full pages. In M1,
compare the shell, controls and a representative Chat page before expanding.
Compare each migrated page against deterministic old-version screenshots using
the same dimensions, theme, language, content and scroll position. Cover Chat,
Tasks/Schedules, Machines, Usage, Settings/editors and approvals; include 390 px,
both sides of the layout breakpoints and normal desktop widths, light/dark and
English/Chinese. Exercise loading, empty, long text/code, streaming, error and
offline states. Check both Wails' actual system WebView and supported browsers.

Record reasonable native-decoration/font-rendering differences explicitly; they
do not excuse product-layout drift. Pages with material visual differences are
not complete even if their API calls work. Present necessary visual changes or
new tray interactions separately for review before implementation. Keep visual
evidence in task artifacts or the issue, outside repository validation reports.

Preserve event-driven updates rather than introducing snapshot polling. Keep
request, draft, cache and cursor ownership scoped to Space/Control/assistant;
reject late responses and stale subscriptions. Preserve dirty-editor exit guards,
save reconciliation, navigation history, reading position and file delivery.
Validate DOM composition input, Enter during IME composition, paste, selection,
caret movement and reference completion. See [UI events](../ui-events.md),
[Shared UI](../shared-ui.md) and [Multi-Space UI](../multi-space-ui.md).

## goja composition and retained runtimes

Tool composition implements exec/wait, not the persistent Node REPL. Give each
exec run its own goja Runtime, driven by one goroutine/event loop. Execute tools
asynchronously in Go and marshal completion back to that loop before resolving
Promises; never concurrently access VM values or resolve from tool goroutines.

Keep the async wrapper, await, Promise.all, tools.NAME(args), text(value),
incremental wait output, run identity, turn scope and cleanup semantics. Expose
only the current authorized tool interface: no additional filesystem, network,
process, module or recursive exec/wait access. Every child operation uses the
ordinary authorization, approval, target and receipt path.

Retain bounded code, output, calls, concurrency, stack and execution time. goja
interruption handles JavaScript execution; Go tools require context cancellation
and confirmed cleanup. Cancellation does not roll back completed effects.

The existing QuickJS implementation has a 64 MiB per-runtime memory limit. goja
does not provide an equivalent hard heap limit. Decide the resource policy in M1
before broader integration. A Go process-wide soft memory target is not a
per-VM limit. If hard limits remain required, evaluate separate Go workers and
platform resource controls, and update affected tool descriptions/contracts.

Keep Node plugin protocol, persistent state, typed text/image results, disposal,
session identity and process cleanup. Keep Swift Helper and Python guest
protocols where practical. Keeping Python does not keep its Rust subprocess:
current guest executors launch pot-tool-executor, which must become Go and be
included in Linux images and macOS guest installation paths before acceptance.

## Business invariants

Preserve the current contracts linked from the documentation index, especially:

- Provider credentials remain at the owning Control. Execution nodes do not gain
  provider keys or assistant databases.
- App and its local Runtime share lifecycle. Embedded Control never opens the
  remote member listener. Closing a window retains background work; explicit Quit
  drains the local Runtime.
- Members connect outward to their bound Control. Removal invalidates access.
  Remote failure never selects local execution, a different model or another
  target as fallback.
- Task Agents start at their fixed destination Runtime. Shared Execution owns
  capacity/cancellation; remote waiting consumes no local Agent slot.
- Resource synchronization, project rules/Skills, selected plugins and run-fixed
  configuration retain their existing ownership and integrity checks.
- Duplicate requests reconcile original identities/receipts. Unknown outcomes
  fence effects; restart must not silently repeat commands or launch another run.
- Cancellation, turn end, shutdown and generation changes clean up sessions,
  process descendants, leases and plugin state. Unconfirmed cleanup stays fenced.
- Budgets retain reservation/settlement semantics; schedules retain time zones,
  trigger deduplication and persistence. Messages and files retain owner/target
  scoping and their delivery/recovery rules.
- Existing local desktop viewing retains ownership and ticket boundaries. Deferred
  remote desktop transport is not added by this migration.
- Tool interfaces remain focused. Do not expand model-visible tools or change
  their behavior merely because their implementation language changes.

Specify DTO missing/null behavior, enums, unknown fields, frontend integer
precision, event ordering and error categories explicitly. Signed payloads and
digests require explicit serialization bytes, not incidental Go map ordering or
default JSON behavior. Preserve stable non-Rust interfaces where useful; this
does not require Rust/Go version interoperability.

## Phase order and exit criteria

This table specifies dependencies and acceptance, not completion status. Record
actual progress only in issue #66.

| Phase | Deliverable | Dependencies | Exit criteria |
| --- | --- | --- | --- |
| M0 Contracts and skeleton | Root module, three entry points, host interfaces, component boundaries and build/check entry points | None | Data roots, DTO/events, plugin/Helper protocols and observable acceptance flows are explicit; Linux builds without GUI dependencies |
| M1 Risk probes | Shared main/tray/browser shell, goja bridge, Go calls to retained Node/Swift/Python | M0 | IME, focus, exit, streaming and representative Chat visuals checked; asynchronous tools/cancellation work; resource policy decided |
| M2 Foundations | Domain/DTOs, SQLite, Secrets, Usage, model codecs and Gateway | M0; resolve M1 findings | Fresh initialization, key reuse, budget transactions, streaming parsing, validation and cancellation checked |
| M3 Local Go flow | Execution, Agent, goja, Go executor, Node integration, Assistant and minimal Control/Runtime/CLI | M1 and M2 | Create assistant, stream reply, approve, run Host command/PTY/plugin, persist results and handle cancellation/restart through one Execution manager |
| M4 Sandbox Go flow | Go Manager/Providers, retained Swift/Python, Go executor images/installers | M3 | Real macOS sandbox and Linux rootless Podman execution, workspace/resources, leases, generation, stop/rebuild and cleanup checked |
| M5 Distributed Go flow | Membership, outward transport, Task runs, resources, approvals, budgets, schedules and messaging/Relay | M3; remote Sandbox cases need M4 | Actual macOS/Linux Host/Sandbox runs reconcile duplicate requests, interruption, restart and uncertain receipts without leaking credentials |
| M6 Complete product UI | All pages, Spaces, drafts/forms, files, local desktop, notifications, PWA and localization | Starts with M1 and each service; complete after M5 | Desktop/tray/browser functionality, visual comparison, event order and owner isolation pass |
| M7 Release and Rust retirement | App/Linux/guest packages, resources, signing/licenses/docs and old-code removal | M4, M5 and M6 | Clean build/install works; no project Rust binaries/libraries or GPUI/WASM build dependencies remain; retained runtime resources are complete |

M1 uses deterministic fixtures or a minimal Go service rather than a transitional
Rust adapter. Probe Node state/results/dispose, Swift lifecycle interfaces and
Python/executor message boundaries; full VM isolation still belongs to M4.

M3 migrates the executor on Host first. M4 installs that Go executable under the
existing Python guest service and checks stdin/PTY, process groups, cgroup
descendants, temporary directories, leases and quarantine after uncertain cleanup.

Within M5, integrate membership and durable command receipts, then a remote Host
Task, remote Sandbox/resources/plugins, failure recovery for model/approval/budget
and guidance/cancellation, then schedules/messages/Relay/files. Use new Go Control
and Go Runner throughout distributed acceptance.

UI progresses with services: M1 establishes shared foundations; M3 connects
Chat/approvals; M4 connects machines/local desktop; M5 connects remote Tasks,
schedules, Usage and Spaces. M6 completes coverage and whole-product review.

## Verification and final cleanup

Use existing tests as behavioral input, not as a requirement to reproduce Rust
implementation details. Adapt useful Python/Node black-box scenarios and add Go
tests for transactions, concurrency and failure semantics. Update architecture
checks for Go package dependencies. Run relevant checks after affected changes,
then a phase-level flow and final cross-platform release acceptance.

Observe outcomes such as one command effect, no remaining process descendants,
recoverable original results and isolated owner state. Compilation and fixture
success do not establish real-model, cross-machine, VM or interactive acceptance.
Report those scopes separately in the task response/issue. Compare startup,
package size, memory, long conversations and streaming on equivalent scenarios;
do not promise performance gains without measurements.

At cutover, initialize a new data root and register new nodes. Preserve old data
roots. A return to the old release uses its original data, not the new Go schema.
Verify Swift, Node, Python, plugins, images, viewer resources, localization, icons
and notices in actual installation artifacts.

After actual App/Web, Swift sandbox, Linux-node and recovery acceptance, remove
the replaced Rust crates/Cargo workspace, GPUI fork, WASM tooling and obsolete
build hooks. Check scripts, image recipes, CI, packaging and docs for stale Rust
paths. Move lasting contracts into architecture, backend components, shared UI,
tool composition, deployment/development and other owning documents as affected.
Then delete this temporary plan, its navigation links and the AGENTS.md exception;
retain the migration history in issue #66 and Git.
