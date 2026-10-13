# Execution target terminology and binding validation

Date: 2026-10-13.

## Scope

`ExecutionTarget` is a value containing a Runtime ID and a typed Host/Sandbox
location. It has no independent identity, resource creation API or lifecycle.
Sandbox lifecycle stays with the Sandbox Manager. The UI names machines, Host
execution and sandboxes directly; settings no longer include a separate work
location tab.

The discovery tool is `list_execution_targets`. Execution tools, Task creation
and schedule specifications use `execution_target`; Task and schedule selectors
carry the full typed reference. The native client, browser client, API inventory,
Task filters, permission checks, remote execution and file reads use the same
binding. Tool responsibilities and fixed-target enforcement are preserved.

Only the current field names and Assignment schema are accepted. There are no
field aliases, stored-record migrations, historical launch checksum fallbacks or
plugin context adapters. Plugin manifests use `host_or_sandbox` or `sandbox`
execution support. Sandbox management uses the `sandbox-management` directory.

## Regression coverage

- Host and same-named Sandbox references remain distinct across machines.
- Task creation and schedule values roundtrip their typed destinations; obsolete
  selector fields and unsupported Assignment schemas are rejected.
- A saved remote schedule restores while its machine is offline without following
  the local Sandbox default.
- The launch journal rejects obsolete fields and mismatched checksums.
- Native MCP schemas expose the current discovery name and execution selector.
- Current browser plugins support Host execution; obsolete manifest capabilities
  and Sandbox-only packages on Host are rejected.

## Checks

| Command | Result |
| --- | --- |
| `mise run test` | 435 passed, 3 infrastructure-dependent tests ignored; architecture check passed |
| `mise run test:app` | 22 passed; Swift client safety checks passed |
| `mise run test:ui` | 15 passed |
| `mise run check:rust` | Entire workspace compiled, including native Swift bridge and embedded Web assets |
| `mise run lint` | Rust/Swift/Python/Shell formatting and script checks passed |
| `mise run test:i18n` | 798 translation keys, native switching and embedded Web localization passed |
| `git diff --check` | Passed |

The backend suite, workspace compilation, lint and localization checks were rerun
after removing compatibility handling. App and shared UI test counts above are
from the preceding terminology change validation.

An earlier suite run timed out in the existing multi-client interruption test.
That test passed both in isolation and in the final complete suite without a
change to its timeout or assertions. The existing `block` dependency emitted its
Rust future-incompatibility notice during compilation.

## Limits

No real model calls, VM startup, remote production deployment or live App visual
inspection are included. Operating-system process variables, third-party desktop
terminology and external research terminology are separate from the product's
execution-location model. Historical reports retain their original test counts;
terminology edits do not claim a new execution of those historical checks.
