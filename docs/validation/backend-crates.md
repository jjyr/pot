# Backend crates / standalone Server validation

Date: 2026-10-05. Issue [#5](https://github.com/jjyr/pot/issues/5); see [Backend crate split](../backend-crates.md). Design committed before implementation; validation used an independent worktree, temporary data directories, and pinned mise tools.

## Results

| Check | Result |
|---|---|
| Workspace check | All 8 crates pass; Server backend dependencies exclude App/GPUI/Swift/Helper |
| lint | Rust/Swift/Python/Shell formatting/script checks pass |
| Regular backend regression | Core/Storage/Driver/Gateway/Runtime/Server/Protocol pass; checksum, old database compatibility, requests/allowances, tasks/schedules, cancellation |
| App | 11 tests pass; caches/events/pagination/guidance acknowledgments/send-disabled behavior remain compatible |
| Real Pi + local mock provider | 8 pass: tools/native continuation, cancellation, allowance recovery/compaction, multitask, automatic orchestration, schedules, provider migration, native codecs |
| Standalone Server | 3 pass: bearer, mode-0600 endpoint, persistent settings/restart, old-token rejection, SSE closure; bind conflict does not initialize storage; active main/worker processes confirmed stopped on shutdown |
| Driver feedback boundaries | Foreign requests, old epochs, ended turns rejected without contaminating current bindings |
| Native Codex App Server | Standalone Server + official CLI + local mock model passes continuation/migration/MCP/read-only summary/independent workers/schedules/real Runtime clock/cancellation/background cleanup; 35 mock requests, 0 paid calls |
| AGY | Mock transport passes native identity/restart/post-cancel continuation/command recovery/redaction/usage/workers/schedules/summary; 0 model calls |
| Claude | Independent Driver + fixture child passes identity/usage/message events/recovery/cancellation without Runtime/database. Original CLI attempt rejected at capability check for missing required controls, with no model requests; not counted as original CLI acceptance |
| Linux ARM64 | `pot-server` `aarch64-unknown-linux-musl` zigbuild passes |

[Development guide](../development.md) and [Runtime contract](../../runtime/README.md) maintain commands. Server integration/Driver fixtures run in `mise run test`. Machine-specific environment notes belong in `~/Workspace/Machines`.

## Scope and limitations

Preserve SQLite layouts, DELETE + synchronous=EXTRA, old metadata, native sessions, request allowances. Control API v1/MCP/PTY paths/endpoint contracts remain compatible. Tool schema is shared through Core API, avoiding cross-crate source includes.

Runtime retains Axum handler arguments/errors; Server composes routes/lifecycle without making every handler transport-neutral. Gateway networking follows Runtime durable reservations/turn-lane validation. Driver feedback defines compile-time responsibilities, not a new guest security domain.

No full OCI/App bundle rebuild, existing App/VM data launch/modification, guest cold install, GUI/real account matrix, or remote deployment acceptance. No new cross-machine discovery/TLS/identity/artifact transfer/strong desktop isolation.
