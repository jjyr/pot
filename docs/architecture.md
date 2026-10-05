# Current architecture

Updated: 2026-10-05. This page describes the implemented structure; see [Product design](product-design.md) for future product constraints.

```text
macOS
+---------------------------------------------+
| Pot App: GPUI + Swift bridge                 |
| chat / tasks / settings / tray / WebViews    |
+--------+----------------------+-------------+
         | Unix IPC             | bearer HTTP/SSE
         v                      |
      Helper                    |
         | Containerization     |
         v                      v
+---------------------- Linux VM ----------------------+
| pot-server: HTTP / SSE / MCP / PTY hosting             |
|      |                                               |
| pot-runtime: bindings / tasks / schedules / recovery   |
|      |-- Storage: SQLite + request/usage ledgers       |
|      |-- Driver: Pi / Codex / Claude / AGY sessions    |
|      +-- Gateway: provider HTTP + protocol codecs     |
|                                                      |
| Desktop / broker <---- App native WebView bridge      |
+------------------------------------------------------+
```

## Backend split design

Issue [#5](https://github.com/jjyr/pot/issues/5) separates Core/Storage/Driver/Gateway/Runtime/Server in an independent worktree according to the [Backend crate split](backend-crates.md). Server starts the backend independently; App accesses it through the protocol. Remote deployment is not exposed as a product feature. HTTP adapter arguments retain their existing types; APIs, storage layouts, and session semantics remain compatible.

## Responsibilities and code entry points

| Layer | Responsibility | Entry points |
|---|---|---|
| App | Chat, tasks, schedules, settings; caches and background synchronization | `app/src/main.rs`, `client.rs`, `ui.rs`, `schedules_ui.rs` |
| Native bridge | Shared NSApplication/window, input methods, tray, WKWebView, configuration writes | `app/swift/Bridge.swift`, `SettingsPanel.swift` |
| Helper | User process managing VM, resources, and normal/abnormal shutdown | `helper/` |
| Server | Standalone program, configuration, routes, listener, and lifecycle | `server/src/main.rs`, `config.rs`, `routes.rs`, `hosting.rs` |
| Runtime / Storage | Authoritative task state, message events, usage, recovery, and persistence | `runtime/src/lib.rs`, `bootstrap.rs`, `storage/src/` |
| Orchestration / scheduling | Single-level delegation, queues, completion delivery, scheduled triggers | `tasks.rs`, `orchestration.rs`, `schedules.rs` |
| Agent / Driver | Profile snapshots, epoch bindings and migration; DriverHost / SessionHandle; Pi RPC, Codex App Server, native Claude CLI and their contexts | `core/src/`, `driver/src/`, `runtime/src/agent/` |
| Gateway / tool bridge | Provider routing, request reservations, protocol codecs; shared Pi / MCP tool definitions and dispatcher | `gateway/src/`, `core/src/pot-tools.json`, `runtime/src/tool_bridge.rs`, `orchestration.rs` |
| Protocol / desktop | Shared types; guest startup, browser, and input arbitration | `protocol/`, `image/` |

GPUI and Swift share the main window and event loop, bridged through a C ABI. Standard pages reuse Kit; native NSTextView handles chat editing, while separate WKWebViews host the desktop and bundled xterm terminal. The terminal connects to Runtime PTY through Swift URLSessionWebSocketTask; bearer credentials stay in the native networking layer, and the web page cannot access the network or navigate externally. Closing the window keeps background work running; Quit closes Chromium normally before stopping Helper/VM.

The build VM preinstalls Sandbox system dependencies into an OCI image shipped with App. Helper imports and starts the local image without installing packages at startup. User data is mounted separately at `/data`. See [Sandbox image](sandbox-image.md) for desktop, fonts, and image sizes.

## Main assistant, tasks, and schedules

Main Pi chooses between a direct reply and task tools based on the goal. Runtime enforces concurrency, idempotency, and state transitions; each task has its own directory and Pi session. Workers receive explicit task instructions without copies of the main chat history and cannot recursively delegate through the orchestration API.

Background concurrency defaults to 5, with additional user-configured profile/provider limits. Runnable tasks queue FIFO; a limited provider does not block others. Main chat has a separate slot. Each task executes serially; follow-up guidance is durably queued and consumed on the next turn. `shared` is an explicit cooperative lock, not permission isolation. Completion reports persist with state; when the main session is idle, up to 8 reports are combined into a summary.

Schedules are persistent definitions; each trigger creates a new task. The scheduler owns time zones, deduplication, missed-run policy, and overlap prevention; the model checks the target and produces structured results. Runs can stay silent when nothing changes; see [Scheduled tasks](scheduled-tasks.md).

## State and recovery

- Runtime stores product messages/task state; each CLI stores native execution context. `agent.bindings.v1` stores immutable ResolvedSpec, SHA-256, epoch, and native_ref per owner. Legacy Pi metadata imports idempotently into epoch 1 while retaining old fields. Recovery uses only the binding snapshot.
- Profile settings switch main chat through a persistent migration operation, waiting for an active turn to stop. Existing tasks stay pinned by default; new tasks resolve the current worker default. External handoff and the new session are written first, then one SQLite transaction archives the old binding, activates the new one, and inserts a conversation boundary. Deterministic handoff is bounded and redacted; the next real input instructs the model to read it without an extra model call.
- Pi transport events become Driver events carrying epoch/turn/seq before projection into product records. Cancellation still waits for Pi exit; an owner cannot restart before shutdown is confirmed. Implementing an enum alone does not establish support for other drivers.
- API commands carry request IDs; message events have persistent cursors. Runtime provides SSE; App currently uses background snapshot/paginated polling (about 500 ms), rejects stale query results, and shows cached conversations first on navigation.
- SQLite uses DELETE journal + synchronous=EXTRA to avoid observed WAL loss on the shared filesystem. Inspect live state through Runtime APIs; the host must not open an active guest database.
- Tasks and schedules persist together as `tasks.v2`, with compatible reads of `tasks.v1`. Restart preserves queued work and marks running tasks interrupted; unknown external side effects are not replayed automatically.
- Token usage uses a shared SQLite request ledger for time aggregation, task attribution, and Pot periodic allowances. After Pi has confirmed shutdown, tasks without enough allowance save a recovery checkpoint, enter waiting, and release their execution slot. See [Token usage](token-usage.md).

Model requests reserve allowance durably before networking. Tool continuations and failed requests also count. Cancellation waits for Pi to settle and cannot undo completed external actions.

The [Runtime README](../runtime/README.md) maintains API, pagination, report delivery, and Pi constraints.

## Current boundaries

Current use is a local VM with Pi + DeepSeek. Remote Docker is a historical POC; Node Manager, multiple nodes, and other drivers are not productized.

The VM isolates the host, but Pi and Runtime still share the guest user domain. Worker tokens restrict APIs, not same-user file access. The desktop broker offers cooperative arbitration without full shell/CDP/X11 isolation. Dedicated browser tools, dependency DAGs, long-term memory, and service authorization remain in the [Development plan](development-plan.md).

## Terminal and command configuration

`runtime/src/terminal.rs` manages up to 8 in-memory PTYs, each with at most one connection and a 64 KiB replay buffer.
Disconnect only detaches; closing a terminal or normal Runtime termination ends the session. Terminal input/output is not stored in the product database, logs, or disk scrollback; the default shell disables HISTFILE.
Terminal and Sandbox have separate sidebar entries. Navigation and reconnect reuse the same PTY without injecting commands into a shell or busy CLI. Pot does not maintain login commands or dedicated login terminals; it only queries official login status, and users authenticate in Terminal themselves. Explicit HTTP(S) link clicks open the Mac browser; guest localhost OAuth callbacks are not forwarded automatically.

CLI and Runtime inherit the same guest user and persistent HOME; `~/.local/bin` and `~/.npm-global/bin` are added to their shared PATH.
`discovery.rs` checks executables and supported official status interfaces only on explicit calls. Identity, installation, compatibility, and readiness remain separate states.
Profile launch_spec/protocol_contract enter the immutable binding and checksum. Diagnostic versions do not enter new checksums, and version probes are not launched. Old snapshots continue to validate their original checksums; compatible upgrades do not migrate bindings merely because the version string changes.

Claude runs one original CLI process per turn and resumes with a fixed UUID. Strict MCP configuration and built-in tool allowlists
restrict tools by lane; summary and worker Runtime tokens cannot orchestrate.
After a result event, Runtime still waits for background cleanup and process exit. If shutdown cannot be confirmed, it retains occupancy and blocks new turns.
Official auth status queries only login state without reading or exporting credentials. Usage is settled from cumulative CLI turn reports.
