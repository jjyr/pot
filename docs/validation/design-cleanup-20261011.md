# Design cleanup — 2026-10-11

## Scope

Consolidated the approved single-Control architecture, assistant/project context,
resource-driven deployment and minimal UI contracts. Removed duplicate design pages,
superseded pairing/preferences/engine reports and unused future-design research.
The documentation index links current contracts; #39 retains one phase TODO table.

No active pairing protocol exists. Machines enroll into one Control and poll
outward. Production filenames and lease fields now use Control/execution terms.
One-time upgrades preserve signing keys, workspaces, receipts and current leases;
the old inbound peer RPC is retained only in a rejection test. Identity authentication,
owner model approvals, liveness leases and reconciliation remain enforced.

Removed Composition presets/enum/configuration, destination tool filtering,
prompt string correction and browser-to-cua_repl translation. Resources determine
assembled services and advertised Runtime capabilities. Existing product roots
adopt their current resource definitions once; fresh roots remain idle.

Worker defaults now use one routing policy. Permission and Sandbox receipt conversion
run before normal service access. Task record upgrades precede ordinary binding
validation; ordinary restore has no legacy-target fallback. Current data is retained;
obsolete enrollment/grants and assistant file formats are not imported.

Main reads project AGENTS.md/Skills at its actual Host cwd and advertises real readable
Host global Skill paths. Workers retain their fixed project context. Assistant DB
rules remain separate, approved and absent from worker instructions. Bundled skill
management guidance requires global publication at Control, using injected helper
paths; connected tasks can edit only applicable project resources.

## Verification

- Core 17, Agent 71, Runtime 166, Sandbox 18 and Server 27 unit tests passed.
  One existing Server test is ignored.
- Server access 5, TLS 1, CLI 3, lifecycle 10, multi-client 5 and remote environment
  4 tests passed. Actual rootless Podman execution remains opt-in/ignored.
- App 22 and UI 13 tests passed; workspace/all-target Clippy passed.
- Focused final upgrade checks passed (Runtime 3 and Server lifecycle 1).
- Rust formatting, diff whitespace and documentation file/heading links passed.

The old remote-plugin integration assertion searched the entire model request for
“42”; random IDs/digests could satisfy it despite an expired session. A clean baseline
with an exact failure check reproduced the expiry. Corrected integration checks
parsed tool-call results: same accepted run continues 41 to 42; a later run rejects
the previous session explicitly. Runtime behavior was not changed to manufacture
cross-run continuity. The previous plugin report has been corrected.

Logs: `/private/tmp/pot39-cleanup-final-tests.log`,
`/private/tmp/pot39-cleanup-app-ui.log`,
`/private/tmp/pot39-cleanup-final-clippy.log`,
`/private/tmp/pot39-cleanup-upgrade-tests.log`,
`/private/tmp/pot39-cleanup-upgrade-final.log`.

No new UI interaction, paid model call, deployment or actual remote VM qualification
was performed. Task Agent loops still run at the owner; fresh remote launch, desktop
transport and broader machine/provider qualification remain unfinished in #39.
