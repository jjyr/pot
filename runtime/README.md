# Product runtime

Rust owns the Control API, SQLite transcript, request ledger and Agent bindings.
`agent/` owns immutable profile snapshots, epoch migration, driver event filtering
and product projection; `pi.rs` owns the Pi subprocess transport. Pi (image baseline 0.87.1, not a runtime lock) owns its native JSONL session and built-in read/write/edit/
bash/grep/find/ls tools. The first MVP uses Pi with DeepSeek Flash; AGY/Codex are
not required to use it. See [MVP validation](../docs/validation/pi-mvp.md).

## Pi configuration and execution

`scripts/configure-pi` imports only DeepSeek from the host Pi configuration into
private guest `product/pi.json`. It neither prints credentials nor makes model
requests. Defaults: `deepseek-v4-flash`, 8192 output tokens/request, 100 requests/turn,
10000 cumulative requests. The output token limit accepts integers and decimal K/M suffixes (128K, 1M,
1.5M); there is no model-specific product cap, only the JSON safe integer limit.
The provider still enforces its own output limit. Explicit flags can adjust these;
re-importing never deletes reservations. Restart the App after changing config.

The Rust gateway defaults to HTTPS DeepSeek and supports configured, allow-listed
provider endpoints. It disables redirects/retries,
overrides output limits and disables thinking. Each HTTP request, including tool
continuation or a failed request, consumes an fsynced `pi-attempts/N` reservation
before networking. It rejects requests without an active turn, cancelled turns,
oversized request bodies (>16 MiB), and exhausted budgets. Actual provider usage is saved
separately. Request counts and token usage have different meanings.

Pi gets a local gateway token, not the provider key, in its private models.json.
`gateway.rs` routes only the active binding’s provider and model.
The real key is absent from its normal RPC messages, sessions and the App bundle.
This is not a separate security boundary against guest tools: Pi and Runtime
currently share the guest user; D4 will isolate Agent/controller access.

Each user turn starts `pi --mode rpc --session <explicit file>` in the workspace.
The session ID is saved in SQLite and validated on every reopen. A missing file
or changed ID fails visibly, never silently creates a replacement conversation.
The transcript is driven by message/tool events, completion waits for
`agent_settled`, and the process is disposed after each turn. Project extensions,
skills/context files and retries are disabled for this MVP. Automatic compaction
is enabled for main and worker sessions, using the same gateway and token limits.
The explicitly loaded Pot usage extension records compaction operations and tags
summary requests; provider usage is counted only once. See [Token usage](../docs/token-usage.md).
The bundled Pot task extension is explicitly loaded for the main session only.
Previous AGY/direct-API model context is not uploaded into the Pi session.

Cancellation names the target user message, so a stale command cannot cancel a
new turn. The GUI reports stopping until Pi's abort acknowledgement and process
exit; an unconfirmed shutdown disables further sends until environment restart.
Quit terminates the VM; startup preserves partial product text and marks unfinished
messages failed without replaying a prompt.

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

VM data is on virtiofs. SQLite uses DELETE journal + synchronous=EXTRA after real
restart testing exposed lost WAL updates. Do not open an active guest database
from the host; query Runtime HTTP. Inspect SQLite offline only after helper exit.
Pi session files are fsynced before binding and after process disposal.

```sh
cargo test --locked -p pot-app -p pot-runtime -p pot-protocol
POT_TEST_PI_BIN="$(command -v pi)" cargo test --locked -p pot-runtime real_pi_tools_resume_cancel_and_budget -- --ignored --nocapture
```

The second test runs the installed Pi against a local mock provider in an isolated
temporary directory. It covers tools, native resume, cancellation with child exit,
recovery, request caps and missing-session handling without paid model requests.

## Legacy adapters

When `pi.json` is absent, `deepseek.json` selects the old direct Rust smoke adapter;
otherwise Runtime falls back to AGY CLI. The smoke adapter keeps its separate
`deepseek-attempts` ledger (3 requests, 128 output tokens, restricted write_file).
Its original ledger is exhausted and must not be reset. AGY's Google login and
native engine flow remain unaccepted; no AGY or Codex claim is implied by Pi MVP.

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

Assistant API settings still edit pi.json and restart the environment. Agent settings edit Pi launch commands through the versioned API without a model call.
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
arbitrary new protocol. Responses, Messages and Gemini codecs are not present.

`pot-tools.json` is shared by the Pi extension and `POST /api/v1/mcp`. The MCP facade
implements JSON-RPC initialize/ping/tools/list/tools/call over HTTP (protocol
2025-03-26). Initialize requires an active lane and returns `Mcp-Session-Id`, bound
to that lane's current request. Subsequent calls require that header and the lane
bearer; reuse in another turn fails. Workers cannot orchestrate; summaries expose
only task list/read. Tool calls use the existing durable dispatcher receipts,
including conflicting-ID rejection. The Codex driver unsubscribes after resource settlement and resumes at the next turn, forcing MCP initialization with the new turn identity. This passed real CLI tests in an offline ARM64 guest.

DriverHost/SessionHandle implement Pi and native Codex App Server. Codex hosts multiplex independent
threads, preserve native IDs, record cumulative usage deltas, and wait for turn termination plus
background-terminal cleanup. A missing native session is marked broken without replacement.
Settings prepare and persist a native candidate before committing a migration; CLI-owned history
is initialized with `thread/inject_items`, without a model call. Codex usage is post-hoc, not
request-level budget enforcement. Each turn explicitly restores shell/file tool feature overrides after
a read-only summary; native resume otherwise retains the previous disabled values. Snapshot labels
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
Normal Runtime shutdown closes PTYs; VM restart loses them. The native bridge
uses Authorization headers, never URL credentials or webview JavaScript tokens.
Bundled xterm 5.5.0 / fit 0.10.0 have adjacent MIT licenses. No remote UI scripts.

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

## Claude native sessions

Claude uses the original CLI's stream-json print mode, one process per turn with an
explicit UUID (`--session-id`, then `--resume`). A fresh UUID is marked unstarted
until init confirms it; a previously started session that cannot resume becomes
broken. Settings prepare checks required CLI controls without a model request.
`--system-prompt-snapshot off` keeps current lane/summary instructions effective
across resume. Strict MCP configuration carries the lane bearer; an explicit
built-in tool whitelist excludes native delegation and scheduling. Summary turns
also exclude shell and mutation tools. Guest VM is the unattended boundary.

Completion waits for process exit and an empty native background-task list after
the result. Cancellation sends SIGINT and waits; uncertain cleanup blocks further
execution on that lane. Usage comes from the final cumulative turn report, with
cache read/create tokens included in input. Missing/failed/cancelled usage remains
incomplete; request-level budget enforcement and safe checkpoint replay are disabled.
An observed `system/api_retry` with `error: authentication_failed` uses the same
SIGINT lifecycle rather than waiting through native authentication retries. After
a terminal result and resource shutdown, Runtime reports authentication required
and keeps the native binding for an explicit retry after login. Provider diagnostic
text is not copied into this error; usage remains incomplete. Synthetic assistant
API-error events also use a generic failure message instead of the provider diagnostic. Missing terminal or
unconfirmed shutdown still blocks the lane.
CLI login is `claude auth login`, and discovery uses official `claude auth status --json`.

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
Codex extra permission requests are denied by the guest-VM policy and surfaced as a
credential-free system message; permitted Pot MCP requests still use per-role authorization.
