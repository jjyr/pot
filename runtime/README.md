# Product runtime library

`pot-server` independently starts and hosts the Control API; `pot-runtime` is its reusable backend library. App depends only on `pot-protocol`. See [crate architecture](../docs/backend-crates.md) and [standalone development entry](../docs/development.md).

Runtime owns product commands and projection. `pot-storage` owns SQLite, request/usage ledgers and binding persistence; `pot-core` owns immutable models and execution contracts.
`agent/` owns epoch migration, driver event filtering and product projection;
`pot-driver` runs Pi and native Codex on Host through scoped execution contexts. Grok execution is unsupported. The guest contains an execution-only service, with no Agent loop, provider configuration or product backend. See [Host Agents and managed Sandboxes](../docs/agent-sandbox-design.md).

## Agent configuration and execution

`scripts/configure-pi` imports DeepSeek into private Host `product/pi.json`; it makes no model calls and never resets ledgers. Pi sees a scoped gateway token rather than the provider key. Gateway routes only the immutable active binding's provider/model, disables redirects/retries and reserves allowance before networking. HTTPS is required except for loopback local model endpoints. Credentials and native sessions stay on Host and are not mounted into a Sandbox.

Each Pi turn starts `pi --mode rpc --session <explicit file>` in a private Host Agent workspace. The trusted Pot extension overrides read/write/edit/bash/grep/find/ls, blocks shell shortcuts, and routes all environment operations through Runtime. Workers get seven environment tools with a fixed Sandbox; main additionally gets task/schedule and environment management tools. Summary turns can only inspect tasks. Project context, extensions and skills are disabled. Native identity/history is validated on reopen; missing history fails without silently replacing a session. Automatic compaction uses the scoped gateway and usage extension.

Codex App Server runs on Host with empty native environment lists. Native shell/patch/image/code execution, hooks, web search, apps/plugins, subagents and inherited MCP are disabled. Pot MCP provides the same environment execution protocol. Codex may expose native session metadata/goal/input and empty resource-inspection tools; those grant no Sandbox management or Host execution. Pot worker MCP loadouts contain exactly the seven environment operations.

Cancellation targets a user message, settles the native CLI, and waits for dispatched remote operations to drain. Linux cgroups contain command descendants, including `setsid`. Unknown outcomes quarantine an environment until main-only recovery confirms cancellation/termination; commands are never replayed. Quit settles Host work then stops managed VMs. Artifact storage and product/native history survive independently.

## Control API v1

Bearer token comes from mode-0600 `product/runtime.json`:

- `GET /api/v1/snapshot`: history, durable cursor, engine/turn state and budget.
- `GET /api/v1/events?after=N`: persisted changes, authoritative named `snapshot`,
  then live events. Subscription/snapshot capture are ordered with publishers.
  Five-second keepalive; lag closes the stream and the client refetches a snapshot.
- `POST /api/v1/messages`: `{request_id, conversation_id: "main", text}`.
  Retries reuse the ID. Conflicting content and concurrent turns are rejected.
  Exhausted budget returns 429 before messages are inserted.
- `POST /api/v1/turn/cancel`: `{request_id, user_message_id}`. Pi and Codex; repeat
  cancellation is safe and a stale target is rejected.
- `GET /api/v1/usage`: durable request counts and saved provider token usage.
- `POST /api/v1/engine/authenticate`: 501; GUI credential setup belongs to D2.
- `/api/pi/v1/chat/completions`: internal bearer-authenticated Pi gateway.
- `POST /api/pi/compactions`: lane-authenticated compaction lifecycle events during an active turn; no cross-lane writes.

The App consumes SSE independently of its send worker, deduplicates events,
resynchronizes gaps and preserves command errors across background updates.

## Storage and verification

Product SQLite, ledgers and sessions live on Host in `root/product`; guest artifact mounts are separate. SQLite uses DELETE journal and synchronous=EXTRA. Query live Runtime through HTTP; inspect SQLite offline only. Atomic private JSON writes fsync data and the parent directory.

```sh
mise run test
mise run test:app
mise exec -- python scripts/tests/sandbox-runtime.py --helper .local/d1/Pot.app/Contents/Helpers/PotHelper --resources .local/d1/Pot.app/Contents/Resources/runtime
```

