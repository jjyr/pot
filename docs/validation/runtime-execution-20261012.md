# Runtime Agent execution — 2026-10-12

## Change

Every production assistant/local Task invocation and destination Runner invocation
uses one execution manager per Runtime Host. The new `pot-runtime-execution` library
owns the actual Agent call, event gate, local admission queue, shared concurrency,
cooperative cancellation, cleanup settlement and metadata logging. It is a library,
not an additional deployed service. Control retains placement and configuration
rules; Assistant and Task adapters retain business persistence and ordering.

Removed Control's stored Agent executor and both adapters' direct Agent calls/event
gates. Runtime no longer constructs an Agent to pass into Control. Resource
activation shares the existing manager. No compatibility invocation path or tool/UI
change was added. Standalone engine evaluation remains an isolated engine fixture.

## Verification

- Runtime Execution: nine tests cover assistant/Task capacity sharing, queued and
  running cancellation, abort cleanup, shutdown, duplicate admission, queue bounds,
  dropped futures, unresolved settlement, failed cleanup, event fencing, strict
  limits and lazy/private/rotated logs without prompt payloads.
- Control integration asserts that assistant and local Host worker replies appear
  in Runtime execution snapshots. Existing model-protocol, approval, checkpoint,
  instruction and tool-receipt tests exercise the shared entry point.
- Runtime integration asserts that a destination Task run appears in the same
  manager exposed by `Backend::executions()`. Neutral startup remains idle.
- The architecture check forbids direct Agent construction in product adapters
  and protects the execution kernel from Control, Server, Provider credentials
  and network-client dependencies.

The backend suite passed 437 tests, with three existing gated cases ignored.
All nine execution-manager tests also passed after the final cancellation cleanup
adjustment. Workspace Clippy, architecture, formatting and license checks passed.

Commands: `mise run test`, `cargo test -p pot-runtime-execution`, workspace/all-target
Clippy with warnings denied, workspace formatting, `git diff --check`, and
`mise run licenses`. All Cargo commands use the mise toolchain. Backend tests use
an isolated temporary master key and deterministic model services.

## Scope

No new UI/browser, physical-machine, paid-model or VM acceptance was required or
performed. The two Linux Podman remote integration cases and the libtailscale FFI build-dependent
case remain gated.
Runtime logs are bounded diagnostics; they do not replace durable business
receipts. Interrupted executions retain local capacity until Runtime restart.
See [Runtime](../../crates/runtime/README.md) for the current contract and
[deployment](../deployment.md#runtime-agent-execution) for limits and log paths.
