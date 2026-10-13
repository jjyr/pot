# Explicit Control Web hosting — 2026-10-11

P3c changes `pot serve` to start without browser access regardless of ambient or
saved listener settings. `--ui --control URL` explicitly enables it; a server-held
`POT_CONTROL_TOKEN` is required and `POT_CONTROL_CA` optionally supplies a private
CA file. The destination must authenticate as a standalone Control before the
hosting Runtime initializes product data. Missing arguments/credentials fail early.
The App's embedded Runtime has no Web endpoint; obsolete App Web/Tailscale tabs,
QR state, access polling and local notification bindings were removed.

The Web backend retains its browser Token/cookie/origin/route boundaries while
forwarding product requests only to Control. Browser credentials are not forwarded;
upstream cookies and redirects are not exposed. SSE streams without buffering the
whole response. Failed Control connections return `control_unavailable` and never
use local product state. Remote desktop returns an explicit pending-feature error;
local notification state is never used for remote browser notifications.

Validation passed: Server unit 22 (one existing ignored), access 5, TLS 1, CLI 3,
lifecycle 10, remote execution target 3 (one real Podman opt-in ignored), App 21, 800
translation keys, workspace all-target compilation, App/Server Clippy, and fresh
WASM build. The TLS process test verifies authenticated browser API reads return
Control's Runtime identity while the Web host has no assistant database. Fixed
proxy tests verify SSE first-chunk delivery, credential/cookie isolation, required
Control role, and disconnection errors without fallback. TLS enable/disable and
certificate/relay validation are retained.

Manual Chrome agent-profile acceptance was attempted at `http://localhost:19753`.
Chrome blocked this origin with ERR_BLOCKED_BY_CLIENT; site access must be allowed
before manual acceptance can continue. No current browser interaction is claimed
as passed. Cross-machine deployment, real model and Apple VM qualification remain
later #39 phases.

## Browser acceptance after site access was allowed

Chrome agent profile at `http://localhost:19753` now passes browser login and
loads the named **Control Assistant** from the independently running Control.
The fixed Web host has no local/remote toggle. The latest Web build was served
from the disposable `/private/tmp/pot-control-app-acceptance/web-host` fixture,
using its existing test-only browser Token. Real App data was untouched.

The session exposed a real cross-build plugin identity defect: App serialization
preserved JSON object order while standalone builds sorted it, so persisted App
bundles failed digest validation in Control. JSON order is now preserved on reads,
new plugin digests sort object keys recursively, and existing bundles still require
an exact full-payload digest. Tests cover nested schema reorder, stored-bundle
round-trip and tampered file rejection. No model-visible tool contract changed.

Validation after the fix: Core 17, executor 9 plus integration 8, Runtime 161,
Server 22 (one existing ignored), App 22, all-target Core/Server/App Clippy and
fresh Web WASM build. Existing Control configuration loaded without resetting or
re-pinning stored bundles. No real model call was made.

Machines browser acceptance used a Host definition created through the resource
API in the disposable Control fixture: expand/collapse, status, View Tasks,
execution target-filter indicator and clearing the filter passed. Host has no desktop
entry. No management shortcut, plugin/session/work-directory details, artifact
list, download or resource-creation controls appeared. The task list was empty;
exact matching against populated task sets is covered by the Runtime tests.

Evidence: `/private/tmp/pot39-machines-web-20261011.jpg`. This verifies the
Control Web and lightweight navigation, not an enrolled remote execution machine,
remote desktop/file transport, real Sandbox VM or remote Agent runner.