Build/bundle first. The real Pi/Codex test uses local mock models, isolated Host homes and two actual Linux VMs; Apple Virtualization privileges are required. Old ignored guest/native-tool fixtures are historical and are not the acceptance command for this architecture.

## Managed environments

- `GET /api/v1/sandboxes`: main/operator inventory, selection, state, active leases and unsettled operation counts.
- `POST /api/v1/sandboxes`: `{action, sandbox_id, name?}`; create/start/stop/delete/select/recover/desktop. Workers are rejected. Start waits for the authenticated executor. Stop/delete rejects occupied or quarantined environments.
- `POST /api/v1/execution`: lane-authenticated `{name,call_id,args}` during an active turn. Main may select a target; a worker override must match its persisted assignment. Native adapters use this API or the equivalent MCP dispatcher.
- `CreateTask.sandbox_id` and `ScheduleSpec.sandbox_id`: omitted creation values resolve to main selection and persist. Task bindings cannot change across turns/restart. Deletion rejects durable references and permanently retires its ID; artifacts are retained.

## Legacy adapters

When `pi.json` is absent, `deepseek.json` selects the old direct Rust smoke adapter;
otherwise Runtime requires an explicitly selected Agent profile. The smoke adapter keeps its separate
`deepseek-attempts` ledger (3 requests, 128 output tokens, restricted write_file).
Its original ledger is exhausted and must not be reset. Antigravity and Claude CLI adapters are removed. Their old configuration values are unsupported; no compatibility migration or conversion is provided. Historical messages and usage are retained.

## Web access

Pi can use its enabled bash tool with the guest's curl/Python and outbound network.
The appended Pot system instructions ask it to retrieve current information and
cite sources before claiming it cannot browse. A site may still return 403 or a
browser challenge; this is distinct from network unavailability. Dedicated web
search and browser-control tools are not yet in Pi's configured tool list.

Background concurrency is configured by `max_parallel_tasks` in pi.json (default 5 for existing configs without the field, range 1–1000). Settings → Agent exposes this separately from per-turn model requests. Excess tasks queue FIFO; the main chat keeps an independent slot and shared-resource tasks remain serialized. Apply restarts the environment after active-task checks. The importer accepts `--parallel-tasks`.

## Automatic task routing

The main Pi agent chooses direct replies for short questions and delegates research,
current-data retrieval, reports and multi-step execution through `pot_create_task`.
`pot_list_tasks`, `pot_get_task`, `pot_guide_task` and `pot_cancel_task` support reuse,
inspection, queued follow-ups and cancellation. A task receives only its explicit
brief, not the main chat history. Workers have the built-in tools and cannot delegate.
This is model-driven routing, not a keyword classifier or a guarantee that every
model response makes the optimal split.

`POST /api/v1/orchestration` accepts `{name, call_id, args}` only with the main
Runtime token during an active non-cancelled main turn. Tool receipts scope Pi call
IDs by main request, reject conflicting arguments, and reuse durable task/input IDs.
The existing FIFO scheduler enforces configured concurrency (default 5), resource
leases and per-lane sessions. Worker bearer tokens cannot invoke the route. These
are API boundaries, not guest-user filesystem isolation.

Completion report cards and task state commit together. An idle-main dispatcher
batches up to 8 unseen reports, atomically records delivery with a System turn,
and resumes the main Pi session for synthesis. User and synthesis turns share one
slot. Synthesis only has task list/read tools, and its API rejects task mutations.
A busy main turn or exhausted budget leaves notifications pending. On the first
upgrade, historical report cards are marked seen without spending model requests.

Unclaimed notifications survive restart. A claimed but interrupted synthesis is
marked failed and is not replayed automatically, preventing duplicate autonomous
work/spend; the original report remains visible and the user can request a summary.
Guidance waits for the current worker turn; dependency DAGs, recursive workers and
automatic side-effect retries are not implemented.

Offline end-to-end verification (real installed Pi, local mock model, no paid calls):

```sh
POT_TEST_PI_BIN="$(command -v pi)" cargo test --locked -p pot-runtime real_pi -- --ignored
```

## Task state, archive views and bounded search

Task phases are `queued`, `running`, `waiting` (token allowance), `complete`, `failed`, `cancelled`,
`interrupted`. Archive is a view over complete/cancelled tasks, not deletion or a
replacement execution phase. A follow-up queues the same task/session again.
Failed/interrupted tasks remain in the attention view. Updated timestamps sort
recently changed tasks first, with stable ID tie-breaking; old records fall back
to creation time without migration.

