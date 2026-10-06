# Small-budget Agent evaluation: 2026-10-06

Implemented in `pot-eval`: optional per-turn text/JSON assertions and request-derived turn usage, a bounded five-turn retention case, five cache requests with fresh sessions, directed serial delegation to two real child Driver sessions, shared request/reservation budgets and eleven deterministic failure/receipt cases. No product retry policy, Pi dependency, Driver adapter, main-Agent isolation or VM scheduling changes were added.

## Live method

One trial with local Pi-configured `deepseek-v4-flash`, thinking off, compaction off and output caps of 64 tokens (memory/cache) or 128 (workers/parent). Pot used the standalone injected transport. Pi used its existing CLI RPC mode for sequential memory turns, and a fresh process for each cache request. Pi resources, sessions and retries were disabled and configuration isolated. Original local configuration was not modified.

User tasks and requested system prompt matched. Pi adds its cwd section. Memory exposes no tools on either side. Cache exposes only exec_command on Pot and the four default native tools on Pi, so raw-input/cache ratios are native-policy observations rather than an equal-tool comparison. Cache population/eviction is uncontrolled; Pot ran before Pi and the repeated requests were separated by recorded short delays. No verified currency pricing or statistical superiority is claimed.

## Five-turn retention

Both Agents retained the facts, applied replacements and returned the correct JSON. Intermediate acknowledgements also passed.

| Turn | Pot input | Pi input | Pot output | Pi output |
|---|---:|---:|---:|---:|
| 1 | 34 | 75 | 1 | 1 |
| 2 | 49 | 90 | 1 | 1 |
| 3 | 66 | 107 | 1 | 1 |
| 4 | 80 | 121 | 1 | 1 |
| 5 | 103 | 144 | 22 | 22 |
| Total | 332 | 537 | 26 | 26 |

Both grew by **69 input tokens** from the first to fifth turn. Pi's input is consistently 41 tokens higher per turn, consistent with the cwd overhead observed in the earlier ablation. This provides no evidence that Pot's history grows more efficiently on this case. Neither runtime reported cached input in this small tool-free workload.

## Cache sequence

All five exact-output assertions passed on both sides. Captured Pot request bodies verified byte-identical A1/A2/A3 bodies, stable system/tools for the tail mutation, and stable user task/tools for the early-system mutation. Pi requests were configured identically within its sequence but historical raw HTTP bodies were not captured.

| Case | Pot total input | Pot cached | Pot uncached | Pi total input | Pi cached | Pi uncached |
|---|---:|---:|---:|---:|---:|---:|
| A1 | 420 | 0 | 420 | 957 | 0 | 957 |
| A2, exact repeat | 420 | 256 | 164 | 957 | 768 | 189 |
| A3, exact repeat | 420 | 256 | 164 | 957 | 768 | 189 |
| Changed task suffix | 420 | 256 | 164 | 957 | 768 | 189 |
| Changed early system prefix | 420 | 0 | 420 | 957 | 0 | 957 |

Both observed cache reuse on repeated requests and the tail mutation, and zero cached input after the early-prefix mutation in this sweep. Pi's warm cached fraction was larger (80.25% versus 60.95%), largely associated with its larger fixed native request prefix. Warm uncached input was much closer: **164 versus 189**, despite total inputs of 420 versus 957. Cached tokens are included once in total input; savings in currency cannot be inferred from raw input totals alone.

## Single worker versus two-child task split

The same six CSV amounts must produce `{"count":6,"total":21,"max":6}`. A single worker reads both partitions. The split parent exposes only the dispatch tool; two real child Agents each read their own partition and return verified statistics. All successful results and child-read requirements passed. The split runs serially in local fixture workspaces; it does not evaluate product sandboxing, production task scheduling, parallel latency or autonomous task-split decisions.

| Execution | Input | Output | Total | Model requests |
|---|---:|---:|---:|---:|
| Single worker | 2,558 | 248 | 2,806 | 4 |
| Split parent | 835 | 91 | 926 | 2 |
| Left child | 981 | 57 | 1,038 | 2 |
| Right child | 981 | 57 | 1,038 | 2 |
| Complete split tree | 2,797 | 205 | 3,002 | 6 |

The split uses **9.34% more input**, **17.34% less output**, and **6.99% more total tokens** in this trial. The worker made three shell calls while each child made one; action selection and native tool sets differ. Small independent tasks may cost more when split, and these results do not establish how larger tasks or parallel execution behave. Tree totals reconcile to the sum of disjoint parent/child request measurements; parent usage alone would substantially understate split cost.

### Retained setup failure

The first eval dispatch tool mistakenly supplied `parameters` instead of the extension contract's `inputSchema`. The encoded model declaration therefore had null parameters, and no child was started. Its failed parent report remains retained: **667 input, 95 output, two requests**. The field was fixed and deterministic encoded-request assertions were added. Only delegation was rerun; successful memory/cache/single-worker measurements were not repeated. The corrected tree used six additional requests. The setup failure is excluded from the successful split comparison but included in overall experiment consumption.

## Deterministic failures and acceptance

All eleven fault/receipt contracts passed without provider calls: control, default retry disabled, one 429, one 503, pre-response connection failure, authentication error, invalid-request error, retry exhaustion, partial-stream interruption, recoverable shell failure and repeated successful tool-call receipt. Recoverable eval transport cases used two distinct physical attempt identities; permanent/stream failures did not replay the model request. The fixture policy records a one-millisecond delay and is not a test of native HTTP Retry-After parsing or a change to production transport behavior. Failed requests without usage remain unknown, not zero. Tool receipt replay appended its side-effect marker exactly once.

Additional automated checks cover failed intermediate output despite correct final output, swapped child execution order, duplicate dispatch, failed child budget exhaustion without replacement, reservation/request limits and tree aggregation. Deterministic usage is synthetic and excluded from paid totals. Active-tree cancellation/restart and parallel child scheduling remain untested follow-ups.

Verification: `mise run check` passed, and `mise run test` passed **149 backend tests**, including 24 tests in pot-eval. `git diff --check` passed.

## Records and spend

Records live under ignored `.local/eval/small-suite-20261006/`:

- `pot/`: initial suite, including the preserved failed parent and successful memory/cache/single-worker runs.
- `pot-tree-repair/`: corrected parent and two children, raw transport records and reconciled tree result.
- `pi/`: native CLI manifests, events, per-turn results and baseline summary.
- `deterministic/` and `deterministic-final/`: original and corrected offline acceptance runs; the corrected run includes encoded delegation-schema assertions.
- `comparison.json`: combined observations, failure classification and experiment usage.

Provider-reported experiment consumption, including the setup failure: **32 requests, 13,776 input tokens, 612 output tokens, 14,388 total tokens**. Pot used 16 requests in the initial sweep and six in the corrected tree; Pi used ten. This remains separate from encoded-byte/output reservations. No additional paid repeats, large output or long-context tests were run. All 22 Pot run records from the initial live suite and tree repair (including synthetic fault cases and the failed live parent) passed independent record validation; passing validation does not turn an expected/actual task failure into a task success. Tree usage reconciled and a full artifact credential scan passed.
