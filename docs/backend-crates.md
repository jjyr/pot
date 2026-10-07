# Backend crate boundaries

Updated: 2026-10-06. See [architecture](architecture.md) for deployment and [Agent/Sandbox design](agent-sandbox-design.md) for authority.

| Crate | Responsibility |
|---|---|
| `pot-core` | Immutable profiles/bindings, execution/feedback/backend traits, shared environment and control tool schemas; no process or filesystem implementation |
| `pot-agent` | Host in-process model loop, context, compaction, tool loadouts, filesystem skill discovery and metering; execution requires an injected ToolExecutor |
| `pot-driver` | Host Codex App Server sessions, native identities, normalized events and cleanup |
| `pot-tool-executor` | Shared patch and pipe-session implementation; credential-free guest binary. Local execution is explicitly injected only in eval and isolated tests |
| `pot-storage` | Host product SQLite, bindings, request/usage ledgers and durable transactions |
| `pot-gateway` | Provider transport and Chat/Responses/Messages/Gemini codecs |
| `pot-runtime` | Product tool authorization/routing, tasks/schedules, execution preferences, accounting and recovery |
| `pot-resource` | Generic HTTPS downloads, byte count/SHA-256 checks, safe extraction, digest cache, shared progress/cancellation |
| `pot-sandbox-images` | Embedded resource catalog, compatible bundles, template descriptions and Linux/macOS recipes; uses pot-resource |
| `pot-sandbox` | Inventory, lifecycle/readiness, leases, execution scopes, receipts and recovery; no concrete Provider dependencies |
| `pot-sandbox-linux` | Apple Containerization helper IPC and Linux guest transport |
| `pot-sandbox-macos` | Owner-enrolled VZ instance lifecycle, pinned guest TLS and whole-VM scope cleanup; awaiting real VM qualification |
| `pot-server` | Standalone startup, HTTP/SSE/MCP/PTY routing and shutdown |
| `pot-protocol` | Frontend/backend DTOs and API constants |
| `pot-app` | GPUI/native UI, protocol client and cached projections |
| `pot-eval` | Standalone deterministic and explicitly selected live evaluation |

App depends on Protocol and embeds Server through its local service adapter. Server composes Runtime without App or GPUI. Runtime owns Storage, Gateway, Agent and Driver integration, and injects its Sandbox ToolExecutor into pot-agent. Core carries the backend trait without depending on Runtime. The guest executor knows neither Runtime, model providers nor Host product credentials.

App embeds Host Server; standalone CLI hosts it independently. Helper owns local VM lifecycle and supports Sandbox-only hosting. Execution tools run in assigned guest scopes; shell code cannot access unmounted Host product state. Tasks sharing a Sandbox use the same guest account, so separate directories are organizational rather than mutual filesystem/process isolation.

Model and native CLI requests run on the Server machine. Remote deployment, endpoint identity/TLS and artifact transfer remain outside current product scope. Tests/build commands are in [Development](development.md); results are recorded in [Host pot-agent acceptance](validation/host-pot-agent-20261006.md).

## Sandbox extraction

The [Sandbox Manager and Provider crate design](sandbox-providers.md#provider-crates-and-explicit-registration) moves environment inventory/lifecycle, scopes, receipts/recovery, artifacts and display arbitration into `pot-sandbox`. Runtime retains product permissions, task/schedule bindings and tool schema integration. Separate `pot-sandbox-linux` and `pot-sandbox-macos` crates implement its contracts; Server's shared backend assembly registers enabled Providers and injects the Manager. `pot-sandbox` depends on neither concrete Providers nor Runtime. The inventory/lifecycle/scope/receipt extraction and Linux Provider are delivered. Template catalogs and artifact services remain pending. The prepared-instance macOS integration is implemented but awaits real VM qualification.

Runtime owns resource approval and passes prepared paths to the environment Manager/Providers. `pot-sandbox` does not depend on the image/resource crates. See [resource delivery](sandbox-resources.md).