`GET /api/v1/tasks/query` and `pot_list_tasks` accept:

- `scope`: `active` (default), `attention`, `archived`, `all`.
- `phase`: optional exact phase; overrides scope when supplied.
- `q`: case-insensitive title or ID substring (max 256 bytes).
- `limit`: default 10, range 1–50; `offset`: default 0.

The response contains `items`, filtered `total`, effective `offset`, `next_offset`,
and global `counts` for all three scopes. Out-of-range pages clamp to the last
page after tasks move between scopes. UI uses 20 per page and the optional
`selected` parameter to retain metadata for a conversation outside that page.
The original unpaged `GET /api/v1/tasks` remains for older diagnostics; App and
Agent now use the bounded query path. `pot_get_task` includes execution metadata
and bounded recent messages, making completed versus failed results explicit.

Main prompt additions contain counts and at most eight active/attention metadata
entries, no archived titles/results and no worker transcript reads. Archive
results are fetched on demand. This reduces new prompt injection/tool output;
it does not remove older messages already in Pi's persistent session history.
The current 1000-task storage cap and in-memory task hub remain; pagination bounds
network/render/model payloads, not the total runtime storage cost.

## Scheduled tasks

The Runtime owns a durable scheduler; each occurrence enters the existing task queue with a fresh Pi session. Main-only tools manage plans, and the task page provides a plan editor and paginated run history. Plans and queue insertions persist together in `tasks.v2`, with migration from `tasks.v1`. GET/POST `/api/v1/schedules` expose paginated reads and idempotent commands. See [scheduled task semantics and validation](../docs/scheduled-tasks.md).

## Token accounting and allowance

Request-level usage, task attribution, local calendar quotas and checkpointed Pi continuation are documented in [Token usage](../docs/token-usage.md). The shared `usage.sqlite` is owned exclusively by Runtime; task archive preserves its records. Waiting tasks release their execution slot and remain non-overlapping for scheduled plans. Only an explicitly saved checkpoint may auto-resume after restart.


## Agent profiles and epoch migration

`agent.profiles.v1` stores editable revisioned profiles. `agent.bindings.v1` stores
owner, epoch, immutable resolved spec/hash, native reference and state. The spec
pins the engine version, provider, model, credential reference, output limit,
context window, tool policy, gateway protocol and accepted capabilities. Secrets
are never copied into these records. Each actual turn records its epoch/spec hash,
provider and model in `agent.turn:<request_id>`.

Legacy `pi.session`, `pi.paused`, Pi config and task stores remain readable and are
not deleted. Native Pi headers and JSONL records are validated on resume; missing
or corrupt sessions become broken and never silently start from empty context.
Existing task snapshots survive config edits and restarts. A missing credential
route records `agent.needs_rebind`; restoring the route permits retry, or the
settings migration endpoint can explicitly choose another profile.

Authenticated Runtime endpoints (root token only):

- `GET /api/v1/agent?owner=main`: profiles, the owner's binding history, pending
  migration and credential-route rebind reason. `owner` may also be a task ID.
- `POST /api/v1/agent`: `{request_id, expected_revision, profile, follow_main, follow_worker}`.
  New profiles use revision 1 / expected_revision 0. Updates require exactly the
  next revision. `follow_main` defaults true; existing tasks remain pinned. This
  also selects the fallback profile for new workers unless `follow_worker:false`.
  An explicit routing `worker_profile` remains authoritative. A failed or repeated request does not
  silently change an existing binding.
- `POST /api/v1/agent/migrate`: `{request_id, owner, target_profile, reason}`;
  reason is `settings_changed`, `needs_rebind` or `user_requested`. Same-ID retries
  return the saved receipt; conflicting content is rejected. Retry a failed
  migration with a new request ID after fixing its cause.

Profile shape is returned by GET. Pi profiles use engine `pi`, an accepted
provider/model, an `auth_ref`, `{max_output_tokens, reasoning_effort: null}` params,
and tool_policy `{worker_orchestration:false, summary_read_only:true,
unattended:"guest_vm"}`. Only accepted engine routes can be selected. Model names
are validated by the provider, not guessed by the Runtime.

