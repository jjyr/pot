# Runtime contract

Runtime owns product commands, Thread configuration, environment routing and projection. `pot-core` defines immutable execution contracts, `pot-storage` persists messages/contexts/usage, `pot-agent` runs the Host Agent, and `pot-gateway` implements provider transport/codecs. Server owns listener/route assembly. See [architecture](../../docs/architecture.md) and [development](../../docs/development.md).

## Configuration and startup

App starts Helper and Host Server independently of Linux VMs. Unconfigured Chat offers the shared AI Configuration page. pot-agent is compiled into the Host Server; Node ships in the bundle for JavaScript tools and plugins; configuration does not require a Sandbox.

API connections are saved through `POST /api/v1/providers`, with immutable credentials in private Host `product/providers.json` and versioned profiles in SQLite. API metadata checks are read-only and do not send model requests. Saving a connection and selecting the Main Agent are separate operations, except that first-time Chat can save both together.

`product/execution-limits.json` contains `request_limit`, `max_tokens`, `max_requests_per_turn` and `max_parallel_tasks`; it carries no credentials or implicit Agent selection. Default limits are 10,000 lifetime requests, 8,192 output tokens, 100 requests per turn and eight parallel tasks. Zero lifetime requests disables new requests. Output tokens accept integers up to 9,007,199,254,740,991. The per-turn request limit is 1–1,000; task concurrency accepts 1 through the platform’s `usize::MAX`. The App UI retains its 1–1,000 concurrency input range. Changing limits restarts Host backend independently of VM lifecycle.

Runtime directly invokes pot-agent. Snapshot/event schema version is 2: `agent` status contains model, phase (`needs_configuration`, `ready`, `failed`) and detail, without external-engine installation/authentication fields. Each owner Store persists one Thread configuration and its execution context; there are no Engine, Binding, NativeRef, DriverHost or SessionHandle adapters. Context persistence uses the same SQLite durability as product messages. Configuration changes preserve messages, tool results, pending calls and compaction summaries. Corrupt context fails visibly. No legacy conversation, configuration or usage importer is provided; use a fresh Host root.

## Agent execution

pot-agent runs in-process on Host with an injected ToolExecutor and owner services;
there is no implicit local backend. It owns [permission policy](../../docs/permissions.md)
and [tool composition](../../docs/tool-composition.md). Main defaults to Host; Workers
default to their assigned Sandbox and may request authorized Host execution.
Agent context, compaction, usage and preferences remain Host-owned.

Main manages Tasks, schedules and Sandboxes; Workers cannot delegate, manage or
switch Sandboxes. Summary turns inspect Tasks only. Guest workspaces contain
artifacts rather than credentials or Helper sockets. Owner-reviewed Host mappings
are visible to every Task sharing the Sandbox.

Cancellation settles the active turn and drains remote operations. Executor subprocesses run as an ordinary guest user inside root-owned cgroups, including detached descendants. Unknown outcomes quarantine the environment until explicit main-only recovery; commands are never replayed automatically. Quit settles Host work and stops VMs. Artifacts and Thread history persist.

## Environment selection and lifecycle

Main may create/start/stop/delete/select/recover environments or open their desktops. Inventory is limited to 16 Linux definitions. The initial selected Sandbox is `default`.

Task creation uses explicit `sandbox_id`, or snapshots the main selection when omitted. It does not allocate a VM per task or balance across environments. Three unspecified tasks share the selected Sandbox, with separate `/data/workspace/tasks/TASK_ID` directories. To split tasks, main creates definitions and passes different IDs. Selection changes through the main management tool affect future creations and main tools; existing task bindings and saved schedules remain fixed across turns/restart. The Sandbox environment sidebar selects the displayed environment only.

Agent profile routing determines provider/model, not environment. Global/profile/provider concurrency limits determine dispatch capacity, not placement. Runtime starts a bound environment when needed and never falls back to Host or retargets a worker. Task turns and operations hold leases; stop/delete rejects occupied or quarantined environments. Definitions referenced by tasks/schedules cannot be deleted, and deleted IDs cannot be reused. Deletion retains workspace artifacts.

Tasks in one Sandbox share its guest account. Separate directories do not establish mutual filesystem/process isolation. Command groups are drained on completion/timeout; the owner/session cgroup drains all descendants, including detached setsid processes, on cancellation, failure or shutdown. Pipe/PTY sessions survive normal turns, retain their original Sandbox and Thread execution generation, and expire when idle. Empty runners keep shell initialization caches without execution leases. See [command execution](../../docs/executor.md) for shell/login, PTY controls, explicit timeout and snapshot policies. The HTTP executor currently batches output per execution/poll response; use yield_time_ms and write_stdin for bounded progress polling. Desktop starts explicitly and stops with its environment.

## Control API

Bearer/boot/port are published in mode-0600 `product/runtime.json`. Browser Origins and worker tokens cannot call native-only account authorization or management routes.

