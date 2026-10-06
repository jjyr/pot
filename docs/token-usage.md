# Token usage and allowances

Usage shows overall Pot statistics, trends, and Agent/model categories over the last 7/30/90 days or year (365 days including today). The first three use daily buckets; year uses consecutive 7-day groups, possibly a partial final group. Dates follow the current system IANA zone; today is incomplete. Hover shows date ranges, input/output/cache hits.

Each task (including archived tasks) and main chat has a usage entry. Details show cumulative usage, stacked model consumption, and tool output rankings. Guidance continues the same task; turn IDs link records. Global usage does not list tasks.

## Metering

Rust Runtime captures provider usage through the generic model gateway. Shared SQLite `product/usage.sqlite` stores request IDs, session/task, turn, time, model, input/output/cache, and reservations. Route snapshots record Agent type (Pi/Codex/Grok; historical CLI labels remain in existing rows), profile ID, provider, binding epoch, and spec hash. Tool events deduplicate by session+turn+call ID. Tasks share the ledger; archive retains records. DELETE journal/synchronous=EXTRA remain; only Runtime accesses the live database.

- Actual total = input + output. Cache hits are a subset of input, not added again.
- Multiple usage frames update one request record. Missing usage is unknown, not zero.
- Tools show calls, failures, duration, and output estimates (serialized UTF-8 bytes / 4). Estimates are not provider billing and are not added to actual totals.
- Pi automatic compaction defaults on, using the same model/gateway. Declared context is 32, 768 tokens, retaining about 8, 192 recent tokens and reserving 8, 192. Pi triggers compaction near the limit or on overflow recovery. Compaction respects the smaller of Pi request cap and user output cap.
- Each session explicitly loads `pot-usage.mjs` for compaction lifecycle. Start persists before the compaction ID is sent in request headers. SQLite aggregates one/multiple summary requests by ID. Pi's own aggregate usage is not counted again. Known usage from failed/cancelled requests remains; incomplete usage stays unknown.
- Session details have no tabs/per-request list. Horizontal stacks show recorded conversation/compaction tokens and proportions, with input/output/cache hover. Hide zero compaction; show empty state without confirmed usage. Rank tools by output bytes using a shared maximum bar scale, with estimated tokens/calls, top 5 by default and expandable. Hide empty tools; tool output contributes neither to model proportions nor totals.
- Underlying request/compaction lifecycle records and archived records remain; API detail pagination remains supported. Context sizes before/after compaction are estimates, not billing.
- Skills lack a reliable independent metering boundary, so no Skills statistics tab or estimated allocation. Skills loading remains unintegrated and unchanged here.
- Legacy `pi.usage` imports idempotently by session. Without time/turn data, it contributes to lifetime totals, not time charts, without a missing-time notice. Historical tool events lack reliable complete output, so estimates are not backfilled.

Runtime uses indexed SQL aggregation, bounded time buckets, and 20-request pages. App refreshes on demand every 3 seconds, rejects stale query responses, and does not present old disconnected data as new. Charts reuse gpui-kit `BarChart` hover/tooltips/ticks/themes; allowances use `Progress`, shared cards, and Kit forms.

## Native Codex metering

Codex bypasses the model gateway and records per-Pot-turn deltas of native thread cumulative usage. This includes native compaction within the turn,
without individual request/compaction breakdown; the UI states this precision. Duplicate notifications do not double-count.
Missing historical baselines, counter rollback, or missing required fields remain unknown rather than attributing historical consumption to the current turn.
Pot checks allowance only before new turns, with no per-request CLI reservation, so a turn may exceed the allowance;
no per-request hard budget, automatic checkpoint resume, or provider subscription balance is claimed.

## Display and limits

Pi is the execution driver; the model service determines billing. API connections default to no periodic limit, hiding allowance cards/reset times. Settings -> Agent or Usage configures **Pot usage limits**: unlimited/daily/weekly, positive integer tokens with K/M, and automatic continuation after recovery (on by default). Saving applies immediately without VM restart.

Daily reset is 00: 00 in the configured zone, weekly Monday 00: 00. The policy explicitly stores its zone and uses calendar DST rules. Chart ranges do not affect allowance windows; changing periods preserves history.

Future subscription allowance adapters must report window, remaining fraction, reset time, and collection time, shown separately as Provider allowance. Codex subscription allowances are not integrated; no provider balance is shown/inferred, and Pot tokens do not represent other clients' subscription consumption.

Conversation/compaction requests reserve in the shared ledger before networking: conservative input estimate = request UTF-8 bytes + 1024; output cap = smaller of configured cap and remaining allowance. Settle actual usage when received; unknown/interrupted requests keep conservative reservations until the window ends. Tokenizers/hidden overhead can differ, so no universal billing hard cap is promised. If the whole allowance is smaller than current context reservation, suggest raising it.

Lifetime request, per-turn request, and per-request output caps remain independent and do not reset with periodic token windows.

## Waiting and recovery

When allowance is insufficient, gateway rejects the unsent request without consuming a request count. Runtime saves `pi.paused` only after Pi stops and fsyncs its native session. Tasks enter `waiting` (still ongoing) and release slots; main chat retains the pending turn. Follow-up task guidance queues after that turn. Schedule no-overlap rules still apply without duplicate waiting runs.

