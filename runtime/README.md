# Product runtime contract

Updated: 2026-10-06. `pot-server` hosts the Control API; `pot-runtime` is the reusable product backend. App depends only on `pot-protocol`. See [architecture](../docs/architecture.md), [backend crates](../docs/backend-crates.md), and [development](../docs/development.md).

## Execution backends

Direct API connections use the Rust `pot-agent` model/tool loop. Supported protocols are OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, and Gemini GenerateContent. Codex App Server and Grok ACP remain external-agent drivers. Codex owns its native model/tool loop and session; Pot does not wrap it in another model loop.

Pi RPC, its JavaScript extensions, bundled npm package, configuration importer, and smoke-agent fallback have been removed. Old Pi sessions and development databases are not imported. New product and usage databases are `pot-v2.sqlite` and `usage-v2.sqlite`; existing old files are left unused. Configure API connections and select the main Agent through AI Configuration. There is no `pi.json`, host credential import, or Pi executable requirement.

`pot-agent` implements the same DriverHost/SessionHandle contract as native drivers. Every event carries owner-bound epoch, turn ID, and increasing sequence. Runtime filters stale/duplicate events and projects accepted events into product messages. A turn completes only after execution and cleanup finish.

## Pot Agent loop and tools

1. Persist the accepted input in the execution context.
2. Compact old completed turns when the conservative context estimate crosses the configured threshold.
3. Construct the provider-native request. Runtime durably reserves request/token allowance before upstream networking.
4. Display text deltas while assembling complete assistant items and tool arguments. Incomplete, truncated, filtered, or failed responses never execute tools.
5. Persist the complete assistant response and pending tool batch. Validate role authorization, record the started call, then execute tools serially.
6. Persist each result before requesting the model again. A repeated same-ID/same-arguments call within a turn reuses its saved result; conflicting reuse returns a tool error.

The first built-in tool set is:

- `exec_command`: `cmd`, optional `workdir`, `timeout_ms`, `max_output_chars`, and `yield_time_ms`. Without a yield interval it waits until exit/timeout and closes stdin. With a yield interval it returns a live `session_id`; the deadline still counts from process launch. Stdout/stderr stream into product tool messages while waiting. Shell executable, timeouts, yield intervals, session count, and output bounds come from the pinned profile. The historical `max_output_chars` argument counts bytes per output stream.
- `write_stdin`: `session_id`, optional `chars`, `close_stdin`, `yield_time_ms`, and `max_output_chars`. Sends input or polls a live pipe session; closing stdin sends EOF. Sessions belong to one execution invocation, cannot be used by another owner/turn, and are cleaned before turn completion or an allowance checkpoint. They do not survive restart/resume or support PTY job control.
- `apply_patch`: a `patch` string using Codex Begin/End markers and Add/Update/Delete File, optional Move to, `@@` context hunks, and End of File. All hunks/targets are validated before writes. Individual text-file replacements are staged, fsynced, and renamed; a multi-file patch is not a filesystem transaction. Paths must remain in the workspace, including symlink resolution.

Main sessions additionally receive the shared Pot task/schedule tools. Workers receive built-in/code-extension tools allowed by their profile and cannot delegate. Summary sessions receive only `pot_list_tasks` and `pot_get_task`. The existing turn-scoped MCP facade enforces the same dispatcher permissions and receipts for Pot Agent and Codex.

## Configuring the Pot loop in code

`Profile.params.max_output_tokens` controls the output reservation. `Profile.params.agent` is an `AgentSettings` value: context window, optional explicit compaction threshold, auto-compaction switch, provider-usage calibration switch, context reserve, optional replacement system prompt, built-in tool list, optional allowlist for all advertised tools, request timeout, and `ShellSettings`. Empty tool lists are supported. Profiles serialize through the existing Agent settings API and pin these values in the binding checksum; changing a profile takes effect through the normal turn-boundary migration rules. These settings apply to Pot Agent, not the internal loops owned by Codex/Grok. Model/provider limits, Runtime authorization, allowances, and transport bounds still apply.

```rust
profile.params.max_output_tokens = 16_384;
profile.params.agent = pot_core::AgentSettings {
    context_window: 128_000,
    compact_at: Some(100_000),
    builtin_tools: vec!["exec_command".into(), "write_stdin".into()],
    allowed_tools: Some(vec!["exec_command".into(), "write_stdin".into(), "my_tool".into()]),
    ..Default::default()
};
profile.params.agent.shell.max_timeout_ms = 3_600_000;
```

`ProfileConfig::agent_settings` can supply these defaults when constructing a profile. `pot_agent::AgentExtensions` supplies application-defined tool schemas/executors, a per-request context transformation, and token estimation. Context transformations operate on a request copy, leaving durable history intact. Tool names cannot shadow reserved built-ins or other advertised tools. Role permissions remain authoritative, and the allowlist narrows the resulting set before both advertising and execution. Summary execution does not receive code extensions. Extension code is trusted application code; JSON settings cannot install executable code or a tokenizer.

