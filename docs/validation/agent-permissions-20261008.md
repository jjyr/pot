# pot-agent permissions validation

Date: 2026-10-08. Scope: Agent-owned permissions for issue #29. Codex integration is deferred. See [the current contract](../permissions.md).

## Automated checks

- `mise run test`: backend regression suite passed; the existing paid real-model test remains ignored.
- `mise run check`: source formatting/script checks, Rust workspace compilation and macOS Helper release compilation passed.
- `mise exec -- cargo clippy --locked -p pot-agent -p pot-runtime -p pot-sandbox -p pot-server -p pot-ui --all-targets -j 4 -- -D warnings`: passed.
- Native App compilation and `pot-mobile` compilation for `wasm32-unknown-unknown` with the pinned Web nightly passed.
- `mise run test:i18n`: passed, covering 765 translation keys.
- `scripts/tests/skill-mounts.py`: 10 tests passed.
- `git diff --check`: passed.

Permission tests exercise denied operations without effects, one-time consumption,
duplicate-call receipts, persistent grants scoped to Agent owner and binding epoch,
read/write and command scope distinctions, revocation, restart and stale-turn
rejection. Host file tests cover symlink redirection and hard-link writes. Shell
tests cover explicitly approved effects and timeout. Sandbox tests cover mapping
validation, save/removal, busy/running rejection and preservation on resource updates.

A native Pot model-loop integration test uses a local mock model endpoint and
owner decisions. It verifies deny/once/persistent behavior, that the Agent does
not sample while approval is pending, and that only explicitly persistent grants
survive reopening the permission store. It does not use a paid model service.

## Limits and unrelated failure

Native/Web approval cards have been compiled, but their interactive rendering has
not been qualified in a running client. Live Podman or Apple Containerization Host
mapping startup has not been qualified. The prepared macOS guest provider does
not support Host mappings.

`scripts/tests/macos-provider.py` was attempted twice and failed in the existing
TLS/RFB tunnel test: its fake RFB service encounters a connection reset during a
readiness probe, then the tunnel read times out. That test, `scripts/macos-enrollment`
and `sandbox-images/macos/xcode/executor.py` were unchanged. This run does not
establish prepared macOS desktop qualification.

## Current-state JSON refinement — 2026-10-08

Permanent grants now use the agreed user-editable
`~/.pot/agent/permissions.json` format (`version`, `grants`, `id`, `agent`,
`permission`). They bind to Agent identity and survive rebinding. Pending and
one-time decisions exist only in memory; denial, cancellation and consumption
delete them. There is no approval history in the permission store. Duplicate
Agent/scopes replace their current entry, and revocation deletes the entry.

Tests cover JSON roundtripping/validation, Agent identity across rebinding,
one-time consumption, 1,100 settled requests without accumulation or file writes,
restart clearing pending decisions, revocation, atomic/private file publication,
external/stale writer conflict detection and migration of only permanent grants
from the previous SQLite database. A missing JSON file does not create an empty
file at startup. `POT_AGENT_HOME` isolates production-Server tests from user files.

The full backend suite passed after this refinement, including the native Pot
owner-decision integration and Server TLS/multiple-client tests. Related Clippy,
source lint, Native App/Web compilation and translation checks passed (760
translation keys). After adding dropped-request cleanup, permission tests and
Clippy were rerun successfully. The initial Server TLS startup timeout exposed
attempted creation of an empty permission file in the user home; lazy file
creation and an isolated test Agent home fixed it. The separate prepared macOS
RFB limitation above remains.
