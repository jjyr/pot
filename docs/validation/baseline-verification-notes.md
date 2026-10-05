---
{ tags: [ POC ] }
---
# Final validation (2026-10-03)

> Historical acceptance record: versions, allowances, and UI reflect each section's date. See [Development plan](../development-plan.md) for current capabilities and [Documentation index](../README.md) for operating contracts.

- Runtime: 22 tests passed; adversarial tests: 4 passed after 3 fixes; OAuth loopback fixture: 3 passed (29 total).
- Reverification: new VM boot/shared-file persistence/UID denial/network passed; Pi live tool loop/restart/active abort/steer passed with local model fixture.
- Codex handshake retried: same state-backfill timeout. Four other engine transports/lifecycles passed again.
- Persistent JSON evidence parsed successfully; Python POC source parsed successfully.
- Native fixture and actual Linux VM screenshots inspected separately.
- Own OpenCode HTTP and RFB WebSocket listeners stopped. Temporary native and VM processes terminated.
- Canonical machine notes updated under ~/Workspace/Machines/machines/local-mac-m4/.
- Machine inventory validation was attempted and failed due to existing missing machine/service example metadata files. This task changed Markdown notes only; unrelated inventory metadata was not modified.
- No complete distribution package, live model turn, real third-party OAuth, Dot logged-in UI or comprehensive desktop isolation claim is made.

- Follow-up: fresh Linux VM Codex App Server 5/5 basic protocol checks passed; host backfill retained as historical diagnostics, no longer blocks isolated integration. No live model turn.