Embedders construct `AgentHost::with_extensions(Arc::new(extension))` and pass it to `pot_runtime::initialize_with_agent_host(config, Arc::new(host))`. The installed host is shared with worker services. Ordinary Server startup uses the default host. Advanced configuration/code extensions currently have no dedicated settings UI.

## Live execution control and diagnostics

Authenticated `POST /api/v1/turn/steer` and `/api/v1/tasks/{id}/steer` accept `request_id`, target `user_message_id`, and `text`. Inputs are persisted, idempotent within the target turn, and rejected for conflicting IDs, stale/stopping/settling turns, or non-Pot drivers. At most 100 inputs are accepted per turn. Agent incorporates them before the next model request, after the current assistant/tool batch settles. It saves context before acknowledging the queue; context IDs prevent duplicate injection after partial acknowledgement. An input accepted while the final response streams causes another model request. Final settlement closes steering acceptance under the same lock as enqueue. Task `/messages` and `pot_guide_task` retain their existing next-turn queue behavior.

`POST /api/v1/turn/control` and `/api/v1/tasks/{id}/control` accept target `user_message_id` and `action: pause|resume`. Pause is cooperative at the next model-request boundary; it neither interrupts the current request/tool batch nor undoes side effects. The response reports the requested action; Engine detail reports when the pause is reached. A manually paused turn keeps its worker slot, and process-session deadlines continue. Cancellation/shutdown also wake a paused turn. This differs from allowance waiting, which saves a checkpoint and releases capacity. Restart does not replay interrupted work.

`GET /api/v1/agent/diagnostics` (optional `task_id`) returns the latest 200 persisted model/tool/turn metadata events and the last requested control state. Events include binding epoch, owner, target turn, timing, tool count, failed status and receipt reuse. Provider payloads, shell arguments/output, and credentials are excluded. Tool output follows the existing authenticated product event stream. There are no new App steering/pause buttons or parallel tool execution in this revision.

## Context, cancellation, and recovery

Execution context is separate from product display messages. Storage owns a private JSON context file per binding with identity, schema version, messages, pending calls, started call, and per-turn tool receipts. Writes fsync the replacement and parent directory. Runtime validates identity and epoch through ExecutionFeedback; Agent cannot access Runtime's database or task Hub.

Native assistant replay preserves Responses output/reasoning items, Anthropic thinking/signature blocks, Gemini parts/thought signatures, and Chat Completions tool calls/reasoning content. Provider adapters translate only at request/response boundaries. Raw protocol replay is not transferred across provider migrations.

Missing/corrupt bound context fails visibly instead of silently starting an empty conversation. On a new user turn after interruption, unfinished calls receive explicit interrupted/not-executed results. A started call with no saved result has unknown effects and is never automatically replayed. The user can inspect effects before retrying.

Cancellation drops the in-flight model stream and stops active shell execution. Shell completion, timeout, cancellation, and turn settlement clean the owned process groups. Detached programs that escape those groups remain a guest isolation limitation. Failure to confirm process/output cleanup retains a failed engine state and blocks new work until restart. Cancellation cannot undo completed external actions. Quit/restart preserves partial product messages and does not replay unfinished prompts automatically.

Pot token allowance exhaustion before a request saves a stable checkpoint, releases worker slots, and waits. Resume continues from saved context without repeating completed tools. Compaction uses the same metered gateway, persists the replacement before reporting completion, and records a tagged usage subset. Default context estimation projects the provider-native request context, counting replay once and excluding display-only fields, stored usage, and generation/transport options. Without an observation it uses UTF-8 bytes conservatively; code extensions can supply a protocol/model-aware tokenizer. Complete provider usage calibrates an unchanged history prefix (input includes cache, output includes reasoning), with only subsequent context growth estimated. A fingerprint binds that calibration to model, system prompt, tools and the prepared request prefix. History edits or request-context transformations invalidate a mismatched prefix; compaction clears it explicitly. Incomplete/missing/zero usage is not used as a measured baseline. `use_provider_usage: false` selects pure local estimation. These remain context estimates, not a new billed-usage source; allowance reservations are unchanged. Every regular model request checks its prepared input plus the output reservation against the profile window before networking. Compaction needs an older user-turn boundary; oversized first-turn context fails explicitly.

## API connections and gateway

Private root `providers.json` stores immutable credential routes:

```json
[{"id":"example","endpoint":"https://api.example.com/v1/responses","allowed_hosts":["api.example.com"],"api_key":"<private>","protocol":"openai_responses"}]
```

Profile snapshots contain provider/model/credential references, parameters, tool policy and capabilities, never keys. Gemini endpoint templates substitute the selected model. Configured upstreams require allow-listed HTTPS hosts; explicit IPv4 loopback HTTP is also supported for local model services. Redirects and automatic retries are disabled. Unknown wire protocols are not supported by Pot Agent.

Agent receives a lane-scoped gateway token, not the upstream provider key. `POST /gw/{provider}/{path}` validates the active binding, role, model, cancellation state, request size (16 MiB), and execution limits. Reservations persist before networking. Native-driver usage is reported separately and does not imply request-level budget enforcement.

