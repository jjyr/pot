# Executor shells, sessions and snapshots acceptance — 2026-10-07

## Delivered

- Common Pot/MCP schemas and runtime validation for shell/login/tty, stdin,
  interrupt, termination and bounded PTY dimensions. Unix nonblocking PTY I/O
  uses the existing libc dependency; guest startup ensures devpts availability.
- Agent owner/epoch/Sandbox/generation-scoped processes survive normal turns.
  Idle runners retain initialization caches without execution leases; active
  processes retain leases. Guest inactivity timers and an independent Runtime
  maintenance task reclaim/reconcile them. Maintenance cannot block task dispatch.
- Cancellation, failed turns and shutdown drain owned execution scopes. Explicit
  cancellation of a completed task closes its retained resources while preserving
  its result. Idle guest inspection drains orphan descendants before releasing
  leases. Invalid/unknown inspection preserves quarantine rather than declaring
  cleanup successful. Restart does not resume handles or replay commands.
- Bounded in-memory Bash/Zsh login snapshots capture exported environment,
  functions, aliases, options and umask. Standard startup-file changes invalidate
  the cache; capture/restore failure uses fresh login startup. User command parsing
  follows restoration so Zsh aliases work. Independent commands cannot mutate the
  cached baseline. Profiles default to Bash with login/snapshots enabled.

## Verification run

- `mise run test`: **263 passed**, one existing live-provider test ignored. This
  includes eight real JSON-lines runner integration tests, five new Runtime
  routing/lifecycle tests and two new Core schema/checksum regressions.
- `mise run test:executor`: **5 passed**. Production broker functions run against
  isolated SQLite and cgroup/process doubles, without root privileges or a VM.
- `mise run clippy:backend`: passed, all portable backend/workspace targets with
  warnings treated as errors (App excluded).
- `mise run lint`: passed Rust formatting, script and Swift formatting checks.
- `cargo check --locked -p pot-tool-executor --target aarch64-unknown-linux-musl`:
  passed.
- `cargo zigbuild --locked -p pot-tool-executor --target aarch64-unknown-linux-musl
  --release`: passed, producing the actual guest binary. Zig emitted one
  linker warning about deprecated optimization setting `1`; no compilation or
  linking errors occurred.
- `git diff --check`: passed.

Real runner coverage includes separate cwd/export state, explicit workdir, Bash
snapshot reuse and profile invalidation, oversized-capture fallback, Zsh aliases/
functions/umask, PTY input/resize/interrupt, pipe interrupt/termination, explicit
command timeout, session inactivity expiry, and retaining completed output for
later reads. Tests use private temporary homes and no model/provider credentials.

Runtime doubles exercise production injected ToolExecutor/HTTP routing and scoped
credentials: cross-turn retention after changing the main default, fixed worker
assignment, parking/reactivation, epoch/generation invalidation, idle expiry,
restart quarantine, malformed inventory failure and completed-task cancellation.
Existing owner/summary restrictions, receipt deduplication and cancellation tests
also passed. Broker doubles cover receipt conflicts/replay, cancelled scopes,
failed cleanup, orphan drainage and avoiding expiry of busy runners.

Local logs: `/tmp/pot-session-full-tests.log`, `/tmp/pot-session-broker.log`,
`/tmp/pot-session-clippy.log`, `/tmp/pot-session-lint.log`,
`/tmp/pot-session-linux.log`, `/tmp/pot-session-linux-build.log`.

## Configuration simplification follow-up

At the user's request, the old shell-settings compatibility behavior was removed
after the verification above. Missing fields now use the current defaults;
serialization includes login, snapshot and idle-timeout settings even when they
are disabled or set to their defaults. Previous profile checksums are not
preserved. The compatibility regression was replaced with explicit serialization,
round-trip/checksum and current-default coverage.

Follow-up verification: `mise run test` passed 264 tests with one live-provider
test ignored; `cargo fmt --all -- --check` and `git diff --check` passed. Test log:
`/tmp/pot-shell-default-tests.log`.

## Remaining acceptance and limits

Snapshots use noninteractive login startup and do not fully track arbitrary
transitive sourced files or external state. `sh` falls back to normal startup.
Zsh tests ran on the available local `/bin/zsh`; the bundled guest includes Bash,
not a newly installed Zsh. PTY output contains raw terminal control bytes rather
than a full terminal screen renderer. Background handles remain memory-only and
bounded by policy; they are intentionally invalid after process/backend restart.
