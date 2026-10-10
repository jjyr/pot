# Control membership, 2026-10-11

Replaced the interim peer federation with a single standalone Control and outward
HTTPS polling by execution nodes. Removed machine capability types, grant/revoke
APIs, old pairing/inbound execution routes, and grant UI. Model permission approvals
are retained. Embedded App Runtime rejects enabling/restoring Control before any
listener is opened. The CLI no longer eagerly launches a Sandbox Helper.

The new tests cover signed identity/credential/recipient/deadline binding, one
Control per member, invitation exact retry/conflict/removal, private credential
projection, failed persistence, no inbound member command listener, removal/leave,
boot fencing, scope cleanup, uncertain receipts across restart, management busy and
identity conflicts, and lost connectivity with original-result reconciliation.

Core 16 and Runtime 160 passed. Server unit 20 passed / one existing opt-in ignored,
plus the added invitation persistence test passed separately. Server access 5, TLS 1,
CLI 2, lifecycle 10, multiple-client 5, and remote task 3 passed (one actual Podman
case is opt-in). App 19 and shared UI 12 passed. Localization checks passed.

Actual devpc Linux rootless Podman qualification was rerun using Control-at-caller
and target-joins-outward. One test passed in 14.32 seconds: two task turns, destination
project rules/installed skills, owner approval, and existing Sandbox
start/stop/update/recover/delete. The dedicated container store was empty afterward.
See the machine inventory's `pot-runtime-20261011.md` for isolated paths and logs.
This uses two Runtime processes on devpc and deterministic SSE model responses,
not cross-machine networking, a paid model, Apple VM, or native browser acceptance.

App Control configuration/mode isolation, explicit fixed-Control Web hosting, and
remaining remote plugin/artifact/desktop transports are separate unfinished phases
in #39. Earlier peer/grant reports are historical, not evidence for these features.
