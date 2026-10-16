# Long-term Agent token evaluation (design proposal)

Status: architecture accepted; deterministic foundation and DeepSeek Flash live tests delivered, 2026-10-06. This document defines the long-term framework. The standalone `pot-eval` Go CLI implements the initial deterministic runner and usage validator; bounded DeepSeek Flash live execution is available; native baseline execution and broader comparisons remain planned. See the [CLI guide](../eval/README.md). It has no Pi-specific data model and does not change product execution or billing. The [runtime contract](../crates/runtime/README.md) describes current Pot behavior.

## Objective

Measure the tokens, cost, latency and work required to complete reproducible scenarios across runtime versions and implementations. Comparisons must retain task quality, failures and observation precision: a cheap incomplete task is not an efficiency improvement. The same framework should compare Pot against an earlier Pot revision, Pi or a future Agent without changing scenarios or aggregation rules.

## Components and boundaries

| Component | Responsibility |
|---|---|
| Scenario catalog | Versioned user turns, workspace fixtures, execution target, task assertions, resource limits and optional controlled response trace |
| Runner | Run matrix, isolated fresh sessions/workspaces, repeats, ordering, deadlines, lifecycle and artifact collection |
| Agent executor | Directly invoke pot-agent with Thread configuration, injected services, controls and ordered observations |
| Execution target/tool service | Prepare identical fixtures and, where supported, common tool semantics/results for controlled comparisons |
| Usage collector | Capture provider usage or native counters and declare what scope/stages are observable |
| Normalizer/validator | Convert observations into a versioned schema, resolve cumulative snapshots and duplicate sources, preserve missing fields and raw provenance |
| Evaluator/reporter | Evaluate artifacts/answers, compute comparable metrics, compare baselines and explain regressions by trace |

The standalone Go runner lives in `internal/eval`, with `cmd/pot-eval` as its CLI and scenarios/experiments in `eval/`. It uses `internal/core`, `internal/agent`, `internal/modelprotocol` and the real executor, without depending on Runtime, Control, product Storage or App. It owns scenario execution, immutable run records, validation, comparison and bounded experiments. Go records use schema 2; legacy records are not imported.

Production invocations go through `pot-runtime-execution`; isolated engine evaluation fixtures call `pot_agent::Agent::execute` directly. Thread context belongs to the caller's Store, not to an external engine session. The Engine/Binding/NativeRef and DriverHost/SessionHandle abstractions have been removed. A future external baseline belongs in a separate evaluation command; it must not restore adapters to production execution.

## Standalone execution services

`ExecutionContext` supplies instructions, execution target tools, context persistence and cancellation feedback. `ExecutionServices` injects model transport, product RPC and compaction lifecycle transport. Runtime supplies authenticated Gateway and owner services; eval supplies deterministic or explicitly selected live transport, run-local context storage, tools and controls. Product reservations remain durable before networking; observers cannot authorize requests or replace settlement.

Core defines ordered Agent events and typed request/usage/compaction observations. Owner/generation/turn/sequence checks reject stale updates. Evaluation adds run/trial identity and provenance without a Driver capability-negotiation layer. Request/attempt IDs link observations; retries have distinct attempts. Collector failures invalidate measurement coverage, and missing measurements remain unknown. Credentials never enter manifests or raw request records.

## Scenario and run identity

Each scenario pins an ID/version, user input sequence, fixture content hash, execution target requirements, expected outcomes, tags and limits. Assertions check task behavior rather than implementation-specific tool names unless the case explicitly tests a tool contract. Optional trace fixtures control model responses/tool results for causal debugging; they do not provide live billing evidence.

Every immutable run manifest records:

- Schema version, run ID, comparison group/pair ID, scenario version/hash and trial number.
- Agent version, runtime commit/build, requested/effective model/provider and generation settings.
- Prompt/tool-set hashes, runtime policy including compaction/retention/output limits, execution target/image/fixture hashes.
- Collector versions and negotiated observation precision; pricing snapshot ID when cost is calculable.
- Mode, ordering, seed when supported, timestamps, resource limits and terminal status.

Requested model aliases are not sufficient to prove identical effective model versions. Missing effective values remain unknown. Preserve manifests and raw observations so a later report can be recomputed without rerunning paid inference.

## Events and usage semantics

Use an append-only `events.jsonl` with schema version, sequence, source event ID, run/turn/request/tool/compaction IDs, causal parent/link IDs, time, event kind and payload reference. Record request start/end/failure, tool start/output/end, compaction start/end, restart/checkpoint and turn/run settlement. A compaction operation may own multiple requests, including retries. Auxiliary model work such as title generation also belongs to the run and receives a separate stage label.

Usage reports carry a measurement scope (`request`, `turn`, or `run`), source (`provider`, `native`, or `estimate`), counter semantics (`snapshot`, `cumulative`, or `delta`), completion status and raw usage reference. A normalized report uses:

```json
{
  "kind": "usage.report",
  "measurement_id": "request-17",
  "scope": "request",
  "source": "provider",
  "counter_kind": "snapshot",
  "complete": true,
  "input_tokens": 1000,
  "output_tokens": 100,
  "cache_read_tokens": 600,
  "cache_write_tokens": null,
  "reasoning_tokens": 40,
  "raw_ref": "payloads/usage-17.json"
}
```

Normalized input includes cached input; cache reads/writes are subsets, not extra totals. Normalized output includes reasoning; reasoning is a subset. Preserve provider-native fields and cache TTL categories for pricing. Unknown fields are null, not zero. Unknown reasoning breakdown does not invalidate a fully observed output total.

