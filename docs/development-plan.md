# Development plan and current scope

Updated: 2026-10-11.

## Control Runtime roadmap

[Issue #39](https://github.com/jjyr/pot/issues/39) owns the single phase progress
and acceptance table. [Composable runtimes](runtime-composition.md) is the design
source. Completed historical phases retain their dated evidence; the following
remaining plan replaces direct peer grants, remote Agent relocation and mandatory
three-node placement. Concrete model-visible tool changes require explicit user
agreement. Discuss any new UI interaction before implementation; preserve the
already approved lightweight Chat/Tasks/Machines flows.

### Delivered foundations

P0-P2 establish contracts, optional local resources and multiple assistant owners.
P3a-P3c establish one independent Control, sole machine membership, outward
connections, isolated App local/remote modes and explicit fixed-Control Web hosting.
P3d1 simplifies Machines. P3d2a delivers optional Chat file links, task-scoped
file reads, Control relay and client downloads. See #39 for commits and qualified
scope; completion reports need links only when the task has files to deliver.
Control global snapshots and named remote plugin dispatch are delivered subphases.
Snapshots bind plugin bundles, Shell settings and observation mode; leases pin the
accepted digest. Control chooses the exact model/environment/tool configuration;
a Runtime executes it or reports an unsupported configuration/dependency error.
It does not replace configuration or hide tools based on node-local settings.

### P3d2b — Control resources and remote environment transports

- Make Control authoritative for global plugin/skill discovery, configuration and
  version selection. Dispatch selected immutable bundles to execution nodes;
  nodes may cache them but must not discover node-local global resources.
- Keep project AGENTS.md and project skills at the actual execution workspace,
  with their existing discovery and precedence rules. Preserve separate App local
  resources when the client is in independent local mode.
- Extend remote plugin discovery/invocation from Control-selected definitions,
  executing environment-dependent plugins in the assigned Host/Sandbox. Retain
  model approvals and durable operation receipts. Agree concrete tool interfaces
  before implementing model-visible changes.
- Add outward desktop streaming through Control for supported Sandbox providers;
  reuse the available desktop entry rather than adding a management panel.
- Qualify actual multi-machine App/Web execution, plugins, desktops and optional
  file downloads. Same-host TLS fixtures are not actual cross-machine acceptance.

Exit gate: Control-selected globals are used without destination-global fallback;
project rules/skills remain correct, task configuration is fixed for a run, and
supported environment transports have recorded acceptance and explicit limits.

### P4 — launch remote Task Agents and collect results

- Start a fresh task-scoped pot-agent on the selected machine. Agent and its
  Host/Sandbox environment are co-located in this release; chat assistants stay
  at their owning local Runtime or Control. Do not relocate live Agent sessions.
- Persist Control launch intent and destination acceptance/run/result records.
  Duplicate deliveries refer to the same accepted run. Retain local execution
  evidence for recovery without implementing full live context replication.
- Send necessary status, ordered guidance/cancellation, approvals and usage during
  execution. Fetch the durable terminal result after completion; reconcile lost
  notifications by querying the original run rather than launching it again.
- Use Control's model gateway, credentials, budgets and approvals. Bind task brief,
  global bundle versions and environment before launch. Task Agents have no
  assistant AGENTS.md and retain actual project AGENTS.md/skills.
- Pause new model/tool actions on disconnection and reconcile upon reconnect.
  Show only necessary task status and machine/environment in existing Task UI;
  do not add Runner management or migration UI.

Exit gate: Control launches a remote Agent plus environment on B, collects its
result, and handles duplicate launch, cancellation, missing replies, restarts and
approval with no duplicate effects or implicit fallback. Configuration edits do
not alter accepted runs, and model usage remains attributable at Control.

### P5 — routing, schedules and end-to-end composition

- Connect assistant DB routing rules to available machines/environments. Preserve
  explicit target precedence and Agent-local defaults; persist selected bindings
  and selection_reason before launch. Do not restore preference tools.
- Validate Control Task -> Machine B Task Agent + Host/Sandbox B, including project
  rules, global bundles, approvals, results and optional client file downloads.
- Keep schedule triggers, occurrence deduplication and authoritative state on
  Control; save fixed execution bindings and resolve model profiles per occurrence.
  Offline targets wait/fail explicitly instead of silently changing machines.
- Verify multi-client isolation, shared environment cancellation and capacities.
  No new resource creation UI or automatic placement/migration is included.

Exit gate: actual two-machine routing and scheduled task completion survive the
specified faults with fixed placement, single effects and durable results.
Three-node Agent/Environment separation is deferred beyond #39's first release.

### P6 — deployment and product qualification

- Document Control deployment, machine join/removal, API-created resources,
  global bundle management, App/Web connections, upgrades and shutdown.
- Qualify supported Linux/Apple Host/Sandbox paths and real models, recording
  actual tested platforms separately from simulated coverage and pending providers.
- Exercise cold start, upgrade, weak network, sleep/wake, reconnect, process restart,
  member removal, multi-client use, resource exhaustion and cleanup.
- Finish the workflow: join, select an assistant, save routing rules, launch, guide,
  approve/cancel, collect results, optionally download files and reconcile restart.

Exit gate: supported deployment and product workflows have reproducible evidence.
Public release still follows its existing signing/publication gates.

### Confirmed scope and rollout

One Control per user; joined machines accept Control's commands for configured
resources without per-machine capability grants. Binding authentication, model
behavior approvals, bounded execution leases and operation receipts remain.
App/local Runtime share lifecycle and never become Control. Bare serve creates
no resources or UI implicitly. Web hosting explicitly targets Control.

Existing user data is preserved through versioned upgrades. Superseded peer/grant
interfaces receive no compatibility layer. Live Agent/task relocation, automatic
failover, distributed consensus, recursive worker delegation, arbitrary three-node
placement and new device providers remain outside this release. Remote startup
and Control-global bundle delivery are approved design, still pending implementation;
current remote environment execution alone does not establish these features.

Commit each completed phase and update #39's single table. Historical acceptance
reports remain dated evidence and are not rewritten to claim the new design.

## Delivered

- Host Runtime, product storage and pot-agent Agent loops run independently of Sandbox readiness.
- Main chat configures API connections through one AI Configuration page. pot-agent is compiled into Server; Node ships in the App; no guest Agent installer or configuration importer exists.
- Main Agent manages up to 16 Linux Sandbox definitions. Task creation saves one immutable environment assignment; workers cannot manage or switch environments.
- Environment tools use the authenticated guest executor. Active leases, cgroup cancellation, durable receipts and explicit recovery protect operation outcomes. Artifacts survive environment shutdown.
- Tasks support independent sessions, bounded concurrency, persistent guidance, cancellation, summaries and archive/search. Schedules save their environment assignment and resolve Agent profiles per occurrence.
- API gateway supports Chat, Responses, Messages and Gemini, with request/usage accounting and allowances.
- GPUI/Swift provides Chat, Tasks, Usage, AI/Task/Environment settings and optional Sandbox desktop.
- Shared `pot-ui` Chat/Tasks/Sandbox/Usage components, page transitions, allowance parsing, request/receipt/draft policy and locale-explicit catalogs are used by native App and WASM; native composer and navigation remain unchanged. See [shared UI](shared-ui.md).

See [architecture](architecture.md), [Agent/Sandbox design](agent-sandbox-design.md), [Runtime contract](../crates/runtime/README.md) and [dated acceptance](validation/host-pot-agent-20261006.md).

## Agent permissions and tools — 2026-10-08

pot-agent now owns environment-local policy and Agent-specific permanent grants.
Host defaults to ask; ordinary Sandbox execution defaults to allow. Shared
execution tools and turn-scoped `exec`/`wait` composition retain role checks and
owner review. Main can coordinate Tasks with reference messages and bounded waits,
interrupt while preserving guidance, or cancel and discard it.

Automated checks passed; live guest/UI qualification remains
open. Independent archive state and inactivity rules are deferred. See
[permissions](permissions.md), [tools](tool-composition.md),
[Task coordination](task-coordination.md) and the
[dated validation](validation/agent-permissions-20261008.md).

## Remaining validation and scope

Remote HTTP basic access is an agreed deployment direction, not delivered. It
requires enrollment/session support without browser WebCrypto, remote listener
policy, WASM compatibility verification, capability-based UI and multi-client
qualification. Service Worker/Web Push remain unavailable on insecure origins.
See [Web UI deployment](web-ui-deployment.md) for the recommended HTTPS procedures and
retained HTTP options.

Mobile physical iOS/Android input, accessibility, startup performance, production relay deployment, actual vendor push delivery and complete App bundle boot remain unqualified. Paired owner browser desktop viewing/control is implemented with login-bound tickets; physical mobile input remains unqualified; notification links open main chat but do not yet scroll to the matching message.

Real-provider API inference must be requalified after the Host deployment changes. Full native appearance, IME, weak-network and sleep/wake matrices are not established by mock-model tests. Public release requires cold-install validation and distribution signing/notarization.

Qualified macOS execution, Android/iOS/Windows Sandbox providers, stronger isolation between tasks sharing one VM, persistent background services and cross-environment artifact transfer are not delivered. Browser takeover permissions and full report/source workflows remain scenario-level work; see [Product design](product-design.md#product-acceptance-scenarios).

## Sandbox redesign — 2026-10-07

The first-phase targets are Linux for default and isolated tasks and macOS for native application development. Android, iOS/vphone and Windows are deferred. [Sandbox providers](sandbox-providers.md) defines Manager/Provider contracts, crate boundaries and allocation profiles. The Manager and Linux Provider extraction is implemented, with source image descriptions under `crates/sandbox-images/linux/default/`. Prepared-instance macOS integration is implemented with a separate VZ Helper and guest bootstrap, pending real VM/Xcode qualification. Broader allocation/catalog/artifact contracts remain pending. See [macOS setup and limits](macos-sandbox.md).

First extract `pot-sandbox` between Runtime and separate Linux/macOS Provider crates, registered by App/Server assembly. Move environment lifecycle/scopes/receipts behind its facade while retaining product permissions and task bindings in Runtime. Qualify Linux migration, then one prepared macOS VM with signed Swift helper, authenticated guest broker, Xcode build/test and durable artifacts. macOS initially has exclusive ownership and verified cleanup or VM-stop fallback. Installation/clone/desktop improvements follow a working prepared-guest slice. See [concrete macOS research](research/macos-provider.md#minimum-delivery-plan-after-scope-reduction).

[Android research](research/android-provider.md) and [vphone research](research/vphone-provider.md) remain deferred background; neither requires a first-phase crate or tooling installation. VM installation and performance/isolation measurements have not been performed.

## Implementation rules

Keep Rust Runtime/SQLite, shared protocol and GPUI/Swift/Helper boundaries. Prefer deterministic tests and actual pinned CLIs against local mock providers. Retired guest-Agent deployment and native engine implementations live only in Git history; do not add migration or compatibility routes. Machine notes belong in `~/Workspace/Machines`.

## Standalone and embedded hosting — 2026-10-07

Persistent instance Tokens authorize browser APIs. The standalone `pot` CLI
provides OS-authorized management, a Web UI release archive and optional macOS
service/provider packaging. App embeds the same backend and dispatches UI
requests in memory; it does not connect to a daemon. See
[standalone hosting](standalone-server.md) and [browser access](mobile-access.md).
Physical-phone and real VM/provider distribution qualification remain separate
from the credential-free hosting tests.

Desktop adapter update (2026-10-07): `pot-sandbox` manages expiring viewer tickets and exclusive input; Server supplies a scoped WebSocket/noVNC gateway. App uses that route for Linux and optional owner-configured macOS RFB. Linux browser/metrics/clipboard are retained. macOS has authenticated TLS tunneling and readiness tests, but no real Guest VNC/Xcode qualification. Viewer closure preserves the VM. See [desktop acceptance](validation/sandbox-desktop-20261007.md).

2026-10-07: Separate `pot-resource` / `pot-sandbox-images`, embedded SHA-256 bundles, Runtime-owned download permission/progress, native cache UI, and unbundled Linux resources implemented. Exact Release assets still require publication; macOS media is owner-provided. See [delivery contract](sandbox-resources.md) and its dated validation report.

## Command execution follow-up — 2026-10-07

Implemented shell/login selection, pipe/PTY sessions and terminal controls, Agent-session process retention with epoch/generation authorization, idle expiry and orphan drainage, and bounded in-memory Bash/Zsh login snapshots with startup-file invalidation/fallback. Current contract: [Sandbox command execution](executor.md). Runtime/real-runner tests and broker lifecycle doubles are included; actual verification and limits are recorded in [acceptance](validation/executor-sessions-20261007.md).

## Linux standalone host and multiple clients (2026-10-07)

Provider naming separates host OS/engine from guest OS. Linux adds a rootless
Podman Provider and shared Dockerfile build recipe; macOS retains its Apple
Containerization and VM engines. Standalone Web icons are generated assets rather
than a macOS build dependency. Multiple-client regression runs in backend CI;
see the dated Linux/client acceptance report for actual verification and limits.

Podman desktop follow-up (2026-10-07): private loopback broker transport, bundled noVNC, Web Sandbox launch, login-bound tickets and revocation guards are implemented. Actual Linux rendering, mouse/keyboard, clipboard, browser launch and cgroup metrics passed; see [acceptance](validation/podman-desktop-20261007.md).

## Web access redesign — 2026-10-08

App has separate Web UI and Tailscale settings. New instances default to no browser
listeners. LAN HTTP and embedded tailnet access are independent; tailnet HTTPS is
optional with automatic certificate provisioning/renewal. CLI retains
localhost/manual HTTPS and optionally uses the same embedded transport. A persistent
instance Token replaces browser enrollment; connection links/QR fragments configure
browser storage automatically. Rotation invalidates every browser/stream/desktop.
Go source and dependency graph are pinned, and linked dependency notices are packaged.
Local Server/CLI/TLS and multi-client tests cover Token behavior. Real tailnet login,
certificate issuance, physical phones and complete App bundle acceptance remain live gates.

## Single Agent engine (2026-10-09)

Production execution uses only the in-process pot-agent with API model connections.
The CLI adapter crate, native authorization/discovery routes, process launch
contracts, native thread bindings and native usage accounting are removed.
No legacy conversation or usage migration is provided. Core execution contracts
remain shared by Runtime and evaluation; Sandbox Manager/Provider boundaries and
Agent tool interfaces remain intact.

## Product-owned Threads (2026-10-09)

Main chat and tasks now own their configuration snapshots and Agent contexts directly.
Runtime and eval call pot-agent without Engine/Binding/NativeRef or Driver host/session
adapters. Context is persisted in the owner's SQLite Store. Configuration updates
preserve history at a safe turn boundary; the old handoff/migration state machine is
removed. Client snapshot/event schema is version 2 with a minimal Agent status.
No old conversation/configuration/usage compatibility is provided. See
[Thread validation](validation/thread-execution-20261009.md).
