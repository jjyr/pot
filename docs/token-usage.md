# Token usage and allowances

Usage has one date range for the total card, trend, category shares, model shares
and compaction usage. Four directly clickable options select 1 month,
3 months, 6 months or 1 year; there is no dropdown or custom date form. They select
the current natural calendar month, quarter, half-year (January–June or
July–December) or year, using inclusive start/end dates rather than fixed day counts.
Previous/next moves a whole selected period
without overlapping dates. Selecting an option again returns to its current
period. The current month is the default. The active timescale stays highlighted
when navigating previous periods. Selection survives navigation and language
changes and usage refreshes within the window, shared by overall, assistant and
task usage entries. There is no URL or stored range preference; a new window or
full reload starts with the current month. Unknown or removed presets resolve
to the current month. Current periods retain all dates, including zero-usage
and future dates, and mark statistics through today. Dates follow the current
system IANA zone. Clicking the full-year calendar still selects an individual day.

The stacked trend separates uncached input, cached input and output. Intervals of
up to 31 days use daily bars; longer intervals up to 180 days use Monday-based
weeks; longer intervals and year selections use calendar months. Partial first
and last weeks/months include only the selected dates. Bars fit the viewport
without horizontal scrolling, scale to the largest displayed total, and expose
exact date boundaries, counts and cache hit rates on hover, focus or tap.

The three horizontal charts follow that same date range, including in a task or
main-chat scope. Category and model shares reconcile with the selected total;
compaction is an included subset. A model's full ID determines its stable color
independently of sorting, refreshes and other models. Small shares retain true
widths and remain readable in the labeled list. These are complete aggregates,
not first pages of model details. Lifetime detail lists keep their separate labels.
Cached tokens remain part of input and total tokens; counts are not billed cost.

A separate full-year calendar appears below these charts. Its year controls do
not change the selected statistics above. Each column starts Sunday and every
calendar date is retained, including February 29. Intensity uses a shared annual
scale. Narrow views stack the two halves of the year. Future tiles are disabled;
tiles do not mark the selected statistics range. Keyboard focus retains its
outline. Selecting a tile switches the upper statistics to
that one day. Hovering or focusing a tile shows category shares and exact
input/cache/uncached/output counts and cache hit rate. Escape dismisses details.
The calendar has its own error/retry state so its failure does not hide the
selected statistics. Overall Usage aggregates this node by date/category/model;
linked nodes own their usage and their ledgers are not combined here.

Each task (including archived tasks) and main chat has a usage entry. Details show cumulative usage, model consumption, and tool call rankings. Guidance continues the same task; turn IDs link records. Global usage does not list tasks.

Overall and conversation Usage address the local node. Heading controls wrap
on narrow windows.

## Metering

The local gateway captures provider usage through the generic model gateway. Shared SQLite `usage.sqlite` stores request IDs, session/task, turn, time, model, input/output/cache, and reservations. Route snapshots record Agent identity, profile ID, provider, execution generation, and spec hash. Tool events deduplicate by session+turn+call ID. Tasks share the ledger; archive retains records. DELETE journal/synchronous=EXTRA remain; only Runtime accesses the live database.

