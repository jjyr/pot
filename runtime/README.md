# Product runtime

Rust owns the Control API, SQLite transcript, request ledger and Pi subprocess
lifecycle. Pi 0.87.1 owns its native JSONL session and built-in read/write/edit/
bash/grep/find/ls tools. The first MVP uses Pi with DeepSeek Flash; AGY/Codex are
not required to use it. See [MVP validation](../docs/validation/pi-mvp.md).

## Pi configuration and execution

`scripts/configure-pi` imports only DeepSeek from the host Pi configuration into
private guest `product/pi.json`. It neither prints credentials nor makes model
requests. Defaults: `deepseek-v4-flash`, 512 output tokens/request, 6 requests/turn,
100 cumulative requests. Explicit flags can adjust these within bounded ranges;
re-importing never deletes reservations. Restart the App after changing config.

The Rust gateway has a fixed HTTPS DeepSeek upstream, disables redirects/retries,
overrides output limits and disables thinking. Each HTTP request, including tool
continuation or a failed request, consumes an fsynced `pi-attempts/N` reservation
before networking. It rejects requests without an active turn, cancelled turns,
oversized context (>96 KB), and exhausted budgets. Actual provider usage is saved
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
skills/context files, automatic compaction and retries are disabled for this MVP.
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
