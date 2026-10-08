# Sandbox UI acceptance — 2026-10-06

## Delivered behavior

The existing global rail remains. The Sandbox page adds a 224 pt environment list and directly embeds the selected noVNC desktop on the right. The native desktop frame reserves the shared navigation header and both desktop toolbar rows. Environment buttons expose accessible names.

Stopped selection does not boot a VM. Start Desktop explicitly starts it; viewing a running execution environment requests its desktop component. View selection and execution-default selection are independent. The list shows running-task counts and the default marker. New creates a stopped Linux definition. Settings updates that environment's name/resources through Runtime; resource values persist under `sandboxes/<id>/sandbox.json` and apply at next start. The global resource configuration and native desktop dropdown are removed.

More provides reconnect, browser, clipboard, workspace, associated-task summaries, Set Execution Default, stop and delete. Active leases and unsettled operations explain disabled lifecycle actions. The backend independently rechecks busy state, stopped state for settings, and durable task/schedule references before deletion. Default Sandbox deletion is rejected. Management requests capture their target. View generations reject old status/probe completions, and native desktop visibility waits for the selected URL to finish loading.

## Verification

- `mise run check`, `test`, `test:app`, `build`, `bundle` passed; `git diff --check` passed.
- Backend: 100 passed, zero ignored. App: 14 passed.
- Added regression coverage for invalid names/resources, rejecting settings updates under a lease before Helper I/O/file mutation, and worker settings rejection.
- Native App interaction in isolated `/tmp/pot-ui-visual` verified new environment creation stays stopped, selected settings save, explicit execution-default changes persist, noVNC displays directly below the toolbar, Take Over / Give Back synchronizes labels, and switching to a stopped environment hides the desktop without starting it. Rapid switches and final accessible environment buttons were exercised.

Local logs: `/tmp/pot-ui-check.log`, `/tmp/pot-ui-tests.log`, `/tmp/pot-ui-app-tests.log`, `/tmp/pot-ui-build.log`, `/tmp/pot-ui-bundle.log`, `/tmp/pot-ui-e2e.log`; machine-readable VM results: `/tmp/pot-sandbox-ui-acceptance.json`.

## Scope

Interactive verification covered the light appearance at the default window size on macOS. A full dark/minimum-size/IME/keyboard matrix was not rerun. The execution providers remain Linux only. No real-provider credentials, paid inference, macOS/Windows Sandbox provider qualification or distribution notarization was used. Associated tasks are displayed as summaries in the management sheet; existing task pages remain available through global navigation.
