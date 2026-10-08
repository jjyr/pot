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

## Unified tools and environment-local permissions — 2026-10-08

pot-agent now registers one set of execution tools (`exec_command`, `write_stdin`,
`apply_patch`, `read_file`, `write_file`, `list_directory`). Main defaults to Host;
workers default to their saved Sandbox, can explicitly request Host, and cannot
switch Sandboxes. Owner cards identify the environment and retain the same three
approval actions. Host and guest execution share the Rust tool implementation.
Codex keeps its prior Sandbox loadout and is not integrated with this policy.

Host grants remain in Host `~/.pot/agent/permissions.json`. Sandbox grants use the
same format at execution HOME's guest-local path. Trusted guest preparation
resolves paths and loads config before approval; permanent decisions/revocations
publish with compare-and-swap through the root control service. Linux protects
both backing and HOME views with readonly namespace mounts. Prepared macOS
protects backing and view paths with an inherited file-write policy. Pending and
one-time requests remain memory-only. Legacy Host tool grants are renamed while
retaining their original command shell/login/timeout settings.

Checks performed:

- Full `mise run test` passed with an isolated `POT_AGENT_HOME`; the paid-model test remains ignored.
- Runtime's authenticated Helper/guest double verifies guest-only path normalization, Host/two-Sandbox grant isolation, guest permanent-grant publication/reload/revocation, forbidden worker Sandbox retargeting and explicit worker Host selection.
- Agent tests verify one registration per shared tool, no `host_*` registrations, denial before effects and legacy command migration.
- `mise run test:permissions`: 6 passed, covering guest canonical paths, defaults, compare-and-swap/restart/revocation, symlink rejection, readonly backing/HOME views and macOS policy construction.
- `mise run test:skills`: 11 passed, including readonly permission mount ordering.
- `mise run test:executor`: 5 passed.
- Backend Clippy with warnings denied, source lint, Native App and pinned Web/WASM compilation passed.
- Translation checks passed (760 keys); `git diff --check` passed.

This is automated code/protocol validation, not live VM or interactive UI
qualification. Published images must be rebuilt with the new broker/module and
Rust runner; prepared macOS guests must be reprovisioned. Missing guest permission
routes fail explicitly without Host fallback. Linux mount enforcement and macOS
policy inheritance still need live environment qualification. Guest grant lists
are loaded lazily: after Runtime restart they appear in the owner UI once that
Sandbox is prepared. The earlier prepared macOS desktop/RFB limitation remains.


## Environment default policy — 2026-10-08

Permission files now accept `policy.default` (`ask` or `allow`). Host defaults to
ask and guest Sandbox defaults to allow, including existing version-1 files without
a policy field. Explicit policy overrides apply in that environment; saving or
revoking grants preserves the loaded policy. Legacy embedder grant-array feedback
continues to ask by default. Summary/management loadouts, fixed worker Sandbox
assignment, session ownership and guest config protection remain independent of
policy. A permissive policy does not grant role/management authority.

Full isolated backend tests passed after this refinement. Agent/Runtime tests
cover allow without grants/approval, ask with denial, Summary and mapping role
limits, Host/guest defaults, explicit overrides, preservation during grant changes
and invalid policy rejection. Guest permission tests passed (7), as did skill
mount tests (11) and source lint. Existing real-VM qualification limits remain.

## Built-in management and session continuation — 2026-10-08

Task/schedule/preferences and Sandbox management remain role-scoped built-ins and
now require no execution permission approval. Host mappings still require an exact
owner-approved grant or an explicit decision, even under Host `allow` policy.
Guest permission configuration remains protected from ordinary tool writes;
permission administration still requires the owner channel.

`write_stdin` now continues the original authorized session without permission
prompts for output, input, resize or stopping. Preparing a guest continuation does
not boot a guest or load guest permission config. Runtime tests cover missing
handles, valid explicit original-environment input and rejection of another
Sandbox target. The Manager accepts an explicit matching original Sandbox but
still rejects switching and checks owner/Agent epoch. Existing session regression
covers turn retention, expiration and cancellation.

Full isolated backend tests passed for the approval changes; related Agent,
Runtime and Manager regression was repeated after refining explicit session-target
validation. Source lint and backend Clippy passed. No search backend was added:
a registered `web_search` extension is outside command/file approval policy, and
future built-in public search is intended to remain approval-free. Shell network
operations retain their ordinary environment policy.

