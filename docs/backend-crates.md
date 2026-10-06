# Backend crate split (Issue #5)

Updated: 2026-10-06. This change preserves product APIs, data formats, session and allowance semantics while adjusting module boundaries. Remote backends are an architectural extension; no remote connection settings or deployment feature are provided.

## Target structure

```text
App / GPUI / Swift ---- HTTP/SSE ----> Server process
                                         |
                               config / routes / hosting
                                         |
                                         v
                                      Runtime
                            tasks / schedules / projection
                              /          |          \
                             v           v           v
                          Storage      Driver      Gateway
                          SQLite       CLI         HTTP / codecs
                             \           /
                              v         v
                                  Core
                         models / execution / tool schema
```

Compile-time dependencies (third-party libraries omitted):

```text
pot-app -------> pot-protocol <------- pot-server
                                           |
                                           v
                                       pot-runtime
                                         |---> pot-protocol
                                         |---> pot-core
                                         |---> pot-storage ---> pot-core + pot-protocol
                                         |---> pot-driver ----> pot-core
                                         +---> pot-gateway
```

Gateway has no production dependency on Storage; codec/ledger integration tests use a dev-dependency.


The Rust workspace contains `app`, `protocol`, `core`, `storage`, `driver`, `gateway`, `runtime`, and `server`. Helper continues to manage the local VM. Server does not depend on App, the Swift bridge, Helper, or GPUI.

- `pot-core`: immutable Profile/ResolvedSpec/Binding, execution requests, Driver events, and host/session contracts; no database, HTTP server, or process implementation.
- `pot-storage`: product SQLite, binding persistence, request/usage ledgers, transactions,. Preserve main/task databases, usage.sqlite layouts, and DELETE + synchronous=EXTRA.
- `pot-driver`: Host Pi RPC and Codex App Server, process cleanup, native session identity validation, and normalized events. Reports native identity/usage through a fixed ExecutionContext and narrow ExecutionFeedback interface; holds no Runtime Service or database connection.
- `pot-gateway`: model protocol codecs and provider HTTP transport. Runtime persists reservations and validates lane/turn before calling transport; actual usage returns to the persistent ledger. Driver native usage is independent of the gateway.
- `pot-runtime`: reusable backend library for product commands, tasks/schedules, execution coordination, binding migration, message projection, recovery, and result delivery. HTTP adapters keep existing contracts, while routes and startup move to Server. This change does not rewrite every handler's arguments and error types.
- `pot-server`: standalone backend binary and service modules for configuration, TCP listening, HTTP/SSE/MCP/PTY routes, endpoint publication, and lifecycle. Composes Runtime's public backend entry points without reading App configuration or depending on frontend startup.
- `pot-protocol`: stable frontend/backend DTOs and API constants; App depends only on this protocol, not Runtime/Core.
- `pot-app`: UI, native bridge, protocol client, and display caches.

The Driver adapts only supported Pi/Codex execution transports. Binding checksums validate the current immutable execution specification.

## Runtime and dependency boundaries

Server starts independently with `POT_DATA_DIR`, `POT_WORKSPACE`, and `POT_RUNTIME_BIND`. Backend file paths always belong to the Server machine; model and CLI requests run there. Model gateway/MCP loopback addresses are internal backend channels, not App addresses.

Local App Helper/VM startup and discovery remain compatible. Cross-machine deployment still needs endpoint discovery, TLS, identity configuration, and artifact transfer; this change does not claim those features are delivered. Desktop broker arbitration remains cooperative; crate separation does not create permission isolation.

Driver ExecutionContext contains turn paths, role, authorized endpoints, and narrow feedback capabilities. Runtime's feedback implementation validates native bindings and persists metering; Driver cannot access the task Hub or configuration management. Lane tokens retain existing permissions: main can call the main Control API, while worker tokens remain API-restricted. This change adds no token or guest permission isolation. Cancellation must confirm native execution has stopped; terminal text cannot replace process cleanup.

## Implementation and acceptance

1. Commit documentation first; create an Issue and independent branch/worktree.
2. Extract Core and Storage, preserving serialization and SQLite behavior.
3. Extract Driver, removing Service dependencies from DriverHost/SessionHandle while preserving epoch/turn/seq filtering.
4. Extract Gateway codecs/HTTP transport; keep persistent reservations before networking.
5. Make Runtime a library; compose and start Server independently. Update build, image, mise, and offline contract entry points.
6. Build backend and App; run domain/storage/Driver/Runtime regressions and standalone Server API startup/restart checks.

Structured tables, a global persistent event stream, routine App SSE synchronization, and a separate Desktop Controller permission domain are outside this structural refactor and remain future work.

See Backend crate validation for implementation and acceptance results.
