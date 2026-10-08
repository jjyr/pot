# Execution preference validation

Date: 2026-10-06. Issue #3 implementation in this worktree; not full release or all-provider acceptance.

## Delivered behavior

- One plain-text record with revision/update time, verbatim readback, replace/clear, stale-write conflicts and atomic idempotent mutation receipts.
- Task creation detects stale/missing preference revisions. Existing Profile authorization, capability checks, queueing and immutable bindings remain. Tasks record creation-time preference revision/reason.
- Chat running-task summaries and task-list rows reuse the existing brand icon assets, with no additional model/reason labels. Opening tasks retains existing model/switching interactions. Icon projection follows actual bindings across migration/restart and does not follow global Profile edits; paginated projection only reads bindings for visible rows.
- Usage adds a whole-main-turn routing subset, already included in totals, with date/scope precision, cache subsets, unknown usage and durable/deduplicated markers. No extra routing request.

## Automated and transport observations

Final backend regression: 115 passing tests across the backend crates (including 102 Runtime tests), plus 14 App tests. Rust workspace check, Swift Helper release build, formatting/script checks and whitespace validation pass.

Backend tests cover read/edit/clear/restart/idempotency/conflicts, credential-free eligibility, unauthorized/missing choices, Grok background rejection, task-only overrides not writing preferences, task-binding/icon retention, and main/worker/summary boundaries. Existing capability, provider/profile concurrency, schedule and immutable snapshot regressions are retained.

Metering tests cover requests before/after delegation in the same turn, multiple delegation attempts in one turn, worker exclusion even with the same turn ID, global date windows versus main lifetime scope, pagination independence, restart and native incomplete/reporting states. Actual totals do not add the routing subset or cache hits a second time.

Nine installed-Pi/local-mock integration tests pass. The added integration performs read -> save -> fresh read -> delegate -> reply with five real Pi gateway requests, checks schema exposure, preference revision, DeepSeek icon and whole-turn measured usage. Existing compaction/cancellation/allowance/resume/multi-provider/schedule tests also pass. These use no paid models.

## Real main-model semantic evaluation

The opt-in evaluation uses configured DeepSeek `deepseek-v4-flash` as the real main model and two **synthetic, unexecuted** worker Profiles named Claude Coding and Gemini Design. It tests semantic interpretation and tool invocation, not real Claude/Gemini worker inference. Workers remain queued in test fixtures; no worker-provider requests occur. Preference tools and task creation use actual Runtime operations in temporary product directories.

The initial 12-case run passed 11: coding, debugging, fiction, visual design, specific software-architecture exception, explicit task override, coding plus README, casual discussion, lasting save, complete readback, and clear. The document case had no source notes. A diagnostic rerun showed that the model asked for missing notes and explicitly identified Gemini Design as the intended executor, without editing the saved preference. The original expectation of unconditional task creation was invalid for that input.

The corpus now distinguishes a document task with supplied notes from a missing-input task (clarification or correct Gemini delegation is acceptable). Both targeted cases pass. Thirteen representative scenarios are therefore covered across these observations; this small sample is not a statistical routing-accuracy guarantee or a claim that all main models behave identically.

Measured paid main-model calls for these observations: 29 requests, 160,838 input and 7,133 output tokens (cache hits remain part of input). No production product records, preferences or ledgers are changed by the fixtures. Test directories are removed after completed evaluation.

## Reproduction

Explicit paid semantic evaluation, using a configured Pi models file (credential values are never printed):

```sh
POT_TEST_PI_BIN="$(command -v pi)" POT_TEST_REAL_PI_CONFIG=/absolute/path/to/models.json mise exec -- cargo test --locked -p pot-runtime real_model_execution_preferences_quality -- --ignored --nocapture
```

Set `POT_TEST_ROUTING_CASE=documents,documents-missing-input` to select cases. The paid evaluation is ignored by ordinary tests and by the `real_pi` local-mock test filter.

## Limits

Native rendered light/dark/minimum-window screenshots and keyboard/AX interaction were not repeated for this increment. App compilation/tests validate the Rust/Swift integration, not a live bundled App/VM visual pass. No OCI image or distributable bundle is rebuilt by these checks. Real Claude/Gemini worker-provider execution, expired provider credentials, and repeated/multi-main-model statistical routing evaluation remain separate acceptance work. Grok lacks preference orchestration tools and is not accepted as a background executor.
