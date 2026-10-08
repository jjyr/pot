# Pot evaluation CLI

`pot-eval` runs the real Pot Agent through the concrete `pot_agent::Agent::execute` entry point with injected execution services. It is a separate workspace crate with no dependency on Runtime, Storage or App internals. The CLI supports deterministic contract runs and bounded live Pot runs against **DeepSeek Flash only**, using local Pi configuration. Cross-runtime statistical comparisons remain pending. See the [framework design](../docs/token-evaluation.md) for the remaining modes.

## Run and inspect

From the repository root:

```sh
mise exec -- cargo run --locked -p pot-eval -- run eval/scenarios/tool-round.json /tmp/pot-eval-baseline
mise exec -- cargo run --locked -p pot-eval -- run eval/scenarios/tool-round.json /tmp/pot-eval-candidate
mise exec -- cargo run --locked -p pot-eval -- validate /tmp/pot-eval-candidate
mise exec -- cargo run --locked -p pot-eval -- compare /tmp/pot-eval-baseline /tmp/pot-eval-candidate
mise exec -- cargo test --locked -p pot-eval
```

Use a new directory for every run; existing runs are never overwritten. The parent directory must exist. Tools execute on the CLI host in the fresh run workspace; this runner does not create a VM sandbox. The supplied fixtures use only that workspace. A run deadline triggers cooperative cancellation and waits up to ten seconds for cleanup; unconfirmed cleanup is a failed run. The `run` command generates responses from native SSE fixtures without networking. The explicit `live` command calls the selected DeepSeek Flash API.

`run` exits unsuccessfully when execution or task assertions fail. `validate` reconstructs the report from records rather than trusting `report.json`; invalid records cause a nonzero exit, while valid records of a failed task remain inspectable. `compare` requires identical scenario hashes/schema/mode and reports whether successful outcomes with complete usage can be compared. Deterministic comparisons always state `empirical_efficiency_claim: false`.

## DeepSeek Flash live tests

### Small-budget suite

```sh
mise exec -- cargo run --locked -p pot-eval -- suite deterministic /tmp/pot-small-contract
mise exec -- cargo run --locked -p pot-eval -- suite live /tmp/pot-small-live
# Rerun only directed delegation, without repeating memory/cache/single-worker measurements:
mise exec -- cargo run --locked -p pot-eval -- tree live /tmp/pot-small-tree
# Optional direct Pi CLI baseline; ten paid single-response requests, no Driver adapter:
mise exec -- python eval/experiments/pi-small-suite.py /tmp/pot-small-live /tmp/pi-small-baseline
```

Use fresh output directories. The suite has a five-turn, tool-free retention task, a five-request fresh-session cache sequence, a single-worker/serial-two-child comparison and eleven deterministic failure/receipt cases. A live suite caps all Pot Agent requests across the sweep at 22 attempts and 50,000 encoded-byte/output reservation units, with 64/128-token response caps and thinking/compaction disabled. The tree has a shared eight-attempt/30,000-unit budget; `tree` applies it to a standalone rerun too. Budget units are conservative reservations, not actual consumed tokens. All tools run in the explicitly recorded local fixture environment, without creating a VM.

Delegation uses an eval-only task-management extension and real child Agent executions; the parent exposes only the dispatch tool, while children read their own partition through exec_command. Tree totals sum disjoint parent/child request usage. The suite records task lifecycle, individual child reports, per-turn answers/usage, cache-request identity checks and shared budget snapshots. JSON assertions allow insignificant JSON whitespace/key-order differences. Ordinary `compare` still requires identical scenarios/policies; architecture-changing comparisons use the suite's explicitly labeled reports, not a bypass of that check.

The fault cases inject typed pre-response HTTP/connection failures and an interrupted native stream through the service boundary. They make no provider calls, even during `suite live`. Optional retries in these fixtures are an explicitly declared eval transport policy (two attempts, recorded one-millisecond delay), not a change to production automatic retry or a test of HTTP-header parsing. Tool recovery and duplicate-effect receipts exercise real local shell calls. Interrupted/failed requests without usage keep unknown consumption. Expected failures pass their contract check while their native run reports remain failed. These cases do not establish real-provider retry cost.

The Pi script uses isolated configuration and direct CLI RPC for five sequential memory turns, then five fresh cache sessions. It disables resources and automatic retries, uses native tools only in cache cases, keeps local Pi configuration unchanged and scans retained artifacts for the credential. It is an optional external experiment and is not part of `pot-eval` dependencies. The Rust suite's limits do not cover Pi; its baseline consists of ten requested model responses, with a 64-token cap, per-turn deadline and termination on unexpected tool execution.

See the [suite design](../docs/token-evaluation-next-suite.md) and [dated results](../docs/validation/small-budget-evaluation.md). Capability-generic experiment matrices, parallel scheduling, active-tree restart/cancellation studies, verified pricing and statistical comparisons remain future work.

