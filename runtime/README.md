# Product runtime

Rust owns the Control API, SQLite transcript, request ledger and Pi subprocess
lifecycle. Pi 0.87.1 owns its native JSONL session and built-in read/write/edit/
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

The Rust gateway has a fixed HTTPS DeepSeek upstream, disables redirects/retries,
overrides output limits and disables thinking. Each HTTP request, including tool
continuation or a failed request, consumes an fsynced `pi-attempts/N` reservation
before networking. It rejects requests without an active turn, cancelled turns,
oversized request bodies (>16 MiB), and exhausted budgets. Actual provider usage is saved
separately. Request counts and token usage have different meanings.

Pi gets a local gateway token, not the provider key, in its private models.json.
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
- `POST /api/v1/turn/cancel`: `{request_id, user_message_id}`. Pi only; repeat
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
