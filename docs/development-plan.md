# Development plan and current scope

Updated: 2026-10-07.

## Delivered

- Host Runtime, product storage and Pot/Codex Agent loops run independently of Sandbox readiness.
- Main chat configures API/Pot or official Codex through one AI Configuration page. pot-agent is compiled into Server; pinned Codex and Node ship in the App; no guest Agent installer or configuration importer exists.
- Main Agent manages up to 16 Linux Sandbox definitions. Task creation saves one immutable environment assignment; workers cannot manage or switch environments.
- Environment tools use the authenticated guest executor. Active leases, cgroup cancellation, durable receipts and explicit recovery protect operation outcomes. Artifacts survive environment shutdown.
- Tasks support independent sessions, bounded concurrency, persistent guidance, cancellation, summaries and archive/search. Schedules save their environment assignment and resolve Agent profiles per occurrence.
- API gateway supports Chat, Responses, Messages and Gemini, with request/usage accounting and allowances. Native Codex reports turn usage separately.
- GPUI/Swift provides Chat, Tasks, Usage, AI/Task/Environment settings and optional Sandbox desktop.
- Shared `pot-ui` Chat/Tasks/Sandbox/Usage components, page transitions, allowance parsing, request/receipt/draft policy and locale-explicit catalogs are used by native App and WASM; native composer and navigation remain unchanged. See [shared UI](shared-ui.md).
- Owner-instance mobile pilot: shared GPUI Kit WASM chat/tasks/inventory, trusted HTTPS QR, owner-approved Passkey enrollment, revocable sessions and encrypted main-message Web Push. See [mobile access](mobile-access.md) and its [dated validation](validation/mobile-access-20261007.md).

See [architecture](architecture.md), [Agent/Sandbox design](agent-sandbox-design.md), [Runtime contract](../runtime/README.md) and [dated acceptance](validation/host-pot-agent-20261006.md).

## Remaining validation and scope

Mobile physical iOS/Android input, accessibility, startup performance, production relay deployment, actual vendor push delivery and complete App bundle boot remain unqualified. Mobile desktop viewing/control is blocked pending separate isolation acceptance; notification links open main chat but do not yet scroll to the matching message.

Real-provider login/inference must be requalified after the Host deployment changes. Full native appearance, IME, weak-network and sleep/wake matrices are not established by mock-model tests. Public release requires cold-install validation and distribution signing/notarization.

macOS/Windows Sandbox providers, direct Host tools, stronger isolation between tasks sharing one VM, persistent background services and cross-environment artifact transfer are not delivered. Browser takeover permissions and full report/source workflows remain scenario-level work; see [Product design](product-design.md#product-acceptance-scenarios).

## Implementation rules

Keep Rust Runtime/SQLite, shared protocol and GPUI/Swift/Helper boundaries. Prefer deterministic tests and actual pinned CLIs against local mock providers. Retired guest-Agent deployment and native engine implementations live only in Git history; do not add migration or compatibility routes. Machine notes belong in `~/Workspace/Machines`.

## Standalone and embedded hosting — 2026-10-07

Owner-generated one-use pairing replaces WebAuthn. The standalone `pot` CLI
provides OS-authorized management, a Web UI release archive and optional macOS
service/provider packaging. App embeds the same backend and dispatches UI
requests in memory; it does not connect to a daemon. See
[standalone hosting](standalone-server.md) and [browser access](mobile-access.md).
Physical-phone and real VM/provider distribution qualification remain separate
from the credential-free hosting tests.
