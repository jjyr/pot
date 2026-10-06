# Small-budget Agent evaluation suite

Status: scoped Pot implementation and one live sweep delivered on 2026-10-06. `pot-eval suite deterministic|live` and `tree deterministic|live` implement the small workload suite, shared limits, per-turn validation, cache identity checks, deterministic faults and directed serial child sessions. The optional Pi baseline is a direct CLI script, not an adapter. See [dated validation](validation/small-budget-evaluation.md) for results and a retained setup failure. The broader capability negotiation/matrix and schema proposals below are not all delivered. This document complements the [long-term framework](token-evaluation.md) and [CLI contract](../eval/README.md).

## Scope and execution policy

Cover at most five conversational turns, provider cache utilization, controlled failure/retry behavior and explicit task delegation to two child Agents. Defer large tool output, long conversations, repeated compaction and broad model matrices. Use only the configured DeepSeek Flash model for initial live measurements; the workload and reporting contracts remain independent of that model and of Pi.

Run deterministic fixtures in ordinary tests without networking. Live execution is explicit, initially one trial per configuration; three repeats are an optional follow-up when a result warrants investigation. One trial supplies diagnostic evidence, not statistical superiority. Reuse the Core Driver contract and injected execution services, keeping trial scheduling and assertions in `pot-eval`. Direct CLI baselines can remain external recorded experiments; this suite does not require a Pi adapter.

Pin workload versions, fixture hashes, output requirements and effective settings. Disable thinking and compaction for these small cases, record the actual tool set, and bound every response. A turn is a user/Agent interaction, not an API request: requests, retry attempts and child-Agent requests have independent limits. Unexpected compaction is reported as outside the intended case, not silently ignored.

## Cases

### Five-turn context retention

Use a tool-free conversation with short, machine-checkable replies. Turn one establishes two identifiers and a small map; turn two adds a key; turn three replaces one original value; turn four updates an identifier; turn five requests the exact resulting JSON. Each intermediate reply is a fixed acknowledgement. The final answer must retain untouched facts, apply both updates and omit superseded values. The fixture contains no sensitive or randomly generated information and no more than 512 bytes of task data.

Record actual input/output and context size per turn, the input difference between turns one and five, cumulative usage, and acceptance of every reply. Store emitted history and encoded requests where supported to distinguish ordinary history growth from duplicate message inclusion. A lower input total is accepted as an efficiency improvement only when all assertions pass. Use a 64-token output cap and at most five requests; a tool invocation is a case failure.

### Cache sequence

Run five independent, fresh conversations with fixed prompt/tool definitions, one model request per conversation and a 64-token output cap. Use meaningful fixed task content, initially no more than 512 bytes, with the pinned native tool declarations. Measure rather than assume whether this provider's cache threshold is reached. Do not add a long padding block merely to force a hit.

| Request | Input | Purpose |
|---|---|---|
| A1 | Fixed system, tools and user task A | Observe initial state; a cache miss is not guaranteed |
| A2 | Exact repeat of A1 | Observe reuse after a configurable, recorded short delay |
| A3 | Exact repeat of A1 | Observe whether reuse becomes visible on another repeat |
| B | Same early prefix, changed final user-task suffix | Observe reuse of an unchanged prefix |
| C | Change an early system-prefix field, keep task/output size comparable | Observe behavior after early-prefix mutation |

Use tasks with exact short outputs and separate acceptance assertions for each variant. Where request capture is available, verify exact-repeat hashes and prefix/suffix differences before interpreting cache behavior. Compare normalized total input, reported cached input, reported uncached input and latency. Report cache categories as provider observations; do not infer a billable category from a request hash. A cache hit may reduce price and latency without reducing total input tokens.

The service may populate cache asynchronously, reuse previous experiments or report no cache detail. Preserve delay and request order. If all requests miss, mark the cache-effect conclusion inconclusive; if usage lacks cache fields, mark that observation unsupported. Do not automatically extend the workload or repeat indefinitely. Cost comparison requires verified model/cache prices; otherwise report tokens and latency only.

### Failure and retry contracts

Put a fault-injection service around the model/tool service boundary so the same configured faults can exercise the actual Agent continuation and transport policy. Keep normal model traces deterministic first. Do not make real provider calls fail merely to test retry logic. An eval fixture does not introduce retries into a runtime that has none: record its effective policy and test its declared recovery or terminal-failure behavior.

| Fault | Expected checks |
|---|---|
| One pre-response 429 or 503, then success | Honor configured retry classification, attempt limit and bounded delay; preserve the logical request |
| Non-retryable authentication/validation error | Stop without retry; retain diagnostics and usage coverage |
| Connection failure before receiving response content | Follow declared retry policy with distinct attempt identities |
| Stream interrupted after partial content, before final usage | Discard incomplete actionable tool calls; preserve partial evidence; mark unknown usage as unknown |
| Tool reports one recoverable failure, then model chooses a correction | No transport replay of successful tool effects; bounded Agent rework; correct final artifact |
| Fault repeats through the configured attempt limit | Terminate within limits and retain every failed attempt |

Include a successful side-effect fixture with a repeated delivery/event to verify that receipt/event replay does not execute the effect twice. This does not assume provider-side exactly-once execution. Synthetic usage snapshots exercise aggregation, not empirical token efficiency. Unknown provider consumption after a stream break is not zero and cannot establish a complete observed total.

