# Local Runtime composition acceptance — 2026-10-10

## Scope and method

P1 of [Issue #39](https://github.com/jjyr/pot/issues/39), tested on macOS with
repository-pinned mise tools. Tests use isolated product roots, local HTTP/mock
models and a fake environment Provider. No paid model requests or real guest
startup were used.

## Verified behavior

- Bare Runtime initializes identity/instance lock only in the Runtime harness;
  Server additionally publishes its private integration endpoint. No workspace,
  Conversation/usage database, Agent/task loop or Environment inventory starts.
  Native capability discovery requires owner authority. Restart preserves
  Runtime UUID and rotates boot/bearer identity.
- Environment-only Runtime executes without AI settings or assistant stores.
  Input and call identity validation precede preparation. Duplicate completed
  operation receipts return saved output without dispatching another effect;
  conflicting call IDs fail. Durable cancellation rejects subsequent calls for
  that run, while unrelated runs continue. Host mount grants are rejected without
  the assistant approval service.
- Existing product roots retain assistant defaults. Explicit versioned composition
  configuration overrides that default; future schema versions are rejected
  while existing database bytes remain intact.
- Legacy task records receive schema-1 local Assignments without moving task IDs,
  context or workspace. Repeated upgrades are idempotent. Wrong Runtime bindings
  fail and leave the original in-memory task owner available.
- Assistant regressions cover chat/task/guidance/context, configuration changes,
  cancellation/restart, permission isolation, independent clients, Web access/TLS,
  SSE shutdown and in-process App dispatch. Server lifecycle adds bare hosting.

## Commands and results

- `mise run test`: 340 passed, 2 existing ignored tests; zero failures.
- `cargo test --locked -p pot-server --test lifecycle -j 4` through mise: 6 passed,
  including the new bare Server case and embedded assistant dispatch.
- `mise run check`: formatting/scripts, Rust workspace compilation and Swift
  Helper compilation passed, including final incremental checks after the last
  input-validation change.

- `mise run test:app`: 17 Rust client/navigation tests and native Swift client
  safety checks passed.

## Limits and remaining phases

P1 provides three explicit local assemblies. The owner Service facade remains in
local tool/control integration; Task constructs it from separated service state
and invokes a scoped local Runner. Remote acceptance, event replay and journals
are P4 work. Arbitrary remote dependency graphs are not qualified by local tests.
Preset changes require restart and preserve inactive service data.

Multiple conversational owners, peer grants, remote Environment/Agent execution,
three-node placement and artifact transfer remain P2–P6. Current model-visible
interfaces and App interaction are retained. Interactive App/Web acceptance,
real model inference, physical Provider behavior and Linux deployment were not
requalified for this phase; historical VM reports do not establish new coverage.