AI Configuration saves API connections and CLI launch commands through the versioned Agent/provider APIs without a model call. Chat selection is a separate explicit save; product execution limits use execution-limits.json and restart the environment independently of credentials. Legacy pi.json remains supported.
Its model/output-limit edit is imported into this same migration operation on
startup. The task-routing settings page edits/copies saved profiles, selects a main profile through
migration, and configures the worker default, tool-hint whitelist and concurrency caps.
Library-only saves use `follow_main:false, follow_worker:false`; existing bindings remain fixed.
The task conversation's Change Agent action targets only that owner through the same migration API.
Migration previews remain incomplete.

Migration persists requested → handoff_written → new_binding_opened → active.
Candidate files are fsynced before the final SQLite transaction, which also adds
the transcript marker. Failure preserves the source binding. Active turns defer
migration; waiting tasks apply it at the next turn boundary. No interrupted turn
is automatically replayed. Deterministic handoffs under `.pot/handoff/<owner>/`
include the original goal, 8 recent messages (32 KiB text bound), older IDs,
workspace/state references and uncertain-side-effect notes. Configured keys,
Runtime tokens and sensitive transcript lines are redacted. Transcript material
is explicitly untrusted data. The next real turn receives the bootstrap pointer.
Narrative generation and in-place switching are disabled.

## Provider configuration and tool facade

The default `pi.json:api_key` route retains `pi-attempts` and the old internal URL.
Additional bearer-key providers live in private root `providers.json`:

```json
[
  {
    "id": "example",
    "endpoint": "https://api.example.com/v1/chat/completions",
    "allowed_hosts": ["api.example.com"],
    "api_key": "<private credential>",
    "protocol": "openai_chat"
  }
]
```

A profile references `provider: "example"`, `auth_ref: "providers.json:example"`.
Pi receives `/gw/example/v1` as its base URL; callers cannot choose an arbitrary
upstream path or host. Production custom routes require HTTPS. The OpenAI Chat
codec clamps output and reads usage without message-format translation. Protocol
`unknown` passes JSON and response bytes through, reserves a request, leaves usage
unknown, and disables exact-budget/usage/checkpoint-resume capabilities. It is
currently useful for opaque OpenAI-shaped streams; it does not make Pi speak an
arbitrary new protocol. Responses, Anthropic Messages and Gemini codecs clamp their native output-cap fields
and read native usage; their original wire formats pass through without translation.

`pot-tools.json` is shared by the Pi extension and `POST /api/v1/mcp`. The MCP facade
implements JSON-RPC initialize/ping/tools/list/tools/call over HTTP (protocol
2025-03-26). Initialize requires an active lane and returns `Mcp-Session-Id`, bound
to that lane's current request. Subsequent calls require that header and the lane
bearer; reuse in another turn fails. Workers cannot orchestrate; summaries expose
only task list/read. Tool calls use the existing durable dispatcher receipts,
including conflicting-ID rejection. The Codex driver uses a distinct MCP endpoint configuration each turn and unsubscribes after settlement, forcing fresh discovery on native resume. This prevents a read-only summary tool list from leaking into the next main turn; current Host/VM acceptance covers that regression.

DriverHost/SessionHandle implement Pi and native Codex App Server. Codex hosts multiplex independent
threads, preserve native IDs, record cumulative usage deltas, and wait for turn termination plus
background-terminal cleanup. A missing native session is marked broken without replacement.
Settings prepare and persist a native candidate before committing a migration; CLI-owned history
is initialized with `thread/inject_items`, without a model call. Codex usage is post-hoc, not
request-level budget enforcement. Native Host environment features remain disabled on every turn. Pot MCP rediscovery restores the appropriate main/worker/summary loadout on resume. Snapshot labels
come from the persisted binding before the first turn after restart. Codex main/MCP, workers,
restart resume and settled shell cancellation passed native account acceptance; permission UI remains incomplete;
see the development plan and [Agent validation](../docs/validation/agent-layer.md).


## Terminal and explicit CLI discovery

Root bearer only, native bridge only (any browser Origin is rejected):

- `GET /api/v1/terminals`: boot ID and ephemeral session IDs/exit codes.
- `POST /api/v1/terminals`: `{cols,rows}` opens an interactive shell only.
  Legacy requests with an agent are rejected; no login command is configured or
  launched. Navigation reconnects an existing PTY without sending input. Maximum 8 retained sessions.
