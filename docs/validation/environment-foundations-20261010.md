# Environment foundations, 2026-10-10

This records partial P3 work for Issue #39, not completion of distributed
execution. The user explicitly approved unified Host/Sandbox Environment UI and
typed tool references before these changes.

## Implemented and tested

- `EnvironmentRef` distinguishes Runtime identity, Host and Sandbox targets.
  A Sandbox named `host` cannot alias Host; a same-named Sandbox on another
  Runtime cannot satisfy a worker's binding.
- Host executor/process/plugin ownership moved out of permission storage.
  Generic execution and cleanup live under Environment; Sandbox retains
  contained-instance management and guest operations.
- Tasks persist typed bindings. Schema-1 Assignments upgrade to schema 2 while
  preserving owner, Agent Runtime, target, context, queues and receipts. Conflicts
  fail instead of rebinding. Existing Sandbox workspace paths are unchanged.
- Local Host tasks have their own workspace, instruction and skill discovery,
  and retain normal owner permission checks. Worker Host/Sandbox switching is
  rejected before execution. Shared cooperative capacity uses the full target.
- `list_environments` replaces `list_sandboxes`; task and execution schemas use
  the approved typed shape while retaining local selection entry points.
  `manage_sandbox` remains Sandbox-only. Remote requests currently fail explicitly.
- The `host` preset registers no Sandbox Provider and initializes no Agent,
  model, conversation or task store. Native operator endpoints execute Host
  operations, persist acceptance before effects and reuse completed receipts
  across retry/restart. Conflicting or uncertain operations never replay effects.
- Host receipt queries expose acceptance/result status. Native cancellation
  persists intent before cleanup, blocks new effects and cleans only its run.
  Tests exercise real local shell sessions, cross-run session rejection and
  sibling-session survival.

## Validation

The automated suites cover Core, Agent, Runtime and Server, including Server
access, TLS, CLI, lifecycle and multiple-client integration. Workspace Rust and
Swift checks pass. Tests use disposable data roots and Agent homes.

## Remaining P3 scope

Peer identity verification, pairing, directional grants/revocation, authenticated
remote Environment transport, remote leases/reconciliation, authorized artifact
and desktop forwarding, unified client Environment/Runtimes UI and browser
acceptance are not delivered by this checkpoint. Native operator authority does
not constitute peer authority. P3 remains in progress and has no phase commit.
