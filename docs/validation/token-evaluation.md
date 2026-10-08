# Token evaluation foundation acceptance

Date: 2026-10-06. Scope: standalone deterministic evaluation and Driver service/observation extensions. The [CLI guide](../../eval/README.md) defines the delivered contract; the [framework design](../token-evaluation.md) lists future modes.

## Delivered

- Separate `pot-eval` workspace crate and `run`, `validate`, `compare` CLI commands; no Runtime/Storage/App dependencies or separate Agent adapter contract.
- Real Pot loop and native SSE decoding against scripted streams, fresh workspaces, real apply_patch/shell effects, run deadlines, cancellation cleanup and final artifact/answer assertions.
- Versioned manifest, append-only JSONL events, native encoded requests, context, failure records and recomputable reports. Task success and measurement completeness are independent.
- Usage normalization with explicit request/turn/run scope, source deduplication, snapshot replacement, cumulative baseline/reset validation, deduplicated deltas, overflow checks and null unknowns. Cache and reasoning subsets are reported without increasing input/output totals. Failed executions retain observed usage.

## Verification

`mise run test` passed **140 backend tests** (125 existing plus 15 evaluation tests), and `mise run check` passed source lint, Rust workspace checks and Swift Helper compilation. The final targeted `cargo test --locked -p pot-eval` also passed after the last validation changes. `git diff --check` passed. No Linux image, VM or App interactive acceptance was performed in this increment.

The 15 evaluation tests cover four native protocols with real shell effects, patch/tool batches and bounded output, repeated compaction/continuation, missing and zero usage, usage on failed Responses streams, cache/reasoning subsets, unused response fixtures, artifact failures, record/path validation, process cleanup after deadline, duplicate snapshots/sources, parent scope exclusion, partial usage, cumulative missing/changing baselines and resets, delta deduplication and overflow.

Manual CLI runs used new directories under `/tmp`:

- `tool-round.json` baseline and candidate both completed two tools and two model requests. Recomputed comparison reported zero input/output delta and explicitly rejected an empirical efficiency claim.
- `compaction.json` completed six requests and two compactions, recording 60 input and 30 output tokens from scripted usage. The two compaction requests contributed 20 input and 10 output tokens; ordinary requests contributed 40 input and 20 output tokens.

All model generation used local fixture frames. These numbers validate bookkeeping and stage allocation, not real provider consumption or relative runtime efficiency. No provider credentials, paid calls or scheduled evaluations were used.

## Remaining scope

## DeepSeek Flash live follow-up (2026-10-06)

Thinking was disabled for all three runs. Responses were capped at 512 tokens for short chat and 768 for the tool/compaction cases. Run deadlines were 180/240 seconds; every run was bounded by eight requests and a conservative cumulative 100,000 encoded-request-byte/output-cap reservation. These reservation units are operational limits, separate from observed provider usage. The compaction case used a 65,536-token configured window, threshold 6,000 and `use_provider_usage: false` to reliably test local-estimator-triggered compression. No automatic HTTP retry or redirect was enabled.

| Case | Outcome | Requests | Tools | Compactions | Input tokens | Output tokens | Cache read subset |
|---|---|---:|---:|---:|---:|---:|---:|
| Short chat | Exact final answer passed | 1 | 0 | 0 | 20 | 1 | 0 |
| Tool round | apply_patch created APRICOT file; exec_command read it; final answer passed | 3 | 2 | 0 | 2,058 | 155 | 1,152 |
| Compaction continuation | Source read; compaction; patch preserved `APRICOT\|73`; final answer passed | 5 | 2 | 1 | 13,071 | 345 | 2,176 |

The compaction request consumed **5,162 input and 218 output tokens**. The other four requests in that run consumed 7,909 input and 127 output tokens. Cache read counts are already included in input totals. Cache write and reasoning breakdowns were absent and remain null.

All runs reported complete usage. Independently decoded raw response chunks and last usage snapshots reconciled with report totals for each run. A credential scan using the configured key verified its absence from every recorded file, without printing the key. The source Pi configuration was not changed. Credential-free manifests capture effective compatibility/limits and the configured price snapshot with `price_verified: false`; no claim of actual billed currency cost is made.

Run records are preserved under `.local/eval/deepseek-flash-20261006/{chat,tools,compact}` with `summary.json`; this directory is ignored by Git. Initial execution directories were `/tmp/pot-deepseek-live-20261006-{chat,tools,compact}`. Supplied scenarios under `eval/scenarios/deepseek-*.json` reproduce the workload, but outputs, caching and usage are not deterministic.

## Same-command Pi CLI comparison follow-up (2026-10-06)

At the user's request, Pi was compared through its existing CLI without implementing a Pi Driver or connecting tools. Three identical commands were each repeated three times with both native tool sets. See the [comparison method, outcomes and limits](pi-token-comparison.md). This supersedes the earlier observation that no Pi experiment had been performed, but does not establish a compaction or aligned-policy benchmark.
