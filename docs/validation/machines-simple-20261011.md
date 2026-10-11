# Lightweight Machines UI, 2026-10-11

## Scope

The approved Machines interaction replaces the full Sandbox management view with
machine status cards and expandable Host/Sandbox rows. Details contain status,
View Tasks, and a desktop entry only when a client transport is implemented.
The local App opens the existing desktop viewer in a browser; Control Web and
remote App do not advertise an unimplemented desktop transport.

There is no assistant-management shortcut, artifact list or download button.
Technical operations and file transfers belong in ordinary Chat. This change
adds no model-visible tools or file-transfer behavior. New transport/tool
contracts remain work in #39. Resource creation remains API-only.

The old shared Sandbox renderer, Web Sandbox navigation handlers, native workspace
menu entry and work-environment settings tab have been removed. Desktop session
cleanup is explicitly sent to the local Runtime even after a client mode change.

## Automated validation

- Runtime task-page tests: exact Runtime/Host/Sandbox matching before pagination,
  invalid environment reference rejection, and existing scope/search pagination.
- Shared UI: 13 tests including desktop transport availability.
- App: 22 tests including rejection of a late task page from a previous environment;
  existing Swift/client safety checks.
- Localization: 803 keys and native/browser language switching.
- App, Server, UI and Mobile all-target Clippy with warnings denied.
- Fresh pinned-toolchain release WASM build.

The environment filter is an HTTP/UI TaskQuery field. The existing list_tasks model
tool schema and behavior remain unchanged. Global task activity counts remain
available independently of the visible environment filter.

## Limits

No new native click-through or real VM desktop acceptance was performed for this
UI change. Chrome agent-profile acceptance of the Control Web fixture remains
blocked by ERR_BLOCKED_BY_CLIENT on localhost:19753. Automated tests and the WASM
build do not establish manual browser acceptance. Remote desktop, installed-plugin
transport, file delivery to the App machine, remote Agent runners, and three-node
qualification remain incomplete in #39.

## Later Web browser acceptance

After Chrome site access was allowed, the latest Control Web fixture passed
login, named Control assistant rendering, Machines Host expansion, View Tasks
navigation and clearing the environment filter. The empty task list was used;
Runtime tests establish populated-set filtering and pagination. The earlier
ERR_BLOCKED_BY_CLIENT is resolved. See [Control Web acceptance](control-web-20261011.md#browser-acceptance-after-site-access-was-allowed)
for the plugin digest fix and exact validation scope. Native click-through,
remote desktop and file transport remain unverified or unimplemented as stated.
