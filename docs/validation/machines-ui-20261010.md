# Local Machines UI acceptance — 2026-10-10

Partial P3 of [#39](https://github.com/jjyr/pot/issues/39). The approved App
navigation keeps conversational assistants local and merges machine connections
and execution environments into the top-level Machines destination. This report
covers the delivered local portion; peer connections are not implemented yet.

## Delivered behavior

- Authenticated `/api/v1/machines` discovery works on a bare Runtime without
  initializing a conversation store, Agent or execution environment. Host
  resource creation updates inventory without implicitly creating an assistant.
- Shared native/Web machine groups contain typed Host/Sandbox rows. Machine
  headings show identity and available capabilities. Host details show working
  directory, authorization requirement and scoped session counts. Session
  metadata excludes command text, outputs and credentials.
- Sandbox detail reuses the existing desktop/management presentation without a
  second list or resource-creation entry. Native resource-cache access remains.
- Selection never changes execution routing. Native history distinguishes
  machine, Host and Sandbox details; returning to a removed target resolves its
  surviving machine. Native desktop overlay is hidden on other details.
- Tasks keep assistant groups and use typed execution locations. Local locations
  show “This machine”; Host is distinct from a Sandbox whose ID is `host`.
- The execution-default marker follows the inventory's actual selection, including
  the absence of a default. Unavailable Sandbox state explains the missing
  provider/resources and disables desktop actions.
- Chat retains its local assistant controls and has no machine selector.
  Resources remain API-only. English and Simplified Chinese labels are included.

## Verification

App 19, shared UI 12, Runtime 158, Tool Executor 8 and Server 40 tests passed;
one pre-existing Server test remains ignored. Server counts include unit/access,
TLS, CLI, lifecycle and multi-client suites. The final App/UI/Server rerun covers
navigation destination identity, authenticated bare discovery and dynamic Host
inventory. Existing Runtime/Tool Executor suites cover the underlying Host
execution and rule boundaries.

`mise run check`, backend Clippy with warnings denied, JavaScript syntax and
`git diff --check` passed. UI/App Clippy passes with the pre-existing
`assistants::Editor::render` argument-count warning. Release WASM was built with
the pinned nightly and wasm-bindgen version; generated browser assets were
packaged through the existing build script.

Chrome's agent profile used the previous disposable acceptance Runtime at
`http://localhost:19740`. Visible browser interaction verified the top-level
Machines entry, machine heading, Host workspace and empty-session details,
Sandbox unavailable detail, local Chat and preserved grouped Tasks. Browser
acceptance found and fixed the wide-screen stacked layout, centered list rows,
long local UUID display and clipped authorization text. No model call, VM start
or environment effect was requested during these UI checks.

## Remaining P3 scope and limits

HTTPS peer pairing, persistent peer credentials/identity verification,
capability grants/expiry/revocation, remote inventory/adapters/leases and
reconciliation remain unimplemented. Offline-selection behavior is a portable
state test, not a real disconnected-peer qualification. Physical native App
interaction and real VM/model execution were not qualified in this UI slice.
P3 remains in progress and has no phase-completion commit.