## Tool composition and naming refinement, 2026-10-08

With explicit user agreement, removed `read_file`, `write_file` and
`list_directory`, removed the `pot_` prefix from the twelve product tools, and
added pot-agent `exec`/`wait`. Existing product tool responsibilities/arguments stay
separate; no task messaging/interruption/waiting tools were added. The project
AGENTS.md now requires explicit agreement before changing tool interfaces.

Composition uses a bounded QuickJS isolate, exposes only the role/profile's
registered tools, and routes child operations through the shared preparation,
approval and execution dispatcher. Only `text()` publications enter model history.
Runs/handles are turn-scoped and are not resumed/replayed after interruption.
See [the composition contract](../tool-composition.md) for limits and migration.

Verification: full `mise run test` backend suite passed with an isolated temporary
`POT_AGENT_HOME`. Targeted Agent/Runtime tests passed after the final dispatch and
prompt refinements. Agent tests cover parallel composition, incremental waits,
private intermediate results, owner denial/approval, disabled/removed tool rejection,
unique child identities, parent receipt reuse, product MCP dispatch across Chat,
Responses and Messages, output/call limits and synchronous-loop interruption.
Guest permission (7), skill mount (11) and executor lifecycle (5) tests passed.
Backend Clippy with warnings denied, source lint and full workspace compilation
including the Native App passed. These fixtures do not establish real-provider
model tool-selection quality, measured token savings or live VM/UI acceptance.

The Rust dependency license check (`mise run licenses`) also passed and generated
notices including rquickjs, rquickjs-core and rquickjs-sys 0.11.0.

## Main task coordination and interruption, 2026-10-08

With explicit user agreement, added Main-only `send_message` and `wait_task`, and
replaced model-visible `cancel_task` with `interrupt`. Owner decisions confirmed
pending guidance retention, explicit `guide_task` resume, and no Worker-to-Worker
messaging in this iteration. Native/Web/CLI task stopping now uses interruption;
the old HTTP cancellation endpoint remains a legacy compatibility route.

Runtime fixtures passed: 158 tests, one paid-model test ignored. Coverage includes
role visibility and invocation rejection, running/queued/allowance-wait interruption,
FIFO guidance preservation, restart, no replay of interrupted executing input,
unconfirmed shutdown retaining capacity, durable reference-message receipts and
boundary acknowledgement, cursor waits and caller interruption. The full isolated
backend suite passed, including the authenticated multi-client interruption route.
Workspace compilation including App, backend Clippy with warnings denied, source
lint, Web resource generation and localization checks passed.

The first full run exposed a shutdown fixture whose expanded Main schemas plus
8,192-token output reservation exceeded the conservative 32,768-byte context
estimate. The shutdown-only fixture now explicitly reserves 1,024 output tokens.
A multi-client fixture also caught the missing Web route allowlist entry for
`/interrupt`; that entry is now present. Small profiles may still need adjusted
context/output limits; the production context guard was not relaxed.

Archive remains phase-derived: complete/legacy cancelled are archived,
failed/interrupted need attention, queued/running/allowance-waiting remain active.
Aged unfinished Tasks are not automatically archived. Inactivity archival and an
independent archive flag remain discussion proposals. Live model collaboration,
VM and native/browser interaction acceptance have not been performed for this update.

## Explicit task abandonment restored, 2026-10-08

After reviewing interruption versus abandonment, the owner approved restoring
Main-only `cancel_task` alongside `interrupt`. Cancellation clears pending guidance,
stops active execution and marks cancelled after confirmed cleanup. Interrupted
Tasks with empty queues can now be cancelled as well. History/files survive;
completed results retain their completed status. CLI cancel and interrupt route to
their respective APIs; App/Web stop continues to interrupt. Guidance is rejected
while either shutdown operation is being settled. Main now defaults to 21 tools.
Independent archive flags and inactivity rules remain deferred.

Full isolated backend tests passed. Runtime: 160 passed, one paid-model test
ignored. Added coverage verifies cancellation of interrupted Tasks both with and
without guidance, repeated cancellation, archived grouping, wait readiness,
restart persistence, explicit reopening without replay, running cancellation and
rejection of premature guidance. Existing role-filter fixtures now cover both
stop tools. Workspace compilation including App, backend Clippy with warnings
denied, source lint and diff whitespace checks passed. Live model/UI qualification
was not repeated.
