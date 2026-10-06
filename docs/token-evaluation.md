# Long-term Agent token evaluation (design proposal)

Status: architecture accepted; deterministic foundation and DeepSeek Flash live tests delivered, 2026-10-06. This document defines the long-term framework. The standalone `pot-eval` crate implements the initial deterministic runner and usage validator; bounded DeepSeek Flash live execution is available; native baseline execution and broader comparisons remain planned. See the [CLI guide](../eval/README.md). It has no Pi-specific data model and does not change product execution or billing. The [runtime contract](../runtime/README.md) describes current Pot behavior.

## Objective

Measure the tokens, cost, latency and work required to complete reproducible scenarios across runtime versions and implementations. Comparisons must retain task quality, failures and observation precision: a cheap incomplete task is not an efficiency improvement. The same framework should compare Pot against an earlier Pot revision, Pi, Codex or a future Agent without changing scenarios or aggregation rules.

## Components and boundaries

| Component | Responsibility |
|---|---|
| Scenario catalog | Versioned user turns, workspace fixtures, environment, task assertions, resource limits and optional controlled response trace |
| Runner | Run matrix, isolated fresh sessions/workspaces, repeats, ordering, deadlines, lifecycle and artifact collection |
| Driver host/session | Reuse the Core execution contract to launch/configure an implementation, send turns, cancel and report capabilities, effective configuration and observations |
| Environment/tool service | Prepare identical fixtures and, where supported, common tool semantics/results for controlled comparisons |
| Usage collector | Capture provider usage or native counters and declare what scope/stages are observable |
| Normalizer/validator | Convert observations into a versioned schema, resolve cumulative snapshots and duplicate sources, preserve missing fields and raw provenance |
| Evaluator/reporter | Evaluate artifacts/answers, compute comparable metrics, compare baselines and explain regressions by trace |

The runner is a separate workspace crate, `pot-eval`, under `eval/`. It depends on shared contracts in `pot-core` and composes hosts from `pot-agent` and `pot-driver`, with `pot-gateway` where direct transport or collection is needed. It does not depend on `pot-runtime`, product Storage or App. The crate owns scenario execution, immutable run records, validation, scoring, comparison and the CLI. The initial crate and scripted Pot runner are implemented; the remaining delivery sequence is listed below.

Use the existing `DriverHost`/`SessionHandle` contract rather than creating a second Agent adapter layer. Pot already implements this contract; native Codex execution uses it too. A future Pi baseline would implement the same Driver contract as an optional evaluation integration, without restoring Pi as a production engine. Vendor protocol translation remains inside each Driver implementation. Scenario definitions and aggregation do not depend on those protocols.

Execution and usage observation remain separate concerns: several Drivers may share a gateway collector, and one Driver may expose different observations depending on its transport. Environment preparation is replaceable, allowing local fixtures or an existing Sandbox without requiring product UI.

## Driver extensions and capability negotiation

A Driver accepts shared execution configuration and emits ordered lifecycle events. Keep `probe`, `session` and `start_turn`; extend the contract with capability description and explicit session shutdown. Cancellation must be usable while `start_turn` holds the mutable session, through an independent control handle or the existing cancellation service. Optional controls include steering, pause/resume, checkpoint/restart and forced compaction. Tools and policy configuration must report requested and effective values; unsupported settings are not silently assumed to apply. Evaluation run IDs, trial ordering and scoring rules stay in `pot-eval`, not in Driver configuration.

Capability description declares control capabilities separately from observation capabilities:

- Control: configurable prompt/model/output cap/context window, custom tools, deterministic response replay, forced compaction, context retention, fresh session, restart/checkpoint.
- Observation: complete run/turn/request usage, request bodies, tool arguments/results, compaction lifecycle and usage, reasoning/cache fields, request IDs, cumulative counters.

A comparison declaring identical tools or compaction thresholds requires Drivers that can enforce those controls. A Driver lacking them can participate in a native-policy comparison, not an aligned-policy claim. Codex native cumulative turn usage can be a valid total measurement while request count and compaction allocation remain unavailable. A configured API-backed Codex variant may expose request usage through a collector; do not assume its subscription-backed variant permits the same observation.

### Standalone execution services

The current contract avoids a Runtime dependency, but standalone construction still needs work: `ExecutionContext` carries `backend_url` and `lane_token`, and Pot uses product HTTP endpoints for gateway/tool/compaction services. `ExecutionFeedback` also mixes cancellation, context persistence, binding validation, usage settlement and diagnostics. Merely reusing the trait does not remove those service requirements.

Introduce injectable execution services for model transport, external tools and compaction transport where applicable; keep the Agent's loop, context policy and compaction decisions in the Agent. Separate context persistence/control, authoritative settlement and typed observation responsibilities. Runtime supplies its existing authenticated gateway, storage and binding/ledger implementations. `pot-eval` supplies standalone transport, run-local context storage, tool services and controls. Product reservations must still be persisted before networking; an observer cannot authorize or replace settlement.

Add a typed execution observer in Core for request lifecycle, usage reports, compaction lifecycle, context estimates and truncation decisions. Reuse existing tool/message events and their epoch/turn/sequence validation. Request/attempt IDs link provider observations to Driver events; retries get distinct attempts. Observation is an opt-in stream, independent of UI projection and metadata-only `diagnostic`. Sensitive payload capture is a separate explicit option, with credential redaction and run-local retention; credentials never enter manifests or raw request records.