When allowance is sufficient and automatic continuation enabled, the scheduler resumes. Otherwise click Continue in session usage. Cancel waiting discards pending work for that turn. After App/VM restart, only explicit persistent checkpoints can resume; unknown execution remains interrupted.

Resume reuses native Pi session with an explicit continuation instruction, without replaying original prompts/tools. The model still chooses subsequent tools; this is not an exactly-once guarantee for external actions. Missing sessions, unconfirmed Pi exit, or unknown crash side effects cannot retry automatically.

## API

Usage query/policy/recovery APIs require the main Runtime token; workers cannot read the global ledger or change policies. Internal compaction endpoints validate session authority.

- `GET /api/v1/usage/summary?days=7&timezone=Asia/Shanghai&scope=&offset=0`: empty scope = global; `main` or task ID = session. Task totals ignore days; requests and Agent/model categories paginate in 20-item offset pages. `agent_models` contains the current category page; `agent_model_next_offset` is the next offset.
- `POST /api/pi/compactions`: internal Pi lifecycle events require the current session token and active turn. Workers write only their own records and cannot read global usage/change policies.
- `POST /api/v1/usage/policy`: `{period: none|day|week, limit, timezone, auto_resume}`.
- `POST /api/v1/usage/resume`: `{scope}` clears the manual-resume gate, still requiring allowance.
- `POST /api/v1/usage/cancel`: cancels main chat waiting; tasks use the task cancellation API.

Legacy `/api/v1/usage` remains for request-count diagnostics; new UI uses summary.


## Agent binding attribution

Gateway accepts only the current binding's provider/model. `request_routes` links request IDs to
Agent type, profile ID, provider, epoch, and spec hash. Existing request tables retain executed binding models/turns; old ledgers are not reset.
OpenAI Chat codec retains DeepSeek reservation/output-cap/settlement behavior. Post-migration requests use
new snapshots; old epoch usage remains. Global/session summaries group input/output/cache/records/unknown usage by Agent type + model. Same models across Agents stay separate; same Agent/model across profiles/providers combines, retaining provider/profile/epoch in request details.

`unknown` protocol neither parses response usage nor changes output caps or pretends usage is zero. Requests retain
unknown usage and conservative reservations. Its ResolvedSpec disables exact request budgets, usage reporting, and
safe checkpoint resume. Subscription quotas remain unintegrated and cannot be inferred from the API ledger.

Terminal, official login queries, and command saves trigger no model calls/usage records. Terminal text enters neither usage nor product logs. CLI discovery Logged in means only official status, not validated real requests, model budgets, or role capabilities.

## Native Claude metering

Claude uses final-result cumulative turn usage. Input = input_tokens + cache_read_input_tokens +
cache_creation_input_tokens; cache hits = cache_read_input_tokens, a subset of input.
Repeated results overwrite the same turn rather than add again. Failure/cancellation retains known usage marked incomplete.
Like Codex, checks occur before new turns without per-request CLI hard budgets or automatic checkpoint recovery.

Responses codec reads usage from response.completed/incomplete/failed; Messages codec
combines message_start input with message_delta output without releasing reservations at initial output=0;
Gemini codec reads usageMetadata, with output = candidatesTokenCount + thoughtsTokenCount.
All formats retain cache hits as input subsets and missing fields as unknown. New formats do not yet enable automatic checkpoint recovery.

## Provider allowances

Task routing settings configure daily/weekly token allowances, zones, and automatic continuation for configured API providers.
UI retains policy zones (new policies default to Asia/Shanghai); API accepts other valid zones.
Global/provider allowances apply together; all main/worker requests share one ledger.
Reservation/provider attribution commit in one transaction; output cap is the smaller available allowance;
unknown requests remain reserved after restart. Resume checks both policies and automatically continues only checkpoint-capable drivers.
Each saved API route has an independent immutable identity; a new endpoint route needs its own allowance.
CLI subscriptions report native post-execution usage without pre-request hard limits; provider rate limits are not Pot allowance exhaustion.


## Agent / model categories

Attribution persists with the immutable binding at API reservation or native CLI turn start. Later Agent,
model, background-default, or profile changes do not rewrite past records. Main chat, background tasks, schedules, and compaction
share one ledger; compaction retains its actual Agent/model attribution. Native CLI summaries retain turn precision.

Global categories use the page's selected range/zone, matching global totals; session/task details use their scope's
lifetime range, matching the total card. Pages hold 20 categories, sorted by input+output descending, then stable Agent/model order.
Pagination does not change totals/trends/allowances; scope/range/zone changes reset pages and stale responses are discarded.
Categories show input/output/input-cache hits/incomplete records without presenting unknown consumption as zero.

Old ledgers gain agent/profile_id fields while preserving provider/epoch/hash and all metering.
Backfill historical Agent/profile only when scope, provider, epoch, spec hash, and model all match saved immutable bindings. Unconfirmed records show Unknown Agent without guesses from current settings, model names, or request IDs.
