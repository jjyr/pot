# Runtime contract

Runtime owns product commands, Agent bindings, environment routing and projection. `pot-core` defines immutable execution contracts, `pot-storage` persists messages/bindings/usage, `pot-driver` runs Host Pi/Codex, and `pot-gateway` implements provider transport/codecs. Server owns listener/route assembly. See [architecture](../docs/architecture.md) and [development](../docs/development.md).

## Configuration and startup

App starts Helper and Host Server independently of Linux VMs. Unconfigured Chat offers the shared AI Configuration page. Node, Pi and Codex ship in the bundle; installation/authorization does not require a Sandbox.

API connections are saved through `POST /api/v1/providers`, with immutable credentials in private Host `product/providers.json` and versioned profiles in SQLite. Codex uses official Host App Server browser or device authorization. Discovery is read-only and does not send model requests. Saving a connection and selecting the Main Agent are separate operations, except that first-time Chat can save both together.

`product/execution-limits.json` contains `request_limit`, `max_tokens`, `max_requests_per_turn` and `max_parallel_tasks`; it carries no credentials or implicit Agent selection. Default limits are 10,000 lifetime requests, 8,192 output tokens, 100 requests per turn and five parallel tasks. Zero lifetime requests disables new requests. Output tokens accept integers up to 9,007,199,254,740,991; per-turn/concurrency limits are 1–1,000. Changing limits restarts Host backend independently of VM lifecycle.

Only Pi and Codex engines/transports/session identities are accepted. There are no configuration importers, guest-Agent deployment routes or direct API smoke-test adapters. Removed deployment and integration records remain in Git history. Native session recovery requires the current persisted binding; it cannot silently manufacture a replacement for missing/corrupt history.

## Agent execution

Each Pi turn starts `pi --mode rpc --session <explicit file>` in a private Host Agent workspace. Pot's trusted extension overrides read/write/edit/bash/grep/find/ls, blocks shell shortcuts and sends environment operations to Runtime. Project context, unmanaged extensions and skills are disabled. Pi sees a scoped gateway token, never the provider credential.

Codex App Server runs on Host with an empty native environment list. Native shell/patch/image/code/web/hooks/apps/plugins/subagent tools and inherited MCP are disabled. Lane-scoped Pot MCP exposes the common execution protocol. Each turn gets a distinct managed MCP URL to rediscover its role's tools after native resume; the native thread identity remains fixed.

Main loadouts include task/schedule and Sandbox management. Workers receive exactly seven environment tools, a lane token and a fixed Sandbox. Summary turns can only inspect tasks. Credentials, Host data and Helper sockets are never mounted in guest workspaces.

Cancellation settles the native session and drains remote operations. Executor subprocesses run as an ordinary guest user inside root-owned cgroups, including detached descendants. Unknown outcomes quarantine the environment until explicit main-only recovery; commands are never replayed automatically. Quit settles Host work and stops VMs. Artifacts and native/product history persist.

## Environment selection and lifecycle

Main may create/start/stop/delete/select/recover environments or open their desktops. Inventory is limited to 16 Linux definitions. The initial selected Sandbox is `default`.

Task creation uses explicit `sandbox_id`, or snapshots the main selection when omitted. It does not allocate a VM per task or balance across environments. Three unspecified tasks share the selected Sandbox, with separate `/data/workspace/tasks/TASK_ID` directories. To split tasks, main creates definitions and passes different IDs. Selection changes through the main management tool affect future creations and main tools; existing task bindings and saved schedules remain fixed across turns/restart. The Sandbox environment sidebar selects the displayed environment only.

Agent profile routing determines Driver/model, not environment. Global/profile/provider concurrency limits determine dispatch capacity, not placement. Runtime starts a bound environment when needed and never falls back to Host or retargets a worker. Task turns and operations hold leases; stop/delete rejects occupied or quarantined environments. Definitions referenced by tasks/schedules cannot be deleted, and deleted IDs cannot be reused. Deletion retains workspace artifacts.