- `GET /api/v1/terminals/{id}/socket`: authenticated WebSocket upgrade; one viewer.
  Binary messages carry raw UTF-8 terminal bytes in either direction; client text
  messages are resize `{cols,rows}`, server text messages report `{exit_code}`.
  Frames/messages cap at 16 KiB, dimensions 1–500 columns / 1–300 rows.
- `POST /api/v1/terminals/{id}/close`: terminate the foreground/session groups and
  wait for the direct child; remove the PTY. Guest Linux also targets background
  process groups in the same session. Deliberately daemonized new sessions are
  not an isolation boundary; stronger process containment remains D4 work.

Disconnect detaches; reconnect replays up to 64 KiB in memory. No terminal output
is stored in SQLite, disk scrollback or logs; default Shell HISTFILE is disabled.
Normal Host Runtime shutdown closes PTYs; Host backend restart loses them. The native bridge
uses Authorization headers, never URL credentials or webview JavaScript tokens.
The product Terminal page and xterm assets are removed. These Runtime PTY facilities remain independent of browser authorization.

- `GET /api/v1/agents/tools`: read-only executable catalog; versioned default-profile
  invocation overrides historical catalog entries; no login-command field
  or POST editor. Invocation changes use versioned profiles instead.
- `POST /api/v1/agents/refresh`: explicit bounded discovery. Codex's official
  `login status` gets a five-second timeout and bounded, discarded output.
  Unsupported/custom status interfaces stay unknown. No credential files are
  opened, no version suite/model test runs, and discovery never changes selection.

Pi launch_spec has `transport: pi_rpc`, an executable, `cwd_policy: workspace`,
and argv entries `{kind: literal,value: ...}` plus exactly one
`{kind: runtime_arguments}`. Runtime expands its RPC/session/model/role flags at
that position; conflicting reserved flags fail validation. Prompts remain RPC
stdin input. User wrappers are direct exec. `pi` uses the Runtime's configured Pi
executable default; an explicit wrapper path overrides it. protocol_contract
requires `events: pi_rpc_v1` and `native_session: pi_jsonl_v3`. No exact CLI version
check gates turns or saves. Legacy version-bearing checksums are still verified;
new checksums exclude diagnostic engine_version. Command changes produce a
profile revision and use the existing turn-boundary migration path.

A fresh Runtime without accepted Pi/API configuration rejects chat with
`setup_required`; terminal and discovery remain available. Existing API/Pi
configuration is imported. The complete multi-backend onboarding matrix remains
listed in the development plan; terminal availability is not Driver acceptance.

## Task profile routing

`POST /api/v1/tasks` accepts optional `profile` (explicit user choice) and
`requirements: {tools, desktop, exact_budget, live_steering}` (all default false).
A schedule stores the same optional fields in its spec; each occurrence resolves
its configured profile at creation. `pot_create_task.profile` is a lower-authority
hint, restricted to the user's allow-list. Omission uses `agent.default.worker`,
then legacy `pi-default`. These entry points preserve selection precedence without
allowing tool input to masquerade as an explicit user selection. Tool-created or
updated schedule profile references use the same allow-list.

Task metadata includes `routing` with creation-time profile, revision, provider
and reason (`user_requested`, `schedule`, `tool_hint`, `worker_default`). Existing
tasks keep their immutable binding across profile edits and restart. A repeated
create request must match profile, requirements and caller source as well as text.
Migration accepts reason `routing` and checks the task's requirements again.
Desktop and live steering requirements are currently rejected; no accepted driver
advertises these capabilities. Unknown gateway protocols cannot satisfy exact budgets.

`GET/POST /api/v1/agent/routing` reads/saves the root-authorized policy:

```json
{"revision":0,"worker_profile":null,"allowed_tool_profiles":["pi-default"],"profile_limits":{"pi-default":2},"provider_limits":{"deepseek":3}}
```

Save with the last observed revision; a successful save increments it. Limits are
1–1000; omitted entries add no limit beyond global background concurrency. Limits
count active binding identities across profile revisions; pending task migrations
are applied before claiming a slot. FIFO applies among eligible tasks, so a capped
provider does not block another. Cancelling tasks retain capacity until settled;
unconfirmed engine shutdown holds the lane and all its leases until Runtime restart.
These limits apply to background workers; the main/summary lane remains separate.

