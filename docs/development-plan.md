# Development plan and current scope

Updated: 2026-10-08.

## Delivered

- Host Runtime, product storage and Pot/Codex Agent loops run independently of Sandbox readiness.
- Main chat configures API/Pot or official Codex through one AI Configuration page. pot-agent is compiled into Server; pinned Codex and Node ship in the App; no guest Agent installer or configuration importer exists.
- Main Agent manages up to 16 Linux Sandbox definitions. Task creation saves one immutable environment assignment; workers cannot manage or switch environments.
- Environment tools use the authenticated guest executor. Active leases, cgroup cancellation, durable receipts and explicit recovery protect operation outcomes. Artifacts survive environment shutdown.
- Tasks support independent sessions, bounded concurrency, persistent guidance, cancellation, summaries and archive/search. Schedules save their environment assignment and resolve Agent profiles per occurrence.
- API gateway supports Chat, Responses, Messages and Gemini, with request/usage accounting and allowances. Native Codex reports turn usage separately.
- GPUI/Swift provides Chat, Tasks, Usage, AI/Task/Environment settings and optional Sandbox desktop.
- Shared `pot-ui` Chat/Tasks/Sandbox/Usage components, page transitions, allowance parsing, request/receipt/draft policy and locale-explicit catalogs are used by native App and WASM; native composer and navigation remain unchanged. See [shared UI](shared-ui.md).

See [architecture](architecture.md), [Agent/Sandbox design](agent-sandbox-design.md), [Runtime contract](../runtime/README.md) and [dated acceptance](validation/host-pot-agent-20261006.md).

## Remaining validation and scope

Remote HTTP basic access is an agreed deployment direction, not delivered. It
requires enrollment/session support without browser WebCrypto, remote listener
policy, WASM compatibility verification, capability-based UI and multi-client
qualification. Service Worker/Web Push remain unavailable on insecure origins.
See [Web UI deployment](web-ui-deployment.md) for the recommended HTTPS procedures and
retained HTTP options.

Mobile physical iOS/Android input, accessibility, startup performance, production relay deployment, actual vendor push delivery and complete App bundle boot remain unqualified. Paired owner browser desktop viewing/control is implemented with login-bound tickets; physical mobile input remains unqualified; notification links open main chat but do not yet scroll to the matching message.

Real-provider login/inference must be requalified after the Host deployment changes. Full native appearance, IME, weak-network and sleep/wake matrices are not established by mock-model tests. Public release requires cold-install validation and distribution signing/notarization.

Qualified macOS execution, Android/iOS/Windows Sandbox providers, direct Host tools, stronger isolation between tasks sharing one VM, persistent background services and cross-environment artifact transfer are not delivered. Browser takeover permissions and full report/source workflows remain scenario-level work; see [Product design](product-design.md#product-acceptance-scenarios).

## Sandbox redesign — 2026-10-07

The first-phase targets are Linux for default and isolated tasks and macOS for native application development. Android, iOS/vphone and Windows are deferred. [Sandbox providers](sandbox-providers.md) defines Manager/Provider contracts, crate boundaries and allocation profiles. The Manager and Linux Provider extraction is implemented, with source image descriptions under `sandbox-images/linux/default/`. Prepared-instance macOS integration is implemented with a separate VZ Helper and guest bootstrap, pending real VM/Xcode qualification. Broader allocation/catalog/artifact contracts remain pending. See [macOS setup and limits](macos-sandbox.md).

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

App Web access now requires an embedded libtailscale node with tailnet-only HTTPS,
automatic certificate provisioning/renewal and a dedicated settings page. CLI retains
localhost/manual HTTPS and optionally uses the same embedded transport. A persistent
instance Token replaces browser enrollment; connection links/QR fragments configure
browser storage automatically. Rotation invalidates every browser/stream/desktop.
Go source and dependency graph are pinned, and linked dependency notices are packaged.
Local Server/CLI/TLS and multi-client tests cover Token behavior. Real tailnet login,
certificate issuance, physical phones and complete App bundle acceptance remain live gates.
