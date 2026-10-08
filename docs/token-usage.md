# Token usage and allowances

Usage shows overall Pot statistics, trends, and Agent/model categories over the last 7/30/90 days or year (365 days including today). The first three use daily buckets; year uses consecutive 7-day groups, possibly a partial final group. Dates follow the current system IANA zone; today is incomplete. Hover shows date ranges, input/output/cache hits.

Each task (including archived tasks) and main chat has a usage entry. Details show cumulative usage, stacked model consumption, and tool output rankings. Guidance continues the same task; turn IDs link records. Global usage does not list tasks.

## Metering

Rust Runtime captures provider usage through the generic model gateway. Shared SQLite `product/usage.sqlite` stores request IDs, session/task, turn, time, model, input/output/cache, and reservations. Route snapshots record Agent identity, profile ID, provider, binding epoch, and spec hash. Tool events deduplicate by session+turn+call ID. Tasks share the ledger; archive retains records. DELETE journal/synchronous=EXTRA remain; only Runtime accesses the live database.

- Actual total = input + output. Cache hits are a subset of input, not added again.
- Multiple usage frames update one request record. Missing usage is unknown, not zero.
- Tools show calls, failures, duration, and output estimates (serialized UTF-8 bytes / 4). Estimates are not provider billing and are not added to actual totals.
- Pot Agent compacts completed older user turns when its context estimate approaches the profile-configured window, reserving room for output. Estimates use the actual provider-native context projection without duplicate display/replay fields. Complete usage calibrates an unchanged request prefix; later input is estimated locally. Cache is included once in input and reasoning once in output. Edits, tool/system/model changes and compaction invalidate old calibration. Missing usage supplies no new calibration; new context is estimated from a valid earlier prefix, or locally when no prefix is valid, and missing billed usage remains unknown. Context estimates and allowance reservations do not contribute to actual token totals. The replacement summary and complete recent turn are persisted through Storage.
- Agent reports compaction lifecycle directly to Runtime. Start persists before its ID tags the metered gateway request. Summary usage is a subset of request usage, never counted twice.
- Session details have no tabs/per-request list. Horizontal stacks show recorded conversation/compaction tokens and proportions, with input/output/cache hover. Hide zero compaction; show empty state without confirmed usage. Rank tools by output bytes using a shared maximum bar scale, with estimated tokens/calls, top 5 by default and expandable. Hide empty tools; tool output contributes neither to model proportions nor totals.
- Underlying request/compaction lifecycle records and archived records remain; API detail pagination remains supported. Context sizes before/after compaction are estimates, not billing.
- Skills lack a reliable independent metering boundary, so no Skills statistics tab or estimated allocation. Skills loading remains unintegrated and unchanged here.
- Pi development sessions and old usage databases are not imported; this revision starts a new usage database.

Runtime uses indexed SQL aggregation, bounded time buckets, and 20-request pages. App refreshes on demand every 3 seconds, rejects stale query responses, and does not present old disconnected data as new. Charts reuse gpui-kit `BarChart` hover/tooltips/ticks/themes; allowances use `Progress`, shared cards, and Kit forms.

## Main-chat routing subset

The `routing_total` summary reports whole main-chat turns that attempt task delegation, already included in total usage. Global scope follows the selected date window; main-chat details use lifetime scope. It includes all requests in the marked turn, even before the delegation tool call, and preserves incomplete usage. It is not a separately measured routing fee; no extra model call is introduced. Task scope reports no main-chat routing subset. Markers are durable/idempotent and historical unmarked turns are not inferred. See [Execution preferences](execution-preferences.md).


## Display and limits

Pot Agent executes direct API connections; the model service determines billing. API connections default to no periodic limit, hiding allowance cards/reset times. Settings -> Agent or Usage configures **Pot usage limits**: unlimited/daily/weekly, positive integer tokens with K/M, and automatic continuation after recovery (on by default). Saving applies immediately without VM restart.

Daily reset is 00: 00 in the configured zone, weekly Monday 00: 00. The policy explicitly stores its zone and uses calendar DST rules. Chart ranges do not affect allowance windows; changing periods preserves history.

