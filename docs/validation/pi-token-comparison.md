# Same-command Pot / Pi token comparison

Date: 2026-10-06. This is a direct CLI experiment, not a Pi Driver integration. No Pi engine, adapter, tool bridge or runtime dependency was added. The evaluation-only Driver/bridge implementation started during discussion was removed when the user requested simple CLI measurement.

## Method

Both CLIs ran identical task text, model `deepseek-v4-flash`, thinking off, a 768-token per-response cap and explicit system prompt `Complete the task.`. Each case ran three times per runtime, using fresh workspaces/sessions. Runner order alternated by trial. All prompts and artifact assertions are preserved with the records; paired prompt strings were checked for byte equality.

Pi version: **0.87.1**. It used native tools (`read`, `write`, `edit`, `bash`) and native message serialization. Pot exposed its normal `apply_patch` and `exec_command` tools and used its own loop/serialization. The short-chat case also exposed tools on both runtimes, so its input cost differs from the earlier tool-free 20-token Pot smoke case. Pi extensions, skills, context files and prompt templates were disabled. Pi configuration was isolated per run, and its API credential was passed through an environment variable referenced as `$POT_EVAL_DEEPSEEK_KEY`; the original local Pi configuration was not changed.

This measures overall task execution under the two native tool sets. It does not hold tool definitions, encoded context or compaction policy identical. Both runtimes used a 180-second deadline; retries were disabled. Server cache state was not forced cold, and latency includes CLI startup. Every effective trial and failure is retained.

Pi usage was taken from one `message_end` event per assistant response. Other stream snapshots and repeated messages in `agent_end` were not added. Pi normalized input is `input + cacheRead + cacheWrite`; output is `output`. The normalized sum reconciled with Pi's `totalTokens` for every assistant response. Pot usage came from its provider-backed report. Input totals below already include cached input; cache reads are not added again. We did not install a Pi transport collector or capture its raw HTTP requests.

## Results

Means over three runs per runtime; fractional counts are averages.

| Task | Pot input | Pi input | Pot output | Pi output | Pot requests | Pi requests | Pot tool calls | Pi tool calls |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Exact short reply | 542 | 964 | 1 | 1 | 1.00 | 1.00 | 0.00 | 0.00 |
| Create file, read back and verify | 1,536.33 | 3,165.33 | 125.67 | 107.67 | 2.33 | 3.00 | 1.33 | 2.00 |
| Inspect/fix Python addition, run assertions | 3,791.67 | 4,576 | 226.67 | 186 | 4.67 | 4.00 | 3.67 | 3.00 |

Pot input was lower by **43.78%, 51.46% and 17.14%**, respectively. The fix task required more tool calls and requests in Pot despite lower input totals; its output usage was higher too. Pot sometimes combined file creation and inspection into one exec_command; Pi used separate write/read tools. Thus tool policy and chosen actions contribute to the differences.

All final files matched expected content. Both runtimes correctly fixed the Python function and ran passing assertions in all three fix trials. Strict response-format acceptance is a separate result:

| Task | Pot exact `done` | Pi exact `done` |
|---|---:|---:|
| Short reply | 3/3 | 3/3 |
| File task | 0/3 | 2/3 |
| Fix task | 3/3 | 3/3 |

The three Pot file-task replies and one Pi reply added a verification explanation before `done`. These remain strict task failures in their original reports; artifact correctness was not used to rewrite those reports. Means include these attempts, and there is no claim that lower usage establishes equivalent complete instruction-following.

No trial triggered compaction. This experiment provides no conclusion about relative compaction usage or retained-context quality. Three repeats are a small sample; different native schemas/messages and uncontrolled caching prevent attributing the input difference solely to the loop implementation. No actual billed currency cost or broad statistical superiority is claimed.

## Follow-up audit: resource loading and fixed overhead

The audit checked the installed **0.87.1** implementation, not only the Workspace Pi checkout (which is a different version). The recorded CLI disables extensions, skills, prompt templates and AGENTS.md/CLAUDE.md loading, and passes no explicit resource paths. Each run redirects `PI_CODING_AGENT_DIR` to its isolated configuration directory. All recorded Pi tool calls target only `result.txt`, `calc.py`, or the requested Python assertion command; no tool call reads a skill or additional context file.

An offline HTTP fixture captured a fresh Pi request with the same CLI flags and original short-chat workspace, using a dummy credential. No paid inference was performed. The capture contains exactly the explicit system prompt plus Pi's `<cwd>` section, the user message and four native tool declarations. It contains no skill, project-context or documentation sections. Pi can independently discover `APPEND_SYSTEM.md` even with a custom system prompt; neither its isolated configuration directory nor the workspace's `.pi` directory contained that file in any trial. The fixture is a reproduction, not a historical HTTP capture; records are under `audit/` in the comparison directory.