Repeated stream usage snapshots replace the same measurement, never add together. Native thread cumulative counters require a known baseline and monotonic progression; a reset or missing baseline makes the delta unknown. HTTP and Agent reports for the same request are correlated and deduplicated using explicit measurement identity. Parent turn/run usage and child request usage must never both be added to a total. Validate disjoint coverage and reconciliation before choosing a total; retain mismatches as diagnostics, not silently repaired data.

Keep three independent quantities:

1. Observed actual usage: provider/native reports, with their measured scope and coverage.
2. Context/tool size estimates: estimator/tokenizer identity and version, never added to observed usage.
3. Reservations/limits: operational policy, not consumed tokens.

Stage attribution is exact only with corresponding request/lifecycle evidence. Tool execution itself has no model billing event: arguments contribute to model output; results contribute to later request inputs. With multiple tools in a batch, exact per-tool billing allocation is generally unavailable. Payload tokenization can explain size and repeated inclusion but must be labeled estimated attribution. An unobserved stage remains unknown rather than being assigned a convenient fraction of the total.

## Evaluation modes

| Mode | Controlled variables | Valid conclusion |
|---|---|---|
| Deterministic contract | Scripted model streams, usage, tools and failures | Correct continuation, receipt handling, usage aggregation and recovery; no empirical efficiency claim |
| Fixed-context/request comparison | Identical history, tool results and policy; observable encoded requests | Encoding overhead, context growth and trigger differences; token counts are estimated unless a provider supplies them |
| Aligned live execution | Same effective model, prompts, tools/execution target and enforceable policy | Runtime efficiency conditional on equivalent settings and outcomes |
| Native-policy live execution | Same task/execution target, each runtime's recorded defaults | End-to-end product behavior, including its tool/prompt/compaction policy |

Forced-compaction cases compare a fixed history and retained suffix. Automatic-compaction cases compare a long task under aligned thresholds or native defaults. Measure the summary request, subsequent requests and quality-driven rework together. Evaluate remembered constraints, artifact paths, completed effects and pending work after several compactions; a short inaccurate summary is a failure.

## Reports and statistical comparisons

The headline is cost/tokens for equivalent successful outcomes, alongside success rate and the cost of failed attempts. Also report observed input/output, cache and reasoning subsets, request/tool counts where observable, compaction cost/frequency, retries/rework, latency and precision/coverage. Separate Agent work from any optional model-based evaluator's own usage.

Cost requires a versioned price table for the actual provider/model/cache categories. Do not convert subscription usage into hypothetical billed cost. Keep token comparisons and priced cost comparisons separate when a valid price is unavailable.

Pair candidate/baseline runs by scenario and trial, alternate or randomize execution order, and start with fresh sessions/workspaces. A seed or low temperature does not guarantee deterministic models. Server-side cache state cannot always be forced cold: record reported cache usage and analyze cache conditions separately; avoid consistently giving one candidate the second, warmed request. Repeat promising cases more often, report paired deltas and distributions/confidence intervals where sample size permits, and retain every failed/timeout/budget-stopped run.

CI can enforce contract tests without networking. Live regression rules require explicit limits and sufficient repeats: fail task-quality regressions, and flag sustained token/cost changes exceeding a configured absolute and relative threshold. No scheduled paid runs are implied by this design.

## Delivery sequence and current status

The [small-budget suite](token-evaluation-next-suite.md) delivers at most five conversational turns, cache utilization, deterministic fault/retry contracts and directed serial two-child delegation, with [dated results](validation/small-budget-evaluation.md). Large-output and long/repeated-compaction tests are deferred for this increment. The broader sequence below remains long-term scope; generic experiment matrices, parallel scheduling and verified pricing are not delivered by this fixed suite.

1. Create the separate `pot-eval` runner, stabilize manifests/events/usage validation and implement a scripted Agent execution plus fixtures for duplicate snapshots, partial usage, counter resets, interrupted tools and compaction retries.
2. Define Core observations and inject standalone execution services; run pot-agent directly with a request collector. Deliver CLI commands to run cases, validate records and compare two run sets; JSONL/raw artifacts first, compact HTML report second.
3. Add short-chat, single/multiple tool rounds, large output, forced/automatic repeated compaction and post-compaction continuation cases with machine-checkable outcomes.
4. Add comparison baselines through the same execution contract. Baseline-specific tests do not change benchmark scenarios or scoring rules.
5. Establish live baselines and regression budgets once comparable real-provider measurements exist. Keep baseline runtime revisions and case versions pinned; version report formulas separately from raw observations.

The schema and aggregation validator should be the first concrete implementation. They are what make longitudinal results comparable; Agent implementations are replaceable inputs to that contract.

Current delivery: steps 1 and the Pot standalone/CLI portion of step 2 are implemented. Step 3 has short-chat, tool batch, shell-output-limit and repeated automatic-compaction scenarios/tests, including continuation assertions. Other live transports, repeat statistics and HTML reporting remain future work. The initial Core change adds opt-in observation to existing feedback and standalone services to the Pot host; a full decomposition of product feedback responsibilities is not claimed.

DeepSeek Flash live follow-up: the CLI reads the local Pi model configuration and runs only the selected Flash model through injected services. Native request/response streams are captured, and all three live task assertions and usage reconciliation passed. This is a single-runtime smoke baseline, not a multi-runtime efficiency comparison. See the [dated live acceptance](validation/token-evaluation.md#deepseek-flash-live-follow-up-2026-10-06).