Compare each recoverable trace with a no-fault control and report logical requests, physical attempts, retry delay, Agent rework requests, executed effects and usage coverage separately. Never count both a parent's cumulative total and its constituent attempts. Maximum fixture transport attempts per logical request: two; at most one intentional failure before success in the basic recoverable cases. Exhaustion cases explicitly expect failure. Future live recovery studies are separately opt-in and are not part of the initial paid suite.

### Directed two-child delegation

Use six small CSV records partitioned into two independent three-record files. The task is to produce exact aggregate JSON containing row count, total amount and maximum amount. Fix the schema and expected values in advance. Raw task data and each child result are bounded at 512 bytes; final model responses are capped at 128 tokens.

Compare two execution configurations for the same final task and fixtures:

1. One worker Agent reads both partitions and computes the result, without delegation tools.
2. A parent Agent uses task-management tools to dispatch exactly two child tasks. Each child receives only its partition and returns the pinned JSON statistics. The parent collects the two results and combines them into the same final JSON.

The parent does not need filesystem tools; workers use the normal sandbox execution environment or the current explicit local eval fixture, recorded in the manifest. Main-Agent isolation is not an acceptance item here. The second configuration deliberately prescribes the task split to measure its overhead reproducibly; autonomous decisions about whether/how to split are deferred. Its tool set differs from the single-worker baseline, so this is a task-architecture comparison rather than an isolated loop-efficiency comparison.

The runner implements an evaluation task-management service through the existing Agent extension boundary, creates real child Driver sessions, routes verified child results, and records parent/child identities. It does not fabricate child responses for live measurements. Deterministic fixtures can replay child traces for contract checks. Product Storage/Runtime are not dependencies of this service; production scheduling policy is not claimed to be evaluated unless it is actually reused.

Limit nesting depth to one and children to two. Run the initial split serially to isolate token transfer/aggregation from scheduling; later use a separate parallel configuration when latency is worth measuring. Limit the live run to eight model attempts across the entire tree, at most four in the single-worker baseline, and at most five conversational turns per Agent. Child execution and all attempts share the root deadline and budget. On child failure or exhaustion, terminate or follow the explicitly configured recovery policy; do not silently spawn another child or omit a partition.

Validate child partition results and the final result, correct result routing, absence of duplicate dispatch, and shutdown of remaining sessions when the root terminates. Deterministic tests additionally cover swapped completion order, a duplicate completion event and one failed child. Report total tree input/output, parent-only usage, each child's usage, dispatch/result payload sizes, attempts, tool calls and critical-path duration. Attribute actual usage only to disjoint request measurements; payload sizes explain transfer overhead but are not extra consumed tokens. A parent context estimate and child cumulative counters cannot be added to provider request totals.

## Proposed records and implementation boundaries

Delivered additions include optional per-turn text/JSON assertions, per-turn usage and answers, bounded fixed cache sequences, typed local fault fixtures, tree lifecycle/results and shared reservations. Scenario fields are additive and omitted when empty, preserving old scenario hashes; the ordinary same-policy comparator remains strict. The following remains the broader schema/contract target, rather than a claim that all controls are implemented:

- Versioned workload identity separated from execution-configuration identity, so native-policy or single/split comparisons need not falsely claim equal scenario hashes. Preserve strict identical-policy comparison behavior.
- Per-turn output assertions and per-turn usage views for the five-turn case.
- Experiment sequences containing fresh-session runs, controlled ordering/delay and explicit repeats for cache cases; byte-level request checks when observable.
- Fault actions with activation point, count and expected outcome; link logical request IDs to attempt IDs and record effective retry policy. Local injected services must support both request-start failures and mid-stream failures.
- Root/parent/Agent/task identities, child lifecycle, and a shared tree-wide request/deadline budget. Negotiate delegation support rather than silently emulating unsupported Driver controls.
- Reports with terminal outcome (`pass`, expected failure, unexpected failure, budget stop), measurement coverage and experiment conclusion status. A missing capability skips the specific control/measurement with an explicit reason rather than inventing zero usage.

Reuse native lifecycle and usage events wherever they already carry the required identity. Add only missing contracts to Core; experiment matrices, fault plans, workload assertions and aggregation views belong in `pot-eval`. Raw request/response capture remains optional and credential-free. Provider/native actual usage, estimates and reservations stay separate.

## Initial budget and delivery order

The first live sweep is one five-turn run, one five-request cache sequence, one single-worker run and one two-child run: **at most 22 model attempts** (5 + 5 + 4 + 8). These are ceilings, not target counts. Set a conservative encoded-request-bytes-plus-output-cap reservation ceiling across the sweep, initially 50,000 units, using the same distinction from observed usage as the existing live runner. It is not a guaranteed billed-token or currency bound. Report and retain budget-stopped cases; do not automatically increase limits. Retries consume the same allowance. Explicit paid execution must name the suite, trial count and effective limits.

1. Add per-turn assertions/reporting and the small five-turn case.
2. Add cache experiment scheduling/reporting and request-identity checks.
3. Add deterministic fault injection, retry/usage-coverage and side-effect receipt tests.
4. Add the minimal eval task-management service, tree attribution and budget enforcement; then run the single/split pair.

Before any paid sweep, validate all fixtures, caps, policy controls and aggregations with deterministic traces. Initial reports remain JSON/JSONL plus a concise Markdown comparison. Large-output tests, additional models, paid fault studies and autonomous delegation remain deferred.
