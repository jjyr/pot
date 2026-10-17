# Token usage and allowances

Usage shows overall Pot statistics, trends, and Agent/model categories over the last 7/30/90 days or year (365 days including today). The default is the last 7 days including today. Always retain every date slot in the selected range, including days with no usage; one active day must not expand to fill the chart. The range selector belongs to the trend; session totals and detail lists are explicitly lifetime values. The first three ranges use daily buckets; year uses consecutive 7-day groups, possibly a partial final group. Dates follow the current system IANA zone; today is incomplete. Trend bars split uncached input, cached input and output. Hovering or focusing any date column (including zero-usage dates), or tapping it, shows exact total/input/cache/uncached/output counts and cache hit rate. Current buckets say statistics are through now; the IANA zone is in the tooltip. Escape or an outside tap dismisses the tooltip. Totals and model categories show cached input, uncached input and the input cache hit rate. Cached tokens remain part of input and total tokens; token counts are not billed cost.

Each task (including archived tasks) and main chat has a usage entry. Details show cumulative usage, model consumption, and tool call rankings. Guidance continues the same task; turn IDs link records. Global usage does not list tasks.

Overall Usage places the location filter in the statistics heading as a compact select. Hide it when only one Space is available; multiple Spaces offer All locations and individual Space names. Conversation Usage has no location filter. Keep heading controls in normal document flow, wrapping on narrow windows.

## Metering

Go Control captures provider usage through the generic model gateway. Shared SQLite `usage.sqlite` stores request IDs, session/task, turn, time, model, input/output/cache, and reservations. Route snapshots record Agent identity, profile ID, provider, execution generation, and spec hash. Tool events deduplicate by session+turn+call ID. Tasks share the ledger; archive retains records. DELETE journal/synchronous=EXTRA remain; only Runtime accesses the live database.