Core contains runtime-neutral capabilities, service contracts and observation types. The evaluation schema adds run/trial identity and serialized provenance around those observations. Drivers report supported measurements rather than inventing missing request or stage detail. Collector failures invalidate measurement coverage; they must not silently produce a successful comparable record.

## Scenario and run identity

Each scenario pins an ID/version, user input sequence, fixture content hash, environment requirements, expected outcomes, tags and limits. Assertions check task behavior rather than implementation-specific tool names unless the case explicitly tests a tool contract. Optional trace fixtures control model responses/tool results for causal debugging; they do not provide live billing evidence.

Every immutable run manifest records:

- Schema version, run ID, comparison group/pair ID, scenario version/hash and trial number.
- Driver version, runtime commit/build, requested/effective model/provider and generation settings.
- Prompt/tool-set hashes, runtime policy including compaction/retention/output limits, environment/image/fixture hashes.
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

Repeated stream usage snapshots replace the same measurement, never add together. Native thread cumulative counters require a known baseline and monotonic progression; a reset or missing baseline makes the delta unknown. HTTP and Driver reports for the same request are correlated and deduplicated using explicit measurement identity. Parent turn/run usage and child request usage must never both be added to a total. Validate disjoint coverage and reconciliation before choosing a total; retain mismatches as diagnostics, not silently repaired data.

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
| Aligned live execution | Same effective model, prompts, tools/environment and enforceable policy | Runtime efficiency conditional on equivalent settings and outcomes |
| Native-policy live execution | Same task/environment, each runtime's recorded defaults | End-to-end product behavior, including its tool/prompt/compaction policy |

Forced-compaction cases compare a fixed history and retained suffix. Automatic-compaction cases compare a long task under aligned thresholds or native defaults. Measure the summary request, subsequent requests and quality-driven rework together. Evaluate remembered constraints, artifact paths, completed effects and pending work after several compactions; a short inaccurate summary is a failure.

## Reports and statistical comparisons

The headline is cost/tokens for equivalent successful outcomes, alongside success rate and the cost of failed attempts. Also report observed input/output, cache and reasoning subsets, request/tool counts where observable, compaction cost/frequency, retries/rework, latency and precision/coverage. Separate Agent work from any optional model-based evaluator's own usage.

Cost requires a versioned price table for the actual provider/model/cache categories. Do not convert subscription usage into hypothetical billed cost. Keep token comparisons and priced cost comparisons separate when a valid price is unavailable.

Pair candidate/baseline runs by scenario and trial, alternate or randomize execution order, and start with fresh sessions/workspaces. A seed or low temperature does not guarantee deterministic models. Server-side cache state cannot always be forced cold: record reported cache usage and analyze cache conditions separately; avoid consistently giving one Driver the second, warmed request. Repeat promising cases more often, report paired deltas and distributions/confidence intervals where sample size permits, and retain every failed/timeout/budget-stopped run.

CI can enforce contract tests without networking. Live regression rules require explicit limits and sufficient repeats: fail task-quality regressions, and flag sustained token/cost changes exceeding a configured absolute and relative threshold. No scheduled paid runs are implied by this design.

## Delivery sequence and current status

The [small-budget suite](token-evaluation-next-suite.md) delivers at most five conversational turns, cache utilization, deterministic fault/retry contracts and directed serial two-child delegation, with [dated results](validation/small-budget-evaluation.md). Large-output and long/repeated-compaction tests are deferred for this increment. The broader sequence below remains long-term scope; generic experiment matrices, parallel scheduling and verified pricing are not delivered by this fixed suite.

1. Create the separate `pot-eval` crate, stabilize manifests/events/usage validation and implement a scripted Driver plus fixtures for duplicate snapshots, partial usage, counter resets, interrupted tools and compaction retries.
2. Extend Core Driver capabilities/observation and inject standalone execution services; run the existing Pot host directly with a request collector. Deliver CLI commands to run cases, validate records and compare two run sets; JSONL/raw artifacts first, compact HTML report second.
3. Add short-chat, single/multiple tool rounds, large output, forced/automatic repeated compaction and post-compaction continuation cases with machine-checkable outcomes.
4. Run the existing native Codex Driver with explicit observation limits, then add another baseline through the same Driver contract. Driver-specific compatibility tests do not change benchmark scenarios or scoring rules.
5. Establish live baselines and regression budgets once comparable real-provider measurements exist. Keep baseline runtime revisions and case versions pinned; version report formulas separately from raw observations.

The schema and aggregation validator should be the first concrete implementation. They are what make longitudinal results comparable; Driver implementations are replaceable inputs to that contract.

Current delivery: steps 1 and the Pot standalone/CLI portion of step 2 are implemented. Step 3 has short-chat, tool batch, shell-output-limit and repeated automatic-compaction scenarios/tests, including continuation assertions. Native Codex only declares its observation capability; its evaluation feedback/runner, forced compaction controls, other live transports, repeat statistics and HTML reporting remain future work. The initial Core change adds opt-in observation to existing feedback and standalone services to the Pot host; a full decomposition of product feedback responsibilities is not claimed.

DeepSeek Flash live follow-up: the CLI reads the local Pi model configuration and runs only the selected Flash model through injected services. Native request/response streams are captured, and all three live task assertions and usage reconciliation passed. This is a single-runtime smoke baseline, not a Pi/Codex efficiency comparison. See the [dated live acceptance](validation/token-evaluation.md#deepseek-flash-live-follow-up-2026-10-06).
