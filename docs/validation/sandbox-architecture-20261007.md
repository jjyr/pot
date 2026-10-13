# Sandbox architecture extraction acceptance

Date: 2026-10-07. Architecture stage, before adding macOS.

`pot-sandbox` now owns inventory, lifecycle/readiness, leases, owner/turn/epoch scopes, session ownership, private execution receipts and quarantine/recovery. `pot-sandbox-linux` owns Apple Containerization IPC and authenticated guest HTTP. Server registers Providers and injects them into Runtime. Runtime retains product authorization, tasks/schedules and Agent instructions/skills. Production Runtime and Manager have no concrete Provider dependency; Runtime unit fixtures use a dev dependency.

Legacy definitions migrate to the Linux Provider without changing IDs or task assignments. Legacy product receipts import into Manager storage; unknown outcomes cannot replay. Manager epochs persist and advance on reopen or Helper generation change. Source recipes moved to `crates/sandbox-images/linux/default/`; descriptor metadata participates in the build fingerprint.

Validation:

- `mise run check`: passed (workspace Rust, formatting/scripts/i18n and Swift Helper compilation).
- `mise run test`: passed, including 130 Runtime tests and Server hosting/access/lifecycle integration.
- Manager contract suite: 6 passed, including concurrent duplicate dispatch, durable epochs, worker/session boundaries, unknown cleanup quarantine, registration/migration and failed persistence rollback.
- Clippy on all targets of Sandbox, Linux Provider, Runtime and Server with `-D warnings`: passed.
- Linux OCI image rebuilt from the migrated recipe; reference `pot.local/sandbox:026f1eea024aed72883e`.

Limits: no macOS execution yet. Installed template catalog, allocation reservations, cross-execution target artifacts and display arbitration remain design work. Storage remains atomic private JSON rather than the proposed injected SQLite store. This stage does not add Linux-host VM support or mutual isolation between workers sharing one Linux VM.