- Actual total = input + output. Cache hits are a subset of input, not added again.
- Multiple usage frames update one request record. Missing usage is unknown, not zero.
- Tools show calls, failures, duration, and output estimates (serialized UTF-8 bytes / 4). Estimates are not provider billing and are not added to actual totals.
- Pot Agent compacts conversation history after tools settle when its context estimate approaches the profile-configured window, reserving room for the appended compact instruction and summary output. Cache maintenance enters the same path at 90% of its configured estimated cache lifetime when context exceeds 30% and the last response reported positive cache reads or writes. Expired windows and unknown/zero cache activity skip maintenance. [Context estimation](#context-estimation) uses Codex's content-cost rules with provider-native replay. Context estimates and allowance reservations do not contribute to actual token totals. Storage persists full current context injection, the replacement summary and the last real user message in that order. Earlier assistant/tool messages are represented by the summary; see [context and compaction](agent-context.md#compaction-and-continuation).
- Agent reports compaction lifecycle directly to Runtime. Start persists before its ID tags the metered gateway request. Summary usage is a subset of request usage, never counted twice.
- Session details show paginated model requests with input, cached input and output; input tooltips include uncached input and cache hit rate. One user turn can produce multiple model requests, each carrying its full context input. Missing input/output stays unknown; unknown input also hides cache breakdown and hit rate. A zero input has no hit rate. Tool output contributes neither to model totals nor input cache calculations.
- Underlying request/compaction lifecycle records and archived records remain; API detail pagination remains supported. Context sizes before/after compaction are estimates, not billing.
- Skills lack a reliable independent metering boundary, so no Skills statistics tab or estimated allocation. Skills loading remains unintegrated and unchanged here.
- Pi development sessions and old usage databases are not imported; this revision starts a new usage database.

Runtime uses indexed SQL aggregation over explicit local-day boundaries and independent 20-item pages for lifetime models, requests, tools and compactions. The summary accepts inclusive ISO start/end dates (at most 3,660 days), or a relative 14/30/90/365-day range, defaulting to 365 days. All ranges return daily buckets and complete Agent/model totals. Future request timestamps are excluded at the current instant; empty future dates remain in the projection. Calendar aggregation groups date/Agent/model in SQL without loading request records; all summary projections share one transaction. Requests sort by request start time descending, then ID descending; compactions sort by occurrence time ascending, then ID; tools sort by call count descending, then name. The summary never loads these lists. Initial summary/list queries run concurrently; subsequent paging refreshes only its list and retains rows while pending or on failure. The shared HTML frontend refreshes from persisted usage updates, keeps the displayed page while preparing replacements, preserves scroll, and rejects stale query responses. Shared CSS and components render the charts, allowance bars and editing controls.

Compaction usage appears below the selected model distribution, using the
same horizontal stacked bar. `compaction_totals` in the summary groups linked
requests into `precompact` (cache maintenance) and `compact` (threshold, overflow
and manual triggers), within the selected scope and local-day interval. Every
metered attempt contributes once, including failures and retries; never-sent
rejections are excluded. This is a subset of the existing Agent/model totals.
Consumption is input + output; cache reads are already included in input. Cache
hit rates use summed cache reads / summed input, including across Spaces. The
ledger retains whether cache reads were reported, distinguishing a reported zero
from unknown. If any contributing input/cache report is missing, its aggregate
hit rate displays as unknown. Partial token sums remain visible as lower bounds;
the bar represents reported usage. The tooltip shows input/output/cache details.

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

The `routing_total` summary reports whole main-chat turns that attempt task delegation, already included in total usage. Global and main-chat scopes both follow the selected date interval. It includes all requests in the marked turn, even before the delegation tool call, and preserves incomplete usage. It is not a separately measured routing fee; no extra model call is introduced. Task scope reports no main-chat routing subset. Markers are durable/idempotent and historical unmarked turns are not inferred. See [Execution preferences](agent-context.md).


## Display and limits

Pot Agent executes direct API connections; the model service determines billing. API connections default to no periodic limit, hiding allowance cards/reset times. Settings -> Agent or Usage configures **Pot usage limits**: unlimited/daily/weekly, positive integer tokens with K/M, and automatic continuation after recovery (on by default). Saving applies immediately without restarting the execution environment.

Daily reset is 00: 00 in the configured zone, weekly Monday 00: 00. The policy explicitly stores its zone and uses calendar DST rules. Chart ranges do not affect allowance windows; changing periods preserves history.

Future subscription allowance adapters must report window, remaining fraction, reset time, and collection time, shown separately as Provider allowance. No provider balance is shown/inferred, and Pot tokens do not represent other clients' subscription consumption.

Conversation/compaction requests reserve in the shared ledger before networking: conservative input estimate = request UTF-8 bytes + 1024; output cap = smaller of configured cap and remaining allowance. Settle actual usage when received; unknown/interrupted requests keep conservative reservations until the window ends. Tokenizers/hidden overhead can differ, so no universal billing hard cap is promised. If the whole allowance is smaller than current context reservation, suggest raising it.

Lifetime request, per-turn request, and per-request output caps remain independent and do not reset with periodic token windows.

## Waiting and recovery

Task model, tool and compaction records use the Task ID as their ledger scope.
Main requests, automatic result summaries and correspondence use the owning
assistant ID and the same serial conversation allowance wait. Correspondence
resumes through the assistant conversation; there is no separate mail wait scope
or `mail_waits` API field. Unknown model
reservations remain conservative even when a completed wait record is cleared.


When allowance is insufficient, gateway rejects the unsent request without consuming a request count. The local execution adapter persists an allowance-waiting conversation or run state only after tools settle and the Agent checkpoint is saved. Tasks enter `waiting` (still ongoing) and release slots; main chat retains the pending turn. Follow-up task guidance queues after that turn. Schedule no-overlap rules still apply without duplicate waiting runs.

When allowance is sufficient and automatic continuation enabled, the scheduler resumes. Otherwise click Continue in session usage. Cancel waiting discards pending work for that turn. After App or execution-environment restart, only explicit persistent checkpoints can resume; unknown execution remains interrupted.

Resume reuses the saved Agent context, without replaying original prompts/tools. The model still chooses subsequent tools; this is not an exactly-once guarantee for external actions. Missing sessions, unconfirmed tool cleanup, or unknown crash side effects cannot retry automatically.

Model connection failures and incomplete streams preserve the last committed
context for an explicit failed-turn retry. An interrupted sample's uncommitted
output is discarded; already completed tool receipts remain in history. A new
sample uses a new request ID and reservation. The old sample's unknown usage
retains its conservative reservation until the allowance window ends; making a
turn retryable does not mark that request rejected or refund its allowance.

## API

Usage query/policy/recovery APIs require the main Runtime token; workers cannot read the global ledger or change policies. Internal compaction endpoints validate session authority.

- `GET /api/v1/usage/summary?start=2026-10-01&end=2026-10-31&timezone=Asia/Shanghai&scope=`: dates are inclusive local calendar dates. Send both dates, in order, with no `days` parameter; the maximum span is 3,660 days. Alternatively `days=14|30|90|365` selects a relative interval ending today. Empty scope is global; an assistant ID (`main` for the default assistant) or task ID selects that scope. Total, daily buckets, Agent/model/compaction shares and routing subset follow the same interval. Response includes `start`, `end` and `today`; `lifetime` remains separate. Allowances and waiting state stay current regardless of the date range. No detail lists are loaded.
- `GET /api/v1/usage/compaction-record?scope=OWNER_OR_TASK&id=OPERATION`: owner-scoped private diagnostic record for investigation. Includes trigger/configuration, timing, context fingerprints, original native request and response, before/after context and metered attempts. Unknown usage remains null; context estimates are distinct from billed usage. Records contain private conversation data and never authorization headers. Chat and usage pages show compaction status and token counts without a download action.
- `GET /api/v1/usage/{models|requests|tools|compactions}?scope=OWNER&timezone=Asia/Shanghai&offset=0`: returns `{items, offset, next_offset}`. Each resource has independent 20-item pagination. Requests/tools/compactions require a session scope and use lifetime data. Models accept empty scope for global data and `days=14|30|90|365`; scoped models use lifetime data. A null next offset marks the end. Request/compaction timestamps use ascending ID as the tie-breaker; tools use ascending name after descending call count.
- Compaction lifecycle enters the local coordinator through injected execution services; Workers cannot read global usage or change policies.
- `POST /api/v1/usage/policy`: `{period: none|day|week, limit, timezone, auto_resume}`.
- `POST /api/v1/usage/resume?assistant_id=OWNER`: requires `{request_id, turn, since, scope?}`. The displayed original `turn`/`since` wait token and originating assistant must match the retained Main, Task or correspondence wait. Omitted `scope` selects Main. The exact wait-instance release and request receipt commit together; stale actions reject, and exact retries retain the original result without releasing a later wait. Available allowance is still required. `/api/v1/tasks/{id}/resume` requires the same `request_id`, `turn` and `since`; its path fixes the Task scope.
- `POST /api/v1/turn/cancel` cancels the exact Main user-message turn, including allowance waiting; Tasks use their interrupt/cancel APIs.

`GET /api/v1/agent/diagnostics` provides bounded execution metadata; diagnostic responses omit prompt text, tool arguments, output and error payloads.

The browser service publishes the summary and all four paginated detail routes
through its explicit GET allowlist. Usage limits are read from the summary's
`quota.policy`; `/api/v1/usage/policy` is a write-only endpoint.


## Thread configuration attribution

Gateway accepts only the current Thread's provider/model. `request_routes` links request IDs to
Agent type, profile ID, provider, generation, and spec hash. Existing request tables retain executed configuration models/turns.
OpenAI Chat codec retains DeepSeek reservation/output-cap/settlement behavior. Requests after a configuration change use the new snapshot; previous request attribution remains unchanged. Global/session summaries group input/output/cache/request counts by Agent type + model. Same models across Agents stay separate; same Agent/model across profiles/providers combines, retaining provider/profile/generation in request details.

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

Summary categories use the selected range/zone in global and conversation scopes, matching the total card. Assistant-scoped summaries, including reads through Agent Link, forward the same inclusive start/end dates, timezone and scope to the ledger. Explicit dates cannot be combined with a days preset or used for paginated detail routes. Paginated session/task details use their scope's lifetime range and are labeled separately. Pages hold 20 categories, sorted by input+output descending, then stable Agent/model order.
Pagination does not change totals/trends/allowances; scope/zone changes reset pages, and the global model range resets its pages. Stale responses are discarded.
Categories show input/output/input-cache hits. Incomplete-record counts are not calculated or exposed in usage aggregates or the UI. Individual request details keep missing token values as unknown rather than zero; reservations still retain conservative accounting until complete usage arrives or the allowance window ends.