- Actual total = input + output. Cache hits are a subset of input, not added again.
- Multiple usage frames update one request record. Missing usage is unknown, not zero.
- Tools show calls, failures, duration, and output estimates (serialized UTF-8 bytes / 4). Estimates are not provider billing and are not added to actual totals.
- Pot Agent compacts conversation history after tools settle when its context estimate approaches the profile-configured window, reserving room for output. [Context estimation](#context-estimation) uses Codex's content-cost rules with provider-native replay. Context estimates and allowance reservations do not contribute to actual token totals. Storage persists full current context injection, the replacement summary and the last real user message in that order. Earlier assistant/tool messages are represented by the summary; see [context and compaction](agent-context.md#compaction-and-continuation).
- Agent reports compaction lifecycle directly to Runtime. Start persists before its ID tags the metered gateway request. Summary usage is a subset of request usage, never counted twice.
- Session details show paginated model requests with input, cached input and output; input tooltips include uncached input and cache hit rate. One user turn can produce multiple model requests, each carrying its full context input. Missing input/output stays unknown; unknown input also hides cache breakdown and hit rate. A zero input has no hit rate. Tool output contributes neither to model totals nor input cache calculations.
- Underlying request/compaction lifecycle records and archived records remain; API detail pagination remains supported. Context sizes before/after compaction are estimates, not billing.
- Skills lack a reliable independent metering boundary, so no Skills statistics tab or estimated allocation. Skills loading remains unintegrated and unchanged here.
- Pi development sessions and old usage databases are not imported; this revision starts a new usage database.

Runtime uses indexed SQL aggregation, bounded time buckets, and independent 20-item pages for models, requests, tools and compactions. Requests and compactions sort by occurrence time ascending, then ID; tools sort by call count descending, then name. The summary never loads these lists. Initial summary/list queries run concurrently; subsequent paging refreshes only its list and retains rows while pending or on failure. The shared HTML frontend refreshes from persisted usage updates, keeps the displayed page while preparing replacements, preserves scroll, and rejects stale query responses. Disconnected cached values are labelled with their availability. Shared CSS and components render the charts, allowance bars and editing controls.

## Context estimation

The default estimator in [`pkg/agent/meter.go`](../pkg/agent/meter.go)
follows the [Codex content-cost baseline](https://github.com/openai/codex/tree/822e58cc3d).
It uses the same arithmetic and
rounding boundaries for the item types Pot supports. Protocol adapters map Chat
Completions, Responses, Anthropic Messages and Gemini to those content rules;
they do not select different token ratios. Callers may inject a tokenizer through
the Agent estimator boundary.

- Text costs `ceil(UTF-8 bytes / 4)`, rounded separately for base instructions and
  each model item. Message content parts are summed before that item's rounding.
  Boundaries follow logical history items, before protocol encoders merge adjacent
  messages or wrap tool images in synthetic user messages.
  This is a coarse heuristic, not a tokenizer-accurate or billed count.
- Count decoded text, not its outer JSON escaping, role, message ID, status or
  display metadata. Native assistant replay replaces duplicate display text and
  calls. Plaintext reasoning is excluded from local replay accounting.
- A function call counts its name, namespace (default `functions`) and argument
  JSON content. Function outputs count their content and the call ID, plus the
  optional name/namespace when present. Keep JSON syntax that is itself content,
  such as argument objects and tool schemas. Pot maps its supplied logical tool
  catalog to one Codex `AdditionalTools` item; provider transport wrappers are
  excluded. Tool results containing JSON remain text unless the protocol marks
  them as structured content.
- Normal-detail images cost 7,373 equivalent bytes each (1,844 tokens for an
  image-only item), independent of Base64 length or dimensions. Parts in the same
  message or tool output share one rounding boundary. Pot currently sends normal
  image detail; it does not expose Codex's original-detail or audio inputs.
- Encrypted Responses reasoning/compaction uses
  `max(floor(encoded_length * 3 / 4) - 650, 0)` equivalent bytes. Encrypted function
  output uses `ceil(encoded_length * 9 / 16)` equivalent bytes. Convert these byte
  estimates to tokens using the same per-item rule. No provider-specific safety
  multiplier is applied.

Complete provider usage calibrates an unchanged request prefix: use actual input
plus output tokens, then add only the local estimate of new content. Cached input
is already included in input; reasoning output is already included in output.
Neither is added twice. Edits, tools, system instructions, provider/model changes
and compaction invalidate old calibration. Missing or zero usage supplies no new
baseline; use a valid earlier prefix or a full local estimate. Missing billed
usage remains unknown. Calibration never uses cumulative session spending as the
current context size.

The configured context window and output reservation remain independent of this
heuristic. Control resolves [model defaults and user overrides](model-directory.md)
from its local directory. A known model input limit also bounds input and the
compaction threshold independently of the total context window. In particular,
a 32,768-token window does not reject a request solely
because its encoded JSON exceeds 32,768 bytes. Requests whose estimated context
plus configured output cap exceed the window are still rejected before transport.
Usage allowance reservations retain their separate conservative byte-based
policy described below; they are not context estimates.

## Main-chat routing subset

The `routing_total` summary reports whole main-chat turns that attempt task delegation, already included in total usage. Global scope follows the selected date window; main-chat details use lifetime scope. It includes all requests in the marked turn, even before the delegation tool call, and preserves incomplete usage. It is not a separately measured routing fee; no extra model call is introduced. Task scope reports no main-chat routing subset. Markers are durable/idempotent and historical unmarked turns are not inferred. See [Execution preferences](agent-context.md).


## Display and limits

Pot Agent executes direct API connections; the model service determines billing. API connections default to no periodic limit, hiding allowance cards/reset times. Settings -> Agent or Usage configures **Pot usage limits**: unlimited/daily/weekly, positive integer tokens with K/M, and automatic continuation after recovery (on by default). Saving applies immediately without restarting the execution environment.

Daily reset is 00: 00 in the configured zone, weekly Monday 00: 00. The policy explicitly stores its zone and uses calendar DST rules. Chart ranges do not affect allowance windows; changing periods preserves history.

Future subscription allowance adapters must report window, remaining fraction, reset time, and collection time, shown separately as Provider allowance. No provider balance is shown/inferred, and Pot tokens do not represent other clients' subscription consumption.

Conversation/compaction requests reserve in the shared ledger before networking: conservative input estimate = request UTF-8 bytes + 1024; output cap = smaller of configured cap and remaining allowance. Settle actual usage when received; unknown/interrupted requests keep conservative reservations until the window ends. Tokenizers/hidden overhead can differ, so no universal billing hard cap is promised. If the whole allowance is smaller than current context reservation, suggest raising it.

Lifetime request, per-turn request, and per-request output caps remain independent and do not reset with periodic token windows.

## Waiting and recovery

Task model, tool and compaction records use the Task ID as their ledger scope.
Main requests and automatic result summaries use the owning assistant ID. Mail
usage belongs to the recipient assistant, with a separate correspondence wait
scope so it cannot overwrite Main or Task waits. The summary’s `mail_waits` list
exposes the selected assistant’s resumable correspondence waits. Unknown model
reservations remain conservative even when a completed wait record is cleared.


When allowance is insufficient, gateway rejects the unsent request without consuming a request count. The Control execution adapter persists an allowance-waiting conversation or Runner state only after tools settle and the Agent checkpoint is saved. Tasks enter `waiting` (still ongoing) and release slots; main chat retains the pending turn. Follow-up task guidance queues after that turn. Schedule no-overlap rules still apply without duplicate waiting runs.

When allowance is sufficient and automatic continuation enabled, the scheduler resumes. Otherwise click Continue in session usage. Cancel waiting discards pending work for that turn. After App or execution-environment restart, only explicit persistent checkpoints can resume; unknown execution remains interrupted.

Resume reuses the saved Agent context, without replaying original prompts/tools. The model still chooses subsequent tools; this is not an exactly-once guarantee for external actions. Missing sessions, unconfirmed tool cleanup, or unknown crash side effects cannot retry automatically.

## API

Usage query/policy/recovery APIs require the main Runtime token; workers cannot read the global ledger or change policies. Internal compaction endpoints validate session authority.

- `GET /api/v1/usage/summary?days=7&timezone=Asia/Shanghai&scope=`: empty scope = global; assistant ID (`main` for the default assistant) or task ID = session. Session totals ignore days; trends follow days. Returns totals, trend buckets, allowances and waiting state, without detail lists.
- `GET /api/v1/usage/{models|requests|tools|compactions}?scope=OWNER&timezone=Asia/Shanghai&offset=0`: returns `{items, offset, next_offset}`. Each resource has independent 20-item pagination. Requests/tools/compactions require a session scope and use lifetime data. Models accept empty scope for global data and `days=7|30|90|365`; scoped models use lifetime data. A null next offset marks the end. Request/compaction timestamps use ascending ID as the tie-breaker; tools use ascending name after descending call count.
- Compaction lifecycle enters Control through injected execution services or the original run-bound signed port; Workers cannot read global usage or change policies.
- `POST /api/v1/usage/policy`: `{period: none|day|week, limit, timezone, auto_resume}`.
- `POST /api/v1/usage/resume?assistant_id=OWNER`: requires `{request_id, turn, since, scope?}`. The displayed original `turn`/`since` wait token and originating assistant must match the retained Main, Task or correspondence wait. Omitted `scope` selects Main. The exact wait-instance release and request receipt commit together; stale actions reject, and exact retries retain the original result without releasing a later wait. Available allowance is still required. `/api/v1/tasks/{id}/resume` requires the same `request_id`, `turn` and `since`; its path fixes the Task scope.
- `POST /api/v1/turn/cancel` cancels the exact Main user-message turn, including allowance waiting; Tasks use their interrupt/cancel APIs.

`GET /api/v1/agent/diagnostics` provides bounded execution metadata; diagnostic responses omit prompt text, tool arguments, output and error payloads.


## Thread configuration attribution

Gateway accepts only the current Thread's provider/model. `request_routes` links request IDs to
Agent type, profile ID, provider, generation, and spec hash. Existing request tables retain executed configuration models/turns.
OpenAI Chat codec retains DeepSeek reservation/output-cap/settlement behavior. Requests after a configuration change use the new snapshot; previous request attribution remains unchanged. Global/session summaries group input/output/cache/records/unknown usage by Agent type + model. Same models across Agents stay separate; same Agent/model across profiles/providers combines, retaining provider/profile/generation in request details.

`unknown` protocol neither parses response usage nor changes output caps or pretends usage is zero. Requests retain
unknown usage and conservative reservations. Exact request budgets, usage reporting and safe checkpoint resume require a known protocol. Subscription quotas remain unintegrated and cannot be inferred from the API ledger.

Terminal operations and API metadata queries trigger no model calls/usage records. Terminal text enters neither usage nor product logs.

Responses codec reads usage from response.completed/incomplete/failed; Messages codec
combines message_start input with message_delta output without releasing reservations at initial output=0;
Gemini codec reads usageMetadata, with output = candidatesTokenCount + thoughtsTokenCount.
All formats retain cache hits as input subsets and missing fields as unknown. New formats do not yet enable automatic checkpoint recovery.

## Provider allowances

Task Configuration exposes daily/weekly token allowances, zones, and automatic continuation for configured API Providers independently of autonomous task model routing.
UI retains policy zones (new policies default to Asia/Shanghai); API accepts other valid zones.
Global/provider allowances apply together; all main/worker requests share one ledger.
Reservation/provider attribution commit in one transaction; output cap is the smaller available allowance;
unknown requests remain reserved after restart. Resume checks both policies and automatically continues only supported API protocols.
Each saved API route has an independent immutable identity; a new endpoint route needs its own allowance.
Provider rate limits are not Pot allowance exhaustion.


## Agent / model categories

Attribution persists with the turn configuration snapshot at API reservation. Later Agent,
model, background-default, or profile changes do not rewrite past records. Main chat, background tasks, schedules, and compaction
share one ledger; compaction retains its actual Agent/model attribution.

Global categories use the page's selected range/zone, matching global totals; session/task details use their scope's
lifetime range, matching the total card. Pages hold 20 categories, sorted by input+output descending, then stable Agent/model order.
Pagination does not change totals/trends/allowances; scope/zone changes reset pages, and the global model range resets its pages. Stale responses are discarded. In the combined locations view, model categories remain grouped and paginated per Space; partial model pages are never summed into misleading global category totals. Global totals and trends still combine complete aggregates across Spaces.
Categories show input/output/input-cache hits/incomplete records without presenting unknown consumption as zero.
