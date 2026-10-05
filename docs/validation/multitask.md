# Multitask implementation and acceptance

> Later changes: manual creation removed; main Agent creates tasks/schedules. Scheduling implemented, [contract](../scheduled-tasks.md). Below preserves historical acceptance/allowances/pending work.

Date: 2026-10-04. Independent tasks/configurable concurrency/guidance/cancel/recovery/automatic routing implemented and validated. Single-level delegation; no DAG/recursive delegation/strong desktop resource isolation.

## Usage

1. Main chat research/execution, Agent chooses delegation/reuse; manual creation now removed.
2. Separate conversation/Pi session/directory, default5 backgrounds configurable, excess queued; main separate slot.
3. Guidance shows Received then Applied when Pi consumes next turn, never inserted into current tool loop; ordered.
4. Stop/Cmd+. cancels target/its pending guidance, not others.
5. Results report main/idle summary, View task links; stable IDs share unread rules.
6. Restart resumes unstarted durable queue, active Interrupted without replay. Explicit guidance resumes after older unconsumed guidance; Stop clears obsolete queue.

Drafts persist per conversation during App run, not guaranteed after Quit.

## Implementation

```text
Pot App
  Main chat / task list / independent task conversations
                |
       Rust Control API
                |
      +---------+----------------------+
      |                                |
  Main execution slot          Persistent task directory / queue
      |                         Per-task exclusive + FIFO
  Pi main session --tools-->     Default five parallel
      |                          /           \
      |                    Task A worker  Task B worker
      |                    Pi session A   Pi session B
      |                    workspace/A    workspace/B
      +---- Persistent completion / summary <----+------+
```

- Main SQLite `tasks.v1` stores tasks/queue/active turns/receipts/durable sequence; same-millisecond inputs ordered by receipt.
- Task transcript SQLite/`pi-session.jsonl`; API real conversation ID, reused internal adapter `main` scoped to own DB.
- Persist active before dispatch; crash before confirmed process start still interrupted, no risky replay.
- Completion/report and directory state commit together in main SQLite.
- Retry receipts dedupe, same-ID changed content conflict; per-task cancel/count isolation.
- Shared global request ledger, per-turn Agent caps; concurrency cannot bypass total.
- Separate Pi gateway tokens route budgets/cancellation; workers cannot manage tasks.
- Next main input gets bounded latest8 status/results, not all transcripts or paid progress summaries.
- App500ms task/main/selected snapshots rejects stale; SSE retained, future event subscription scale.

## Free automated validation

**35** regular(App9/protocol1/Runtime25) plus**3 real Pi/local mock** pass without DeepSeek key.

Added/expanded coverage:

| Scenario | Validation |
|---|---|
| Two tasks/concurrency | Two occupy, third queues, FIFO refill |
| Main response | Main starts despite two busy background slots |
| Serial task | Guidance while A active cannot start overlapping A |
| Durable guidance | Persisted queue/ordered consume/receipt preserves active turn |
| Dedupe/conflict | Create/guide retries once, changed text rejected |
| Cancel isolation | A cancel leaves B, clears A guidance/no restart |
| Resources | Explicit `resource: shared` exclusive, independent runnable |
| Restart | Queued retained/active interrupted/no side-effect replay |
| Completion | Stable report ID, repeated finish no duplicate |
| Auth | A worker cannot read B/manage tasks |
| Real Pi processes | Semaphore blocks two requests simultaneously, proves overlap without sleep guessing |
| Tools/context | Separate files/requests; A guidance native resume |
| Real cancellation | Shell children/post-cancel/budget regression passes |

```sh
CARGO_TARGET_DIR=.local/poc/cargo-target cargo test --locked -p pot-app -p pot-runtime -p pot-protocol
POT_TEST_PI_BIN="$(command -v pi)" CARGO_TARGET_DIR=.local/poc/cargo-target cargo test --locked -p pot-runtime real_pi -- --ignored
scripts/pot build
scripts/pot bundle
scripts/pot open
```

Ignored logs `.local/d1/multitask-tests.log`, `multitask-pi-tests.log`, `multitask-build.log`, `multitask-bundle.log`.

