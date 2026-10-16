# Control service contract

Control owns product commands, Thread configuration, execution target routing and projection. `pot-core` defines immutable execution contracts, `pot-storage` persists messages, `pot-assistant` owns Threads, `pot-usage` owns accounting, `pot-agent` runs the Host Agent, and `pot-gateway` implements Provider transport and registry; `pot-model-protocol` owns codecs. Server owns listener/route assembly. See [architecture](../../docs/architecture.md) and [development](../../docs/development.md).

## Configuration and startup

App starts Helper and Host Server independently of Linux VMs. Unconfigured Chat offers Provider setup followed by explicit assistant creation. pot-agent is compiled into the Host Server; Node ships in the bundle for JavaScript tools and plugins; configuration does not require a Sandbox.

API connections are saved through `POST /api/v1/providers`, with Provider metadata and encrypted credential values in Control `product/control.sqlite` and versioned profiles in SQLite. API metadata checks are read-only and do not send model requests. Saving a connection only updates the shared Provider; assistant registration and selection are always explicit.

`product/execution-limits.json` contains `request_limit`, `max_tokens`, `max_requests_per_turn` and `max_parallel_tasks`; it carries no credentials or implicit Agent selection. Default limits are 10,000 lifetime requests, 8,192 output tokens, 100 requests per turn and eight parallel tasks. Zero lifetime requests disables new requests. Output tokens accept integers up to 9,007,199,254,740,991. The per-turn request limit is 1–1,000; task concurrency accepts 1 through the platform’s `usize::MAX`. The App UI retains its 1–1,000 concurrency input range. Changing limits restarts Host backend independently of VM lifecycle.

Control injects in-memory services into pot-agent. Snapshot/event schema version is 2: `agent` status contains model, phase (`needs_configuration`, `ready`, `failed`) and detail, without external-engine installation/authentication fields. Each owner Store persists one Thread configuration and its execution context; there are no Engine, Binding, NativeRef, DriverHost or SessionHandle adapters. Context persistence uses the same SQLite durability as product messages. Configuration changes preserve messages, tool results, pending calls and compaction summaries. Corrupt context fails visibly. Only current data schemas are accepted; historical data and plaintext Provider files are not imported.

## Agent execution

pot-agent runs in-process on Host with an injected ToolExecutor and owner services;
there is no implicit local backend. Control owns [machine permission policy](../../docs/permissions.md);
pot-agent owns [tool composition](../../docs/tool-composition.md). Control resolves
machine policy for each assistant turn and each Task dispatch. Sandbox is the
default; Host access is independently configured as ask, allow or deny. A Task may
request a single Host operation on its execution machine through Control's exact
operation approval without changing its normal execution context.

Main manages Tasks and schedules. Runtime manages shared Sandbox lifecycle;
models do not create or share containers. Guest workspaces contain artifacts,
not Control credentials or Helper sockets. Agent context, compaction and usage
remain owned by the assistant Runtime.

## Machine selection and execution lifecycle

Task and schedule creation accept `machine_id`. Omission selects the local machine
subject to message routing constraints. Tasks read current policy at dispatch;
schedules create a new Task on every occurrence. The recorded `execution_target`
is the actual run context, not a persistent selection for future work.

Sandbox Tasks receive separate work directories and process/session scopes inside
one shared machine environment. CPU/memory limits apply to that environment;
Task concurrency is controlled separately. A run lease spans model/tool waits.
Completion/cancellation drains only that execution. Installed software, Rust
caches and the GUI remain available. Explicit stop preserves the writable layer;
owner rebuild resets it. Ended Tasks cannot reopen; rerun creates a new Task.

Prepared macOS VMs remain operator-managed. No missing-provider failure falls
back to Host. Agent model/profile routing and execution-machine placement are
independent. See [permissions](../../docs/permissions.md),
[Task lifecycle](../../docs/task-coordination.md) and
[command execution](../../docs/executor.md).

