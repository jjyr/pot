# Remote schedule integration — 2026-10-11

User approved the optional `manage_schedule.spec.environment_ref` and fixed-target
behavior before implementation. Creation shares `create_task` target resolution;
updates without a target retain the old reference, even while offline. Local string
inputs remain; ambiguous dual inputs fail. Restart upgrades an existing schedule to
its original local Sandbox exactly once. Defaults cannot retarget triggers. Changing
its goal or environment resets the comparison baseline.

Control reads a completed scheduled Task's structured result at its immutable
execution environment via the existing file transport, outside task hub locks.
The result is limited to 16 KiB, validated before comparison, and needs no artifact.
Missing, invalid or oversized files fail. Sandbox deletion references are local to
the exact Runtime; a remote Sandbox with the same name does not block local deletion.
App and Web show the saved machine/environment; no selection workflow was added.

Tests cover the pinned remote binding, offline restore, update/default changes,
request deduplication, ambiguous inputs, bounded results and versioned upgrades.
Runtime 175 tests (before the additional upgrade case), Core 19, Protocol 2, UI 13
and Server 30 (one existing ignored) passed; workspace/all-target Clippy and the
pinned WASM build passed.

The independent-process crash harness runs Linux Control and macOS Host across
signed tailnet HTTPS using fresh roots. Its extended run passed fixed remote Host
schedule creation, duplicate manual trigger, one Task, destination file read, stored
fingerprint `remote-stable`, verified summary and first-observation change. Twelve
Worker model fixture samples covered all crash/guidance/cancellation/plugin/schedule
cases; seven destination runs ended as five complete, one cancelled, one interrupted.
Exactly two non-schedule effect files contained one X each. Fixture processes stopped.
No paid provider, native App/Web interaction or actual OS sleep is claimed here.