## Real UI / model acceptance

- Native A `sleep 8; printf ALPHA > result.txt`, then completion reply.
- While A tool runs, API B and A guidance; B completes, A still active/pending1.
- A finishes then consumes guidance in same session, deployment-cost acknowledgment; file `ALPHA`.
- Task list/separate chat/Chinese paste/three main reports/links/drafts/unread pass.
- Quit/reopen both complete, A guidance/tools retained, ledger10/100, no auto requests.
- Fix native Edit validation/GPUI mutex conflict; native menu routes composer without automatic GPUI validation.
- **4 DeepSeek requests, 70 output**; noncached3767/cache4864/input8631, ledger**6->10/100**, no reset.

## Boundaries

- Main automatic delegate/reuse below; model/system prompt determine routing quality.
- `shared` cooperative scheduler lock, not desktop broker/security; bash bypass files/desktop possible.
- Separate directories reduce conflicts, not permissions; same Sandbox user.
- No automatic external retries/exactly-once, DAG/priorities/memory future; schedules later implemented.
- Then Pi-only backgrounds, no other drivers/cross-driver sessions.

## 2026-10-04: Configurable concurrency

Agent concurrent tasks default5/range1-1000, pi.json max_parallel_tasks, old missing defaults5. Runtime/UI read it, main separate/shared serial; save uses active-task check/restart.

No-model1/5/8 slot/FIFO tests, missing defaults5, explicit2 retained; 28 pass/2 external ignored. App/Runtime/Helper bundle/reopen AX5; no models.

## 2026-10-04: Automatic routing

Main Pi explicitly loads built-in create/list/get/guide/cancel extension.
Research/current retrieval/reports/multistep delegate; simple chat direct. Workers no extension,
only task context; guidance next turn, not realtime interrupt.

Deterministic coverage:

- No active main rejects tools; dedupe/conflicts/cross-turn same IDs.
- Worker recursive delegation rejected, guidance dedupe/cancel, summary cannot mutate.
- Out-of-order reports coalesce/delay if main busy; unclaimed survive restart, interrupted claimed summary no replay.
- Upgrade no historical resummary, exhausted allowance preserves notifications, failures enter summary.

`real_pi_automatic_routing_and_completion`: real Pi/mock greeting creates none,
then two tasks, independent real result.txt, original main summary.
Workers lack orchestration, summary only two read-only tools, correct links/no repeated wake.
8 mock requests, 0 API cost; protocol/execution, not real routing quality.

Notification claim/System message atomic, no claimed-summary retry; crash may fail summary,
original reports remain; same VM user/no DAG/no recursive delegation unchanged.

31 regular/3 real Pi mock pass. App/Runtime/Helper
bundle/reopen. Real guest DeepSeek natural web-title/file
request without explicit delegation -> pot_create_task, curl,
write/verify, auto summary. One complete task/file
`title: Example Domain`/source; 7 real requests, no config/reset.
UI report/link/summary. Then task brief prefers user language,
relative artifacts; summary System state updates complete/fail instead of permanent busy.

Final rebuild/Quit/reopen ready/task complete,
main idle/ledger27(previous20), no duplicate summary/calls.


## 2026-10-04: Task states / pagination

Complete/cancel archive, failed/interrupted needs attention; phase retained, guidance requeues.
Agent defaults active, 10/page/max50, scope/phase/q/offset. Main only
counts/max8 active-attention metadata, no old body. Existing Pi
history retained, no compaction/forgetting then.

Tests six states/exact-phase priority/CJK-case-ID search/no overlap,
stable equal-time sort/page shrink/invalid limits/off-page selection/archive-resume.
1000 archives default0/count1000, archive page20/context
under600 chars/no old titles. Stale search cannot overwrite query/chat; running
counts independent.35 regular/3 real Pi mock pass, 0 paid.

Kit Input/Button/ListItem/Ongoing-Attention-Archived/search/fixed pages,
20/latest-change; list scroll/filter-page fixed, archive actual state.

Reopen old4 archived, Chinese search2/unmatched0, archived
completed/history preserved; HTTP limit2, next_offset2, ledger27, 0 models.