Only the DeepSeek provider and its configured `deepseek-v4-flash` (or `deepseek-flash`) model are selected. The default configuration source is `~/.pi/agent/models.json`; no Pi runtime/library is loaded and no source configuration is modified. Literal API keys or environment-variable references are read in memory. Command-based secrets and custom headers are unsupported. Credentials and authorization headers never enter manifests or captured requests.

```sh
mise exec -- cargo run --locked -p pot-eval -- live eval/scenarios/deepseek-short-chat.json /tmp/pot-live-chat
mise exec -- cargo run --locked -p pot-eval -- live eval/scenarios/deepseek-tool-round.json /tmp/pot-live-tools
mise exec -- cargo run --locked -p pot-eval -- live eval/scenarios/deepseek-compaction.json /tmp/pot-live-compact
# Optional fourth argument: explicit Pi models.json path.
```

These commands make paid API calls. The supplied cases disable thinking, bound each response to 512/768 output tokens and use 180/240-second run deadlines. Default `LiveLimits` permits at most eight requests and reserves at most 100,000 units across attempts, each unit bound calculated as encoded request bytes plus its output cap. This conservative operational bound is separate from reported actual usage; it is not a provider billing guarantee. Limits can be set through `run_deepseek` in code. Redirects and automatic HTTP retries are disabled.

The model ID, endpoint, requested context/output policy, effective thinking/effort compatibility, configured price snapshot and limits are recorded. Configured prices remain unverified and are not used to claim actual billed cost. The compaction case deliberately disables usage calibration and uses a low byte-estimation threshold to reliably exercise compaction with a fixed history; it is not the default product policy. Its final artifact must retain both critical facts without rereading the source file.

Live runs use `native_policy_live` mode and store streamed `requests.jsonl`/`responses.jsonl` alongside the shared events. Request records contain the final compatibility-adjusted native body; response records contain exact received byte chunks keyed by request ID, permitting independent usage replay. Those two files use owner-only permissions. Partial/failed requests and their available observations are retained. Live comparisons additionally require matching transport configuration hashes; the initial comparison command does not make a statistical efficiency claim.

## Scenario contract

Scenarios contain an immutable identity, a Profile and native protocol, ordered user turns, workspace text fixtures, expected file contents/final text and a run deadline. Native model response frames are consumed per request; each scripted response declares `agent` or `compaction` and may assert fragments of the encoded request. Missing, extra or misordered responses fail the run. Tool names and effects use the production Agent implementation. Profile Agent settings configure prompt, tools, context/compaction thresholds and shell output limits.

The initial hash covers the entire scenario, including configuration and scripted responses. It permits strict same-case comparisons across builds. Separate workload/policy identities and policy-changing comparisons are future work. `POT_EVAL_REVISION` can be set at **build time** to pin the runtime revision; absent revision provenance is recorded as null, never guessed from the checkout.

## Records

Each run contains:

- `manifest.json`: schema/run/scenario identity, effective resolved configuration, executor identity and build provenance.
- `scenario.json`: the complete scenario used for replay.
- `events.jsonl`: append-only ordered Agent events and typed execution observations, including usage snapshots and causal compaction IDs.
- `requests.json`: credential-free native encoded model requests captured by the scripted service.
- `workspace/`: fresh fixtures and final artifacts.
- `context.json`: final Agent context for diagnosis; persistence during execution is run-local memory.
- `execution.json`: execution/deadline/cleanup/unused-trace failures.
- `report.json`: recomputable task status, measurement coverage, total/stage input/output/cache/reasoning usage and request/tool/compaction counts.

Unknown usage/subsets remain null. Cache/reasoning are included in input/output totals, not added again. Stream snapshots replace previous values for a measurement. Usage normalization supports explicit request/turn/run scopes, cumulative counters with a stable known baseline and event-deduplicated deltas; conflicting sources, resets and overflows are rejected. Failed attempts retain observed usage. Context estimates are separate events and never contribute to observed token totals.

The validator checks manifest/configuration consistency, envelope and Agent sequence/terminal rules, request/tool/compaction lifecycle, usage semantics and task assertions. It requires matching explicit measurement IDs to deduplicate sources; it does not infer arbitrary overlapping parent/child scope coverage. JSON records are a local diagnostic format, not a tamper-proof audit system.

## Integration boundary

Core declares execution context/events, `ExecutionObservation` and `ExecutionServices`. The concrete `pot_agent::Agent` accepts injected services and extension hooks; no driver/session adapter is involved. The normal product host continues to call its authenticated HTTP services and settle usage through Runtime. Observations do not authorize requests or replace reservations.

`run_agent` is the runner entry point for the concrete Agent executor. The CLI constructs pot-agent with scripted transport or the DeepSeek Flash live service. Other live models, paired repeats, verified price tables, statistical regression budgets and HTML reports are also pending. Paid DeepSeek calls occur only through the explicit `live` entry point; no scheduled evaluations are enabled.