| Route | Contract |
|---|---|
| `GET /api/v1/snapshot` | Messages, durable cursor, engine/turn/budget state |
| `GET /api/v1/events?after=N` | Persisted replay, authoritative snapshot, live SSE; refetch after lag |
| `POST /api/v1/messages` | `{request_id, conversation_id:"main", text}`; idempotent identical retries, conflicting reuse/concurrent turns rejected |
| `POST /api/v1/turn/cancel` | `{request_id, user_message_id}`; rejects stale cancellation |
| `GET /api/v1/providers` | Selected and saved connection metadata, no secrets |
| `POST /api/v1/providers` | Versioned connection save; `follow_main:false` retains current selection; empty key retains only the same profile/endpoint credential |
| `POST /api/v1/providers/check` | Bounded supported metadata/model-list GET, no inference, redirects or upstream bodies exposed |
| `GET/POST /api/v1/agent` | Thread/profile inspection and versioned settings |
| `POST /api/v1/agent/config` | Copy a profile configuration into an existing Thread at a safe turn boundary |
| `GET/POST /api/v1/agent/routing` | Worker profile defaults, model-visible allowlist and concurrency policies |
| `GET/POST /api/v1/sandboxes` | Main/operator inventory and typed lifecycle actions |
| `GET/POST /api/v1/permissions` | Owner approval decisions, persisted grants and revocation; no worker administration |
| `POST /api/v1/execution` | Active lane `{name,call_id,args}`; fixed worker scope, durable receipts |
| `POST /api/v1/mcp`, `POST /api/v1/orchestration` | Role-scoped shared product tool dispatcher |
| `/gw/{provider}/{*path}` | Internal lane-authorized provider gateway |
| `POST /api/agent/compactions` | Active-lane compaction lifecycle, no cross-lane writes |
| `/api/v1/tasks`, `/api/v1/tasks/query`, `/api/v1/tasks/{id}` | Main task creation, bounded list/search and scoped detail |
| `/api/v1/tasks/{id}/messages`, `/interrupt` | Persistent guidance and interruption preserving pending guidance |
| `/api/v1/tasks/{id}/cancel` | Explicit task abandonment; discards guidance, marks cancelled and archives after cleanup |
| `GET/POST /api/v1/schedules` | Durable schedules, occurrences and user actions |
| `/api/v1/usage`, `/summary`, `/policy`, `/providers`, `/resume`, `/cancel` | Request/token accounting and allowance policy/recovery |
| `/api/v1/terminals`, `/{id}/socket`, `/{id}/close` | Native/operator Sandbox PTY; no authentication command injection |

## Tasks, profiles and schedules

Every task has a private product transcript, Agent context and persisted Sandbox assignment. Guidance receipts are durable and consumed in order. FIFO dispatch respects configured concurrency and cooperative resource locks; one lane cannot cancel another. Restart marks already-started work interrupted rather than replaying it. Automatic completion summaries are bounded/deduplicated and wait while main is busy. Archive/search do not expand the default model context. See [Task coordination](../../docs/task-coordination.md) for guidance, messages, waiting, stopping and archive semantics.

Explicit task profiles take precedence over configured worker defaults. With no explicit worker default, new tasks snapshot the active Main Agent. Saved profile changes do not rewrite existing task configurations. Explicit Change Agent copies a new configuration into the same Thread at a safe turn boundary, preserving its complete context and Sandbox assignment. The configuration update and pending-update removal commit together. Events and permission requests are filtered by owner/generation/turn to reject stale updates. Provider-specific replay is retained in stored history and sent only to its original provider/model/protocol; other routes receive portable text and paired tool calls/results.

Schedules persist an environment at save time; each occurrence resolves its Agent profile independently. Occurrence dispatch and deduplication are atomic, nonoverlapping and bounded; catch-up/skip, time zones and change notifications are documented in [scheduled tasks](../../docs/scheduled-tasks.md).

## Gateway, accounting and storage

Gateway validates the active immutable provider/model snapshot and allow-listed HTTPS endpoint (HTTP allowed only for loopback). It reserves allowances before networking, disables retries/redirects and preserves the selected Chat/Responses/Messages/Gemini wire format. Missing usage remains unknown; unsupported protocols disable exact budget/usage/checkpoint claims. All model usage is accounted per API request. See [token usage](../../docs/token-usage.md).

Product SQLite/usage/Thread contexts live on Host in `root/product`; guest mounts contain artifacts only. SQLite uses DELETE journal and synchronous=EXTRA. Private JSON writes fsync data and parent directory. Inspect live state through HTTP and SQLite only when offline.

## Verification

```sh
mise run check
mise run test
mise run test:app
mise run build
mise run bundle
mise exec -- python scripts/tests/sandbox-runtime.py --helper .local/d1/Pot.app/Contents/Helpers/PotHelper --resources .local/d1/Pot.app/Contents/Resources/runtime
```

Real CLI/VM tests use isolated Host homes and local mock models, require Apple Virtualization privileges, and make no paid model calls. See [dated acceptance](../../docs/validation/host-pot-agent-20261006.md) for tested scope. Real-provider login/inference and full interactive UI matrices require separate acceptance.

Sandbox management also supports `update` with `name` and `config` (`cpus`, `memory_gib`, `disk_gib`). Updates require a stopped/failed environment without active leases or unsettled operations; resources apply on the next start. Inventory exposes per-environment configuration, running-task counts, associated tasks and durable-reference status for the native UI.