`execution-limits.json` independently configures request_limit, max_tokens, max_requests_per_turn, and max_parallel_tasks. Limits never select a model or create a connection. Existing request reservations are not reset by settings changes.

The Sandbox isolates the host. Agent tools and controller currently share a guest user; API lane permissions are not filesystem isolation.

## Control API

Bearer token and boot identity are published in private `runtime.json`.

- `GET /api/v1/snapshot`, `GET /api/v1/events?after=N`: persisted messages/state/cursor and ordered SSE. Lag requires a fresh snapshot.
- `POST /api/v1/messages`: request_id, conversation_id, text. Retry IDs are idempotent; conflicting content and concurrent turns are rejected.
- `POST /api/v1/turn/cancel`: request_id and target user_message_id. Stale targets cannot cancel a later turn.
- `GET/POST /api/v1/providers`, `POST /api/v1/providers/check`: save private API connections or inspect bounded model-list metadata without inference. Empty keys retain only the same profile's same-endpoint key. Saving a connection and selecting main are separate operations.
- `GET/POST /api/v1/agent`, `POST /api/v1/agent/migrate`: revisioned profiles, immutable bindings, explicit per-owner migration. Existing tasks remain pinned unless explicitly changed.
- `POST /api/v1/mcp`: turn-bound HTTP JSON-RPC initialize/ping/tools/list/tools/call (2025-03-26). A lane token and the current Mcp-Session-Id are required after initialization.
- `POST /api/agent/compactions`: lane/active-turn-scoped compaction lifecycle.
- `GET /api/v1/usage` and usage subroutes: durable accounting, policies, time aggregation, task/provider attribution, and recovery. See [token usage](../docs/token-usage.md).

## Tasks, schedules, and bindings

Runtime owns the main slot, FIFO worker scheduling, per-profile/provider concurrency, cooperative resource leases, single-level delegation, queued guidance, and result delivery. Main receives bounded task metadata; workers receive their explicit brief without main history. Idle-main result synthesis batches at most eight unseen reports and only exposes read tools. Interrupted claimed synthesis is not automatically replayed.

Task phases are queued, running, waiting, complete, failed, cancelled, and interrupted. Archive is a view, not deletion. `GET /api/v1/tasks/query` and `pot_list_tasks` accept scope, phase, title/ID search, limit (1–50), and offset. App uses paginated queries. Counts and pagination bound payloads, not total retained history.

Schedules persist with tasks in `tasks.v2`; each occurrence creates an independently bound task. Runtime handles time zones, deduplication, missed runs and overlap. See [scheduled tasks](../docs/scheduled-tasks.md).

New bindings pin a resolved spec and checksum. Profile changes defer migration until a turn boundary. An explicit switch prepares a bounded/redacted handoff, opens the target, and atomically archives/activates bindings with a product boundary marker. This ongoing product feature does not import old Pi development sessions. Native Codex candidates are prepared without inference.

SQLite uses DELETE journal + synchronous=EXTRA on the shared VM filesystem. Inspect live state through Runtime HTTP; do not open an active guest database from the host.

## Native authorization and terminals

Codex App Server and Grok use their official native authorization interfaces. Credentials remain in guest HOME; Pot does not extract CLI tokens into API profiles. Native launch_spec arguments are direct argv, with Runtime-owned flags reserved. Compatible executable upgrades do not migrate bindings merely because a diagnostic version changes.

Native-only authorization endpoints reject browser Origin. Terminal PTYs remain internal infrastructure with bounded replay and no durable transcript; they do not imply Agent readiness. Native cancellation must wait for turn settlement and subprocess/background cleanup.

## Verification

```sh
mise run test
mise run test:app
mise exec -- cargo test --locked -p pot-runtime native_agent
```

Native-agent tests use real HTTP streams and filesystem/shell tools against local mock providers, including four protocols, opaque replay fields, restart continuation, cancellation, unknown tool effects, metered compaction, and allowance checkpoints. These do not establish real-provider/account acceptance. Historical Pi validation pages remain dated historical evidence.

## Persistent execution preferences

The main Pot/Codex Agent can read and replace dedicated plain-text preferences through `pot_get_execution_preferences` / `pot_update_execution_preferences` (`text`, `expected_revision`; empty clears). Workers and summaries cannot mutate them. Per-turn context carries current text/revision and a credential-free eligible Profile catalog. Task tool requests may carry `preference_revision` and `selection_reason`; nonempty preferences require a fresh revision. Stored task routing metadata preserves the decision, and `executor_icon` follows the actual current binding. These tools do not grant routing authorization or add a classifier-model call. Grok currently lacks these orchestration tools. See [Execution preferences](../docs/execution-preferences.md).

Usage summary `routing_total` is a durable, deduplicated subset of whole main turns attempting task creation, already counted in global/main totals. It is not a pure routing fee and does not attribute worker usage to main Chat.
