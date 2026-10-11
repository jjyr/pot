# Development plan

[Issue #39](https://github.com/jjyr/pot/issues/39) owns the single phase TODO table,
commits and acceptance gates. [Architecture](architecture.md) is the design source.
On 2026-10-11 the user accepted the delivered scope as complete. The untested
external checks below remain follow-up verification, not closure gates for #39.
Changes to model-visible tool interfaces require explicit agreement. New UI flows
must be discussed and confirmed before implementation.

## Delivered

- Optional resources, typed Host/Sandbox environments and multiple assistants.
- Assistant-owned DB AGENTS.md, self-scoped approved edits and project context.
- One Control with outward machine membership, isolated App modes and fixed Web UI.
- Minimal Machines status/environment views and optional Chat file delivery.
- Control-selected global snapshots, pinned remote plugin dispatch and receipts.

## Qualification

Fresh destination Task Agents, Control model/budget/approval services, guidance,
cancellation, pinned plugins, result/file reads and remote schedules are delivered.
Actual Linux Podman, physical Linux Control/macOS Host processes, native App/Web,
Control restart, disconnect/crash and process-suspension recovery are qualified.
The real DeepSeek test returned the destination project's marker; Provider secrets
remained on Control. See [production integration](validation/task-agent-production-20261011.md),
[clients](validation/control-cross-machine-ui-20261011.md),
[notifications](validation/control-notifications-20261011.md) and
[final qualification](validation/control-final-qualification-20261011.md).

External push-service/browser OS notification delivery and whole-machine sleep or
power loss have not been exercised. Keep these verification limits explicit in
#39; process suspension and encrypted-payload tests do not establish them.

Global resources and exact configuration are selected by Control. Execution nodes
run the assigned model/environment/tools or report an error. Task results do not
require downloads unless files are actual deliverables. No live Agent relocation,
three-node placement, peer pairing, per-machine capability grants or resource UI
is planned for this release.

Remote desktop transport is deferred by user decision until its connection design
is agreed. Neither Control relay nor direct access is selected. It is outside the
current #39 completion scope; existing local desktop support remains available.
