# Development plan

[Issue #39](https://github.com/jjyr/pot/issues/39) owns the single phase TODO table,
commits and acceptance gates. [Architecture](architecture.md) is the design source.
Changes to model-visible tool interfaces require explicit agreement. New UI flows
must be discussed and confirmed before implementation.

## Delivered

- Optional resources, typed Host/Sandbox environments and multiple assistants.
- Assistant-owned DB AGENTS.md, self-scoped approved edits and project context.
- One Control with outward machine membership, isolated App modes and fixed Web UI.
- Minimal Machines status/environment views and optional Chat file delivery.
- Control-selected global snapshots, pinned remote plugin dispatch and receipts.

## Remaining

- Remote environment transports: outward desktop streaming, actual Sandbox plugin
  qualification and multi-machine App/Web acceptance; remote notification relay.
- P4: fresh Task Agent startup on the execution Runtime, durable launch/result
  records, Control model/budget/approval services, ordered guidance/cancellation,
  disconnection pause and reconciliation. Current loops still run at the owner.
- Final acceptance: real machines and supported providers, recovery without replay,
  data-preserving upgrades, documentation and packaging. Do not infer completion
  from same-host fixtures or deliver undeclared fallback behavior.

Global resources and exact configuration are selected by Control. Execution nodes
run the assigned model/environment/tools or report an error. Task results do not
require downloads unless files are actual deliverables. No live Agent relocation,
three-node placement, peer pairing, per-machine capability grants or resource UI
is planned for this release.
