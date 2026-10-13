# Task Agent destination foundation — 2026-10-11

## Scope

P4 foundation only. `pot-runtime::task_agent` provides a destination-owned launch
journal and a fresh worker loop with explicitly injected model, approval/control
and tool-execution services. No assistant database or provider credentials are
needed on the destination. Production Control transport and Task dispatch do not
use this port yet; the current product still runs remote worker loops at Control.
This report does not establish end-to-end remote Task Agent delivery.

## Checks

- Eight concurrent identical deliveries produce one execution claim. Conflicting
  launches fail; terminal results survive reopening and duplicate delivery.
- A new boot fences unfinished runs as recovery-required. Neither the accepted
  input nor a replacement run automatically replays them. Unknown outcomes cannot
  become successful terminal results.
- Launch validation binds exact model configuration, resource snapshot, observation
  mode and destination Execution target; invalid inputs fail before acceptance.
- A neutral Runtime with Host enabled and no assistant runs an actual pot-agent
  loop against injected, credential-free fixture services. The Control readiness
  gate blocks its first model request until released.
- Actual project AGENTS.md and project Skills plus supplied Control Skills enter
  the model request. Destination-level instructions do not enter it; assistant
  instruction-edit tools are absent for the worker. No provider or assistant DB
  file is created.
- A model-requested Execution target switch returns the explicit fixed-target error
  to the model before reaching the tool executor. Plain text results contain no
  fabricated file link. An exact duplicate run makes no second model request.

The workspace identity uses the same Control/task/target hash as existing remote
file access. Agent context is persisted locally; it is not replicated live to
Control. The server must supply run-bound, receipt-backed tools and authenticated
Control ports before enabling production dispatch.

## Limits

The model/control services are deterministic fixtures in one process. Actual
network model streams, owner approval relay, execution receipts, cancellation,
ordered guidance, restart reconciliation and result retrieval by Control remain
P4 implementation work. No real model, VM, desktop or multi-machine acceptance
was performed. Remote desktop transport is deferred by user decision.

## Verification

`cargo test --locked -p pot-runtime -j 4`: 170 passed, including four new
foundation tests. Workspace/all-target Clippy with warnings denied passed.
Rust formatting and commit whitespace checks passed. Logs are under
`/tmp/pot39-task-agent-runtime.log` and `/tmp/pot39-task-agent-clippy.log`.