Future subscription allowance adapters must report window, remaining fraction, reset time, and collection time, shown separately as Provider allowance. No provider balance is shown/inferred, and Pot tokens do not represent other clients' subscription consumption.

Conversation/compaction requests reserve in the shared ledger before networking: conservative input estimate = request UTF-8 bytes + 1024; output cap = smaller of configured cap and remaining allowance. Settle actual usage when received; unknown/interrupted requests keep conservative reservations until the window ends. Tokenizers/hidden overhead can differ, so no universal billing hard cap is promised. If the whole allowance is smaller than current context reservation, suggest raising it.

Lifetime request, per-turn request, and per-request output caps remain independent and do not reset with periodic token windows.

## Waiting and recovery

When allowance is insufficient, gateway rejects the unsent request without consuming a request count. Runtime saves `execution.paused` only after tools settle and Storage persists the execution context. Tasks enter `waiting` (still ongoing) and release slots; main chat retains the pending turn. Follow-up task guidance queues after that turn. Schedule no-overlap rules still apply without duplicate waiting runs.

When allowance is sufficient and automatic continuation enabled, the scheduler resumes. Otherwise click Continue in session usage. Cancel waiting discards pending work for that turn. After App/VM restart, only explicit persistent checkpoints can resume; unknown execution remains interrupted.

Resume reuses the saved Agent context, without replaying original prompts/tools. The model still chooses subsequent tools; this is not an exactly-once guarantee for external actions. Missing sessions, unconfirmed tool cleanup, or unknown crash side effects cannot retry automatically.

## API

Usage query/policy/recovery APIs require the main Runtime token; workers cannot read the global ledger or change policies. Internal compaction endpoints validate session authority.

- `GET /api/v1/usage/summary?days=7&timezone=Asia/Shanghai&scope=&offset=0`: empty scope = global; `main` or task ID = session. Task totals ignore days; requests and Agent/model categories paginate in 20-item offset pages. `agent_models` contains the current category page; `agent_model_next_offset` is the next offset.
- `POST /api/agent/compactions`: internal Agent lifecycle events require the current session token and active turn. Workers write only their own records and cannot read global usage/change policies.
- `POST /api/v1/usage/policy`: `{period: none|day|week, limit, timezone, auto_resume}`.
- `POST /api/v1/usage/resume`: `{scope}` clears the manual-resume gate, still requiring allowance.
- `POST /api/v1/usage/cancel`: cancels main chat waiting; tasks use the task interruption API.

Legacy `/api/v1/usage` remains for request-count diagnostics; new UI uses summary.


## Agent binding attribution

Gateway accepts only the current binding's provider/model. `request_routes` links request IDs to
Agent type, profile ID, provider, epoch, and spec hash. Existing request tables retain executed binding models/turns; no legacy ledger import or schema migration is provided.
OpenAI Chat codec retains DeepSeek reservation/output-cap/settlement behavior. Post-migration requests use
new snapshots; old epoch usage remains. Global/session summaries group input/output/cache/records/unknown usage by Agent type + model. Same models across Agents stay separate; same Agent/model across profiles/providers combines, retaining provider/profile/epoch in request details.

`unknown` protocol neither parses response usage nor changes output caps or pretends usage is zero. Requests retain
unknown usage and conservative reservations. Its ResolvedSpec disables exact request budgets, usage reporting, and
safe checkpoint resume. Subscription quotas remain unintegrated and cannot be inferred from the API ledger.

Terminal operations and API metadata queries trigger no model calls/usage records. Terminal text enters neither usage nor product logs.

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
Provider rate limits are not Pot allowance exhaustion.


## Agent / model categories

Attribution persists with the immutable binding at API reservation. Later Agent,
model, background-default, or profile changes do not rewrite past records. Main chat, background tasks, schedules, and compaction
share one ledger; compaction retains its actual Agent/model attribution.

Global categories use the page's selected range/zone, matching global totals; session/task details use their scope's
lifetime range, matching the total card. Pages hold 20 categories, sorted by input+output descending, then stable Agent/model order.
Pagination does not change totals/trends/allowances; scope/range/zone changes reset pages and stale responses are discarded.
Categories show input/output/input-cache hits/incomplete records without presenting unknown consumption as zero.