Tasks in one Sandbox share its guest account. Separate directories do not establish mutual filesystem/process isolation. Background descendants are drained at operation completion; persistent services are not an execution contract. Desktop starts explicitly and stops with its environment.

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
| `POST /api/v1/authorization/start` | Native-only official Codex authorization |
| `GET /api/v1/authorization/status` | Attempt status and verified model options |
| `POST /api/v1/authorization/cancel` | Settle current attempt |
| `GET /api/v1/agents/tools`, `POST /api/v1/agents/refresh` | Explicit supported executable/status discovery |
| `GET/POST /api/v1/agent` | Binding/profile inspection and versioned settings |
| `POST /api/v1/agent/migrate` | Explicit Driver/profile handoff at a safe turn boundary |
| `GET/POST /api/v1/agent/routing` | Worker profile defaults, model-visible allowlist and concurrency policies |
| `GET/POST /api/v1/sandboxes` | Main/operator inventory and typed lifecycle actions |
| `POST /api/v1/execution` | Active lane `{name,call_id,args}`; fixed worker scope, durable receipts |
| `POST /api/v1/mcp`, `POST /api/v1/orchestration` | Role-scoped shared product tool dispatcher |
| `/gw/{provider}/{*path}` | Internal lane-authorized provider gateway |
| `POST /api/pi/compactions` | Active-lane compaction lifecycle, no cross-lane writes |
| `/api/v1/tasks`, `/api/v1/tasks/query`, `/api/v1/tasks/{id}` | Main task creation, bounded list/search and scoped detail |
| `/api/v1/tasks/{id}/messages`, `/cancel` | Persistent guidance and per-lane cancellation |
| `GET/POST /api/v1/schedules` | Durable schedules, occurrences and user actions |
| `/api/v1/usage`, `/summary`, `/policy`, `/providers`, `/resume`, `/cancel` | Request/token accounting and allowance policy/recovery |
| `/api/v1/terminals`, `/{id}/socket`, `/{id}/close` | Native/operator Sandbox PTY; no authentication command injection |

## Tasks, profiles and schedules

Every task has a private product transcript/native session and persisted Sandbox binding. Guidance receipts are durable and consumed in order. FIFO dispatch respects configured concurrency and cooperative resource locks; one lane cannot cancel another. Restart marks already-started work interrupted rather than replaying it. Automatic completion summaries are bounded/deduplicated and wait while main is busy. Archive/search do not expand the default model context.

Explicit task profiles take precedence over configured worker defaults. With no explicit worker default, new tasks snapshot the active Main Agent. Saved profile changes do not rewrite existing task bindings. Explicit Change Agent uses transactional handoff to a new epoch; it changes Driver/model while preserving the task's Sandbox assignment. Events are filtered by owner/epoch/turn to reject late native updates.

Schedules persist an environment at save time; each occurrence resolves its Agent profile independently. Occurrence dispatch and deduplication are atomic, nonoverlapping and bounded; catch-up/skip, time zones and change notifications are documented in [scheduled tasks](../docs/scheduled-tasks.md).

## Gateway, accounting and storage

Gateway validates the active immutable provider/model snapshot and allow-listed HTTPS endpoint (HTTP allowed only for loopback). It reserves allowances before networking, disables retries/redirects and preserves the selected Chat/Responses/Messages/Gemini wire format. Missing usage remains unknown; unsupported protocols disable exact budget/usage/checkpoint claims. API request accounting and native Codex turn accounting are separate. See [token usage](../docs/token-usage.md).

Product SQLite/usage/native sessions live on Host in `root/product`; guest mounts contain artifacts only. SQLite uses DELETE journal and synchronous=EXTRA. Private JSON writes fsync data and parent directory. Inspect live state through HTTP and SQLite only when offline.

## Verification

```sh
mise run check
mise run test
mise run test:app
mise run build
mise run bundle
mise exec -- python scripts/tests/sandbox-runtime.py --helper .local/d1/Pot.app/Contents/Helpers/PotHelper --resources .local/d1/Pot.app/Contents/Resources/runtime
```

Real CLI/VM tests use isolated Host homes and local mock models, require Apple Virtualization privileges, and make no paid model calls. See [dated acceptance](../docs/validation/agent-sandbox-20261006.md) for tested scope. Real-provider login/inference and full interactive UI matrices require separate acceptance.

Sandbox management also supports `update` with `name` and `config` (`cpus`, `memory_gib`, `disk_gib`). Updates require a stopped/failed environment without active leases or unsettled operations; resources apply on the next start. Inventory exposes per-environment configuration, running-task counts, associated tasks and durable-reference status for the native UI.