## Control API

Bearer/boot/port are published in mode-0600 `product/runtime.json`. Browser Origins and worker tokens cannot call native-only account authorization or management routes.

| Route | Contract |
|---|---|
| `GET /api/v1/snapshot` | Messages, durable cursor, engine/turn/budget state |
| `GET /api/v1/events?after=N` | Persisted replay, authoritative snapshot, live SSE; refetch after lag |
| `POST /api/v1/messages` | `{request_id, conversation_id:"main", text}`; idempotent identical retries, conflicting reuse/concurrent turns rejected |
| `POST /api/v1/turn/cancel` | `{request_id, user_message_id}`; rejects stale cancellation |
| `GET /api/v1/providers` | Shared Provider connections, default Provider and model metadata, no secrets |
| `POST /api/v1/providers` | Versioned connection save; empty key retains only the same profile/endpoint credential |
| `POST /api/v1/providers/default` | Select the configured Provider used to initialize new assistant forms |
| `POST /api/v1/providers/check` | Bounded supported metadata/model-list GET, no inference, redirects or upstream bodies exposed |
| `GET/POST /api/v1/agent` | Shared Provider definitions, owner Thread, editable assistant selection and pending configuration |
| `GET/POST /api/v1/assistants` | Directory and explicit creation with request ID, name, concrete `configuration` and optional instruction text |
| `POST /api/v1/assistants/{id}/definition` | Versioned name/instructions and assistant-owned `configuration`; identical acknowledged retries are idempotent |
| `POST /api/v1/agent/config` | Copy a profile configuration into an existing Thread at a safe turn boundary |
| `GET/POST /api/v1/agent/routing` | Provider/model concurrency policies; routing is autonomous |
| `GET/POST /api/v1/sandboxes` | Main/operator inventory and typed lifecycle actions |
| `GET/POST /api/v1/permissions` | Machine policy, exact Host approvals and Runtime resource budgets |
| `POST /api/v1/execution` | Active lane `{name,call_id,args}`; fixed worker scope, durable receipts |
| `POST /api/v1/mcp`, `POST /api/v1/orchestration` | Role-scoped shared product tool dispatcher |
| `/gw/{provider}/{*path}` | Internal lane-authorized provider gateway |
| `POST /api/agent/compactions` | Active-lane compaction lifecycle, no cross-lane writes |
| `/api/v1/tasks`, `/api/v1/tasks/query`, `/api/v1/tasks/{id}` | Main task creation, bounded list/search and scoped detail |
| `/api/v1/tasks/{id}/messages`, `/interrupt` | Persistent guidance and interruption preserving pending guidance |
| `/api/v1/tasks/{id}/cancel` | Explicit task abandonment; discards guidance, marks cancelled and archives after cleanup |
| `GET /api/v1/assistants/{owner}/tasks/{id}/files` | Owner-only task workspace chunk; query `path`, `offset`, `revision`; response `{size,offset,revision,data}` with base64 data, 192 KiB chunks, 1 GiB maximum; worker/cross-owner access rejected |
| `GET /api/v1/assistants/{owner}/tasks/{id}/file-download` | Binary attachment stream, query `path`, optional `revision`; task-bound local or Control-relayed execution workspace; missing/offline/changed files fail, no Agent tool invocation |
| `GET/POST /api/v1/schedules` | Durable schedules, occurrences and user actions |
| `/api/v1/usage`, `/summary`, `/policy`, `/providers`, `/resume`, `/cancel` | Request/token accounting and allowance policy/recovery |
| `/api/v1/terminals`, `/{id}/socket`, `/{id}/close` | Native/operator Sandbox PTY; no authentication command injection |

Assistant `configuration` contains `provider_id` (the shared connection Profile
ID), a concrete `model`, and optional `context_window`, `max_output_tokens` and
`reasoning_effort`. Editing it never rewrites the Provider definition. Creation
uses the destination Space's defaults in the form and submits the concrete
selection. Provider saves persist their definition, model list, default identity
and hashed request receipt together, without changing any Thread.

