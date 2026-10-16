# Component boundaries — 2026-10-11

## Change

Runtime now assembles optional Control and local execution resources and manages
lifecycle. Assistant owns identity, SQLite instructions, Threads and chat-turn
sequencing. Gateway owns Provider transport and metadata; Usage owns accounting;
Secrets owns authenticated encrypted SQLite values. Runner owns destination Task
Agent execution and run journals. Model codecs are a pure shared component.

Local execution services call Control in memory. Remote model requests are bound
to the selected run and resolved through Control's Gateway. No API credential is
sent to Runner. Provider metadata and secrets share Control's `control.sqlite`;
assistants share its configuration handle instead of copying Provider files.
Project AGENTS.md and project Skills remain execution-workspace inputs.

Removed implicit Agent HTTP fallback, plaintext Provider JSON readers, obsolete
startup/resource upgrades and eager startup constructors. Tool schemas and user
interaction flows were not changed.

## Verification

- Workspace Clippy, all targets, locked dependencies, warnings denied.
- Component dependency assertions: four passing checks.
- Deterministic component/backend suite: 370 passing tests, three explicit
  environment-dependent skips (libtailscale and two rootless Podman cases).
- Follow-up Runner, Control and Server regression suite after final error-handling
  cleanup: 224 passing tests, three environment-dependent skips. Agent RPC
  cleanup: 72 passing tests.
- AES-GCM tests verify reopen, wrong-key rejection, authenticated reference
  tampering, immutable references and absence of plaintext in the database.
- Assistant tests verify identity/rules persistence, stale edits and shared Control
  credentials; remote Host tests preserve project-rule discovery, approvals,
  disconnection recovery and fixed plugin execution.

Tests used an isolated temporary master key, not the user's Keychain or model
credentials. `mise run test` now provisions and removes its own temporary key and
runs component boundary assertions first.

## Scope limits

No fresh browser, VM, real-model, physical sleep or external notification receipt
acceptance was performed for this refactor. Previous dated reports are historical
evidence, not a new qualification of this change.

General task-secret distribution and master-key rotation remain future work.
The current invocation boundary is documented in [Runtime](https://github.com/jjyr/pot/blob/6d57b31/crates/runtime/README.md).
This dated report covers the component extraction, not subsequent execution-manager changes.
