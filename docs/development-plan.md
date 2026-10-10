# Development plan and current scope

Updated: 2026-10-10.

Computer use (2026-10-08): a default-enabled plugin provides persistent `cua_repl` sessions on Host and Linux Sandboxes under environment permissions, with pinned packages and model-configured text/image observations. Native desktop automation and manual takeover remain pending. See [configuration and contract](computer-use.md).

## Composable Runtime roadmap — proposed, 2026-10-10

Track phase status, acceptance evidence and pre-phase review gates in [Issue #39](https://github.com/jjyr/pot/issues/39). Before implementing a phase's UI changes or behavior dependent on its interaction design, discuss the concrete interaction with the user and obtain confirmation. This applies at execution time; agreement to the roadmap does not approve later UI designs. Independent backend work can proceed without that UI decision. Add newly discovered UI changes to the same tracking table before implementing them.

This is a planning proposal, not an implementation status report or authorization to change model-visible tools. Architecture and internal boundaries are in [Composable runtimes](runtime-composition.md). The user has confirmed Agent-local default dispatch and explicit capability grants between Runtimes. P0 completed on 2026-10-10: the user additionally confirmed stopping new effects on owner disconnection and preserving current data through versioned upgrades. P1 local composition is delivered with the existing App interaction; future UI/tool changes remain subject to their phase review.

```text
P0  Contracts, scope and rollout decisions
 | 
P1  Optional services, local composition
 |
P2  Multiple conversations and connection-aware clients
 |
P3  Direct peers and remote Environments
 |
P4  Remote Agent + co-located Sandbox
 |
P5  General placement, preferences, artifacts and schedules
 |
P6  Product/deployment qualification
```

Every phase includes its necessary client/operator interaction, documentation and scoped verification. UI, permissions, cancellation and recovery are not postponed to the final phase. Complete each exit gate before expanding distributed behavior. There are no time estimates until P0 resolves scope and identifies the storage/protocol rollout work.

### P0 — contracts and review

- Resolve service ownership, independent enablement, stable IDs, assignment records and the local/remote port contracts. Specify dispatch/context/Environment generations separately.
- Define the task state transitions, idempotency receipts, bounded leases, checkpoint acknowledgements and disconnect/cancel semantics. Specify deployment presets and a minimal two-machine reference scenario.
- Inventory hard-coded main assumptions, existing persistent data and client protocol consumers. Decide current-root conversion versus an explicitly documented fresh-root requirement; never clear or reinterpret existing data silently. Retired engine importers remain outside scope.
- Prepare concrete model-visible tool/prompt/default proposals for multiple conversational owners and remote selection, plus wire/schema version changes. Obtain explicit user agreement before implementing each affected tool contract.
- Resolve first-release boundaries and autonomous continuation policy. Proposed default: stop new effects on owner disconnection, reconcile current work; no live migration or automatic failover.

Exit gate: reviewed service/record contracts, phase scenarios, storage rollout choice and a concrete list of approved or pending tool changes. Architecture agreement alone is not tool-interface agreement.

### P1 — independent local composition

Delivered 2026-10-10: separate local service state, scoped Runner adapter, stable identity and versioned Assignments, bare/environment/assistant presets and CLI capability discovery. The local owner facade remains for existing tools; remote acceptance/replay and arbitrary dependency graphs are later work. See [acceptance](validation/runtime-composition-20261010.md) for automated coverage and untested platform/UI scope.

- Replace the monolithic Service with Runtime host and independent Conversation, Task, Agent and Environment service state. Use local port adapters; stop copying the entire Service to create workers.
- Enable services explicitly, validate their dependency references, open only relevant stores/routes and start only relevant coordinators. Register Providers only for enabled Environment services.
- Separate Agent instructions/skills/home resources from the Sandbox Manager wrapper. Keep concrete pot-agent and existing Thread contexts/configurations.
- Deliver bare, Environment-only and App assistant presets. CLI/operator inspection reports actual enabled capabilities; existing App behavior is exercised through its preset.
- Establish local scoped identities/assignments and durable dispatch records needed by subsequent phases. Introduce the async-capable port shape; remote journaling is qualified in P4.

Exit gate: bare Runtime makes no model calls/task dispatch/image downloads or Environment startup; Environment-only execution works without AI configuration; App chat/task/steering/cancellation/context continuity still work. Stopping a service does not implicitly shut down unrelated components. Apply the P0 storage rollout strategy.

### P2 — multiple conversations and client foundations

- Persist Agent definitions, independent conversations/Threads, owner references and per-Agent preference records. Eliminate global-main identity assumptions in routing, permissions, usage and projections.
- Bind each conversational Agent to an explicit Task/control scope. Preserve worker restrictions and each Thread's turn serialization/configuration snapshots.
- Add App/shared-UI assistant selection and creation/configuration flows, with clear conversation/Task ownership. Agent definition reuse does not merge Thread contexts.
- Introduce client transport and connection identity abstractions: local embedded dispatch plus a remote client backend. Cache keys and event cursors include Runtime/owner identity; pages consume capability projections. Peer enrollment is delivered in P3.
- Preserve existing local routing while implementing only the approved multiple-owner tool changes.

Exit gate: two conversational Agents maintain independent context/preferences/permissions, each can dispatch and follow up its own tasks; switching conversations/connections does not mix state or cancel unrelated work. Local behavior and supported frontend protocol consumers pass regression checks.

### P3 — direct connections and remote Environment execution

- Implement stable peer identity, direct endpoint configuration, authenticated encrypted transport, compatibility negotiation, caller-visible discovery, directional grants and revocation.
- Add remote Environment execution and separate management adapters: capabilities, scopes, leases, operation receipts, sessions, polling, cancellation and reconciliation. Preserve concrete Provider containment.
- Deliver connection/grant management through operator controls and the minimum App UI; remote Environment lists/details expose location and grant-limited actions.
- Add authorized artifact listing/reading, and desktop observation/control forwarding only for already supported Provider capabilities, retaining controller grants. Cross-node artifact copying follows in P5.
- Complete authenticated remote UI access to existing services using UI-specific credentials. Demonstrate that a UI on A selecting an Agent on B does not make A its default execution location.
- Implement only the agreed remote Environment tool selection contract. New privileges are never inferred from a connection or from remote prompt text.

Exit gate: `Task A + Agent A -> Environment B` works while B has no Agent/Task service; remote tools, sessions, cancel/restart reconciliation and permission rejection are qualified. UI-only access works without a local assistant. Disconnect or an unavailable target never triggers Host fallback or duplicate operations.

### P4 — remote Agent and Sandbox dispatch

- Add remote RunnerPort and a durable receiving run journal, paired with the owner's transactional dispatch outbox and replayable execution-event outbox.
- Complete async context/control/event acknowledgements, checkpoint revision checks and run-generation fencing without network waits under database/state locks.
- Run pot-agent on B with its assigned Sandbox B; use explicitly selected owner or peer model gateway, with scoped budget grants and attributable usage receipts.
- Forward ordered guidance/interrupt/cancel and concrete approvals; show the Agent and Environment locations, run progress and cleanup-pending state in Task details.
- Enforce bounded grants/leases at destination services. Qualify duplicate dispatch, response loss after acceptance, owner/Agent restart, grant revocation and owner disconnection.

Exit gate: `Task A -> Agent B -> Sandbox B` completes and can continue with preserved context; duplicates/stale events do not cause repeated effects; uncertain results and cleanup remain visible; inference usage is accounted once. The approved owner-disconnection policy is demonstrated on real peers.

### P5 — general composition and routing workflows

- Support `Task A -> Agent B -> Environment C` with explicit usable grants on every service edge; independent gateway/context locations use the same contracts.
- Connect per-Agent saved prompt rules to the authorized Runtime/Agent/Environment catalog. Apply explicit choice, saved rule and Agent-local default precedence; persist revision/reason and fixed placement before dispatch.
- Deliver bounded authorized artifact transfer with destination validation, integrity checks, durable receipts and interrupted-transfer recovery. Expose artifact locations and explicit export/copy actions.
- Integrate schedule owner, dispatch origin, fixed Environment references, model/profile resolution and occurrence deduplication; unavailable peers produce explicit wait/failure rather than retargeting.
- Complete multi-Runtime views for conversations, tasks and Environments, filtered by scope. Queue by selected peer capacity without implicit load balancing or assignment migration.

Exit gate: three-Runtime execution and artifact handoff work; routing prompts choose the intended permitted targets and cannot elevate privileges; schedule placement is independent of the current UI; edits do not rewrite existing Task bindings. Multi-client operation and shared-Environment cancellation remain isolated by authority.

### P6 — deployment and complete product qualification

- Package/install the selected presets on supported hosts; document enabling installed capabilities, connection setup, grants, upgrades, storage handling and shutdown behavior.
- Qualify real supported Host/Sandbox paths on macOS and Linux, plus App and Web clients. Do not claim pending macOS VM qualification or deferred mobile/device Providers merely because remote routing works.
- Run cold-start/upgrade, weak-network, sleep/wake, reconnect, multi-client, resource exhaustion, cleanup and credential-isolation scenarios. Scope new tests to the actual behavior changed in each earlier phase; this phase closes remaining end-to-end gaps.
- Demonstrate real model inference for local, remote-Environment and remote-Agent workflows, with separate accounting and paid-call authorization where required. Record tested platforms and remaining limitations in dated acceptance reports.
- Complete the user journey: connect a node, grant capabilities, select an assistant, save a routing prompt, dispatch, guide, cancel, inspect artifacts and continue after restart. Public distribution has its existing signing/release gates.

Exit gate: the supported deployment matrix and complete workflows have verifiable acceptance evidence. CLI/backend checks alone do not establish App/Provider/UI qualification.

### Scope to align before implementation

Recommended first-release scope is P0–P6 with direct configured peers, installed modules, one authoritative owner per Task, fixed Agent/Environment assignment and visible outage handling. Dynamic remote module installation, live Task/owner migration, automatic failover, distributed consensus, recursive worker delegation, transitive control and new device Providers remain separate projects.

Confirmed on 2026-10-10: owner disconnection pauses new model/tool actions and requires reconciliation; current chat, Tasks, configuration and artifacts are preserved through versioned upgrades, without importing retired engines. The user authorized implementing P0–P6 and committing/updating Issue #39 after each completed phase. UI confirmation remains required at execution time for the affected phases.

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