## Tasks, profiles and schedules

Every task has a private product transcript and records its actual Host/Sandbox
execution context when dispatched.
Remote Task Agent context and run journals live on the execution Runtime; Control
keeps the task transcript, launch intent and terminal result. Guidance receipts are durable and consumed in order. FIFO dispatch respects configured concurrency and cooperative resource locks; one lane cannot cancel another. Local restart marks started work interrupted. Control restart reconciles the
original remote run; execution Runtime restart waits for receipts and interrupts
it; retry requires a new Task. Neither path replays external effects. Automatic completion summaries are bounded/deduplicated and wait while main is busy. Archive/search do not expand the default model context. See [Task coordination](../../docs/task-coordination.md) for guidance, messages, waiting, stopping and archive semantics.

Assistants choose task Provider/model routes autonomously, guided by their AGENTS.md and the available catalog, without a user allowlist or routing confirmation. Omitted task selection snapshots the invoking assistant's active configuration. Saved profile changes do not rewrite existing task model settings or plugin selections. Before each execution, selected plugin names resolve to the current implementations in the owning assistant’s source Profile; Skills synchronize by name as described in [Agent context](../../docs/agent-context.md). Explicit Change Agent copies a new configuration into the same Thread at a safe turn boundary, preserving its complete context and Sandbox assignment. The configuration update and pending-update removal commit together. Events and permission requests are filtered by owner/generation/turn to reject stale updates. Provider-specific replay is retained in stored history and sent only to its original provider/model/protocol; other routes receive portable text and paired tool calls/results.

Schedules persist an execution target at save time; each occurrence resolves its Agent profile independently. Occurrence dispatch and deduplication are atomic, nonoverlapping and bounded; catch-up/skip, time zones and change notifications are documented in [scheduled tasks](../../docs/scheduled-tasks.md).

## Gateway, accounting and storage

Gateway validates the active immutable provider/model snapshot and allow-listed HTTPS endpoint (HTTP allowed only for loopback). It reserves allowances before networking, disables retries/redirects and preserves the selected Chat/Responses/Messages/Gemini wire format. Missing usage remains unknown; unsupported protocols disable exact budget/usage/checkpoint claims. All model usage is accounted per API request. See [token usage](../../docs/token-usage.md).

Control product SQLite/usage/Thread configuration live in `root/product`.
Destination Task Agent journals retain context and results without an assistant
database or provider credentials; guest mounts contain task files and the supplied
read-only resources. SQLite uses DELETE journal and synchronous=EXTRA. Private JSON writes fsync data and parent directory. Inspect live state through HTTP and SQLite only when offline.

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

Sandbox management also supports `update` with `name` and `config` (`cpus`, `memory_gib`, `disk_gib`). Updates require a stopped/failed execution target without active leases or unsettled operations; resources apply on the next start. Inventory exposes per-execution target configuration, running-task counts, associated tasks and durable-reference status for the native UI.

## Runtime resources and task bindings

Identity and process authority exist independently of product services. Startup is
neutral; callers create assistants, Host execution and Sandbox definitions through
the [resource API](../../docs/deployment.md#runtime-resources). The durable manifest
restores these resources. App creates its initial assistant through the same API.

Conversation, Task, Agent and Execution target service state remain separate. Typed
Execution target references are the execution target; Assignment stores run/owner and
workspace bindings. Assistant and local Task adapters submit fixed context and
services to the local Runtime execution manager. Remote Tasks deliver a launch to
the destination Runner adapter, which submits to that destination's same manager
and persists the terminal result. Control never invokes the Agent engine directly.
Control's task admission, provider budgets and shared-execution target rules are business
policy; Runtime separately limits actual concurrent Agents on its machine.