The serialized tool declarations contain **2,841 characters for Pi versus 1,152 for Pot**, using compact JSON with the same serialization for measurement. Characters are not token counts. Pot's stored first request contains only `Complete the task.` as its system message, whereas Pi adds the working-directory path. This establishes unequal fixed request overhead. The observed short-chat usage (964 versus 542 input tokens, one request and no tool executions on both sides) is consistent with that difference; exact token attribution between schemas, cwd and provider serialization has not been measured.

The file case also differs in action selection: Pi performs separate write/read calls, while Pot sometimes combines both actions in one shell call. The code-fix case uses more requests and output tokens in Pot. Consequently these results support lower input usage on these small native-tool tasks, not general runtime superiority or a compaction-efficiency claim. An isolated token ablation would be needed to quantify each source of overhead.

## Follow-up ablation: measured tool and cwd overhead

Date: 2026-10-06. Six configurations each ran three paid, fresh-session native CLI calls against the same DeepSeek Flash model, short-reply task, thinking-off setting and 768-token output cap. Trial order reversed for the second repeat. Pi's workspace path was held constant. The two additional Pot configurations inject exactly Pi's full system-message text, including its `<cwd>` section, to isolate the prompt difference without changing Pi or introducing an adapter. Both binary hashes match the earlier comparison. All 18 attempts succeeded with exactly `done`, one model request, one output token and zero tool calls or compactions. Each group's three input measurements were identical.

| Configuration | Input tokens |
|---|---:|
| Pot, no tools, short system prompt | 20 |
| Pot, native tools, short system prompt | 542 |
| Pi, no tools, native cwd section | 61 |
| Pi, native tools, native cwd section | 958 |
| Pot, no tools, system content matched to Pi | 61 |
| Pot, native tools, system content matched to Pi | 583 |

The measured marginal input cost of enabling native tools is **522 tokens for Pot** and **897 tokens for Pi**. Adding Pi's exact cwd section to Pot costs **41 tokens**, with and without tools. When system content is matched and tools are absent, both CLIs consume **61 tokens**, despite their different user-message serialization. The native-tool difference is then **375 tokens**. Thus the 416-token short-chat gap in this ablation decomposes into 375 tokens of marginal native-tool overhead and 41 tokens of cwd overhead. This measures declaration-related provider overhead, not tool execution or loop efficiency. The earlier Pi short-chat total was 964 rather than 958 because its workspace path differed; these experiments must not be treated as byte-identical inputs.

Offline captures using a dummy credential and local SSE fixture verified that Pi exposes zero/four tools in the respective configurations and that its system-message content exactly equals the matched Pot requests. The fixture usage values are synthetic and excluded from all measurements. Pi usage was again normalized from one assistant `message_end`, reconciled against its total; Pot's records were independently validated. Input includes cached tokens, regardless of the uncontrolled cache-hit differences. No billed-currency or compaction comparison is inferred.

The preserved experiment script, per-attempt stdout/stderr and configuration, Pot raw requests/responses, results, binary fingerprints, credential-free offline Pi captures and summary are under `.local/eval/pi-ablation-20261006/`. A credential scan passed. Local Pi configuration and product source were not changed. This is a single-response ablation; multi-turn quality and action-selection differences from the original comparison still apply.

## Records and setup failures

The 18 valid trials, exact experiment script, binary hashes, individual manifests/events/artifacts and computed summary are under `.local/eval/pi-comparison-20261006b/`. `summary.json` records Pi's version, both binary fingerprints, mean cache reads, outcome counts and observed tool names. This directory is ignored by Git. The experiment script skips previously recorded runs; a new output directory is required for new paid measurements.

An initial setup used a bare environment-variable name in Pi's temporary API-key field. Pi 0.87.1 treats that as a literal and requires `$VAR`/`${VAR}` interpolation. Its requests failed authentication; the associated zero usage was not counted as efficient inference. Those failed records and the first Pot attempts remain separately preserved under `.local/eval/pi-comparison-20261006/`. The corrected comparison reran both runtimes in fresh workspaces and alternated order. A full credential scan of valid experiment files passed without printing the key.

The product source was not changed for this comparison. `pot-eval` was rebuilt after removing the transient Pi integration code, and Rust checks/formatting and `git diff --check` passed. The underlying live runner's 144-test acceptance is recorded in [token evaluation validation](token-evaluation.md).