## Supported native sessions

Codex uses official Host App Server authorization/session APIs. Pi and Codex are supported; Grok profile selection and authorization are rejected. Claude is API-only via the Anthropic Messages gateway codec. Removed CLI values are rejected by new profile requests. No startup compatibility migration, archive or conversion is provided for removed CLI configurations. Native account/session files are not deleted.

## Native provider codecs

`providers.json` supports `openai_chat`, `openai_responses`, `anthropic_messages`,
`gemini`, and `unknown`. Each entry has id, full HTTPS endpoint, allowed_hosts,
api_key, and protocol. Gemini's full endpoint may contain `{model}` in
`/models/{model}:streamGenerateContent`; the expanded model must match the binding.
Pi uses its corresponding native provider API through `/gw/<provider>/...`.
The gateway injects Bearer, x-api-key, or x-goog-api-key upstream; SDK local keys
are accepted only as Runtime lane authentication and are never forwarded upstream.
Codecs preserve request/response wire formats, clamp their native output field,
and normalize usage for the shared ledger. Anthropic cache reads/creation count
inside input; Gemini thought tokens count inside output. Unknown usage retains
reservations. Newly added formats do not enable automatic checkpoint resume until
that backend-specific contract has been validated.

`GET /api/v1/providers` returns the active API connection's endpoint/model/protocol
and has_key, never its key. `POST /api/v1/providers` accepts request_id (UUID),
expected_revision, endpoint, model, protocol, api_key, and optional max_output_tokens.
It creates/updates `api-default` and selects it through the existing migration
transaction. Blank key retains the current key only for the same endpoint.
Each save uses a new immutable provider ID so existing bindings keep their route;
the mode-0600 provider file is persisted before selection. A failed selection may
leave an unused private route, but cannot mutate an existing route. No model test
is sent. API-only setup works without pi.json; default Pi limits apply and its
request reservations never use the legacy direct-API smoke ledger.

### Provider allowances

`GET /api/v1/usage/providers` returns known provider routes, current policy/revision,
usage/reservation totals and enforcement precision. `POST` accepts
`{provider, revision, policy:{period,limit,timezone,auto_resume}}`; root bearer and
current revision are required. Only configured gateway routes support hard allowances.
The gateway atomically reserves tokens and associates the immutable provider route before
networking. Global and provider allowances both apply; output is capped by the smaller
available allowance. Unknown requests retain reservations after restart. A paused owner
resumes only when both policies allow it and the backend supports checkpoint resume.
Provider service rate limits and native subscription usage are separate from these policies.

Native invocation literals support user wrapper options with direct argv expansion.
Runtime-owned model, transport, session, MCP, policy and config override flags are rejected,
including attached short forms. Wrappers are user-trusted executables and must preserve
the selected protocol; incompatible output fails visibly, without transport fallback.
Codex extra permission requests are denied by the managed tool policy and surfaced as a
credential-free system message; permitted Pot MCP requests still use per-role authorization.


## AI connection management (#7)

- `GET /api/v1/providers`: selected connection plus all saved API connection metadata (`connections`, keyed by `profile_id`), never secrets; Gemini endpoint templates remain templates.
- `POST /api/v1/providers`: accepts optional `profile_id`, `name`, and `follow_main`. `follow_main: false` adds/updates a connection without selecting it; omitted fields preserve the legacy request contract. Never updates the worker default. Empty keys retain only the same profile's same-endpoint credential, even when another Agent is active.
- `POST /api/v1/providers/check`: endpoint/protocol/key or same-profile retained key; bounded supported model-list GET only, 12-second timeout, no redirects or inference. Returns models and safe status, never upstream response bodies or secrets. Custom unsupported paths permit manual configuration.
- `POST /api/v1/terminals`: optional purpose `shell` (default) or `login`, returned by list; both open a shell only, never execute authentication commands. `agent` command injection remains rejected.
- Routing supports `follow_chat: true`, which pins new tasks to the active main binding snapshot rather than an unapplied profile revision. Existing explicit defaults remain effective until changed.
- `execution-limits.json` contains request_limit, max_tokens, max_requests_per_turn and max_parallel_tasks; no credentials or model selection. Bounds match existing settings. Limits do not create an implicit legacy Pi binding.
