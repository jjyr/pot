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
| `pot-runtime` | Tool authorization/routing, Sandbox inventory/lifecycle, tasks/schedules, execution preferences, accounting and recovery |
| `pot-server` | Standalone startup, HTTP/SSE/MCP/PTY routing and shutdown |
| `pot-protocol` | Frontend/backend DTOs and API constants |
| `pot-app` | GPUI/native UI, protocol client and cached projections |
| `pot-eval` | Standalone deterministic and explicitly selected live evaluation |

App depends on Protocol. Server composes Runtime without App or GPUI. Runtime owns Storage, Gateway, Agent and Driver integration, and injects its Sandbox ToolExecutor into pot-agent. Core carries the backend trait without depending on Runtime. The guest executor knows neither Runtime, model providers nor Host product credentials.

Helper starts Host Server independently and owns local VM lifecycle. Execution tools run in assigned guest scopes; shell code cannot access unmounted Host product state. Tasks sharing a Sandbox use the same guest account, so separate directories are organizational rather than mutual filesystem/process isolation.

Model and native CLI requests run on the Server machine. Remote deployment, endpoint identity/TLS and artifact transfer remain outside current product scope. Tests/build commands are in [Development](development.md); results are recorded in [Host pot-agent acceptance](validation/host-pot-agent-20261006.md).
