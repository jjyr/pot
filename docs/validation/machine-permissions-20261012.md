# Machine capability permissions — 2026-10-12

## Delivered contract

Control authorizes an assistant's Host or Sandbox capability on a machine, with
work-scoped decisions, durable grants and owner-configured ask/allow/deny rules.
Task work scopes propagate to derived Tasks; independent work receives a new
scope. Remote Tasks wait for authorization before starting their Agent. Enrollment
changes invalidate prior machine grants and preallow rules.

Sandbox creation and management enforce ownership and configured sizing/inventory
limits. Host mappings are owner configuration. Execution target references remain
internal fixed execution targets. Native and Web clients share capability cards
and machine policy controls. Sharing and mounts are configured through owner APIs;
this change does not add dedicated sharing or mounts editors.

Legacy command/path grants, Guest permission files, plugin approval-display
metadata and the model-visible mount-changing tool were removed. Version 2 policy
lives in each Control data directory. Old permission/Task state is not migrated;
Guest brokers must be rebuilt or reprovisioned. See [permissions](../permissions.md).

## Verification

- `mise run test`: 431 passed, 3 ignored; architecture prerequisite: 5 passed.
- `mise run clippy`: all workspace targets passed with warnings denied.
- `mise run test:ui`: 12 passed.
- `mise run test:i18n`: 792 translation keys and native/Web localization checks passed.
- Guest argument preparation: 2 passed; executor lifecycle: 5 passed; skill mounts: 11 passed.
- Rust formatting, modified Python Ruff checks and `git diff --check` passed.
- `mise run lint` passed again before submission.
- Both opt-in remote rootless Podman tests passed on Linux (2 passed, 0 ignored,
  64.37 seconds), using the updated Guest scripts and a freshly cross-built musl
  executor in a derivative test image. They cover Task launch approval before
  sampling, destination project rules, persistent files, lifecycle management and
  pinned plugin session isolation. The fixture now assigns its actual `remote-test`
  sandbox identity rather than an unrelated `default` sandbox.

The Podman run used an isolated container store and deterministic model fixtures;
both Runtime nodes ran on the same Linux machine. It is not a physical
cross-machine or paid-model qualification. Test containers and fixture runtime
directories were empty after completion. Machine-specific build/run locations
are recorded in the canonical machine inventory.

Focused coverage includes shared approval waiters, cancellation, work inheritance,
denial, revocation, durable restart, re-enrollment, ownership, actual provider
creation sizing, machine inventory quotas and instruction review separation.
Five remote Host integration tests passed, including offline recovery, lease
recovery and plugin execution. The final full suite includes the updated shutdown
test, which explicitly grants Host access before expecting a Task model request.

An earlier full run saw a connection-refused error in the fake macOS Helper startup
test. Its isolated retry and the final full suite passed without changing that
test or Helper startup implementation.

## Limits

The local default suite skips two rootless Podman tests, which were subsequently
run successfully on Linux. The optional libtailscale FFI test remains skipped.
No real macOS VM end-to-end qualification was performed. Podman does not enforce the disk sizing ceiling as
a storage quota. Remote sandbox creation remains unsupported by the existing
transport. OS privacy controls and image-resource download confirmations remain
separate from machine capability authorization.
