# Integrated titlebar and parent navigation — 2026-10-12

The user approved a single compact titlebar, parent navigation and a background
matching the chat canvas. The native App now uses one 44 px Kit TitleBar with native
window controls, drag/double-click handling, no bottom divider, and the shared
`#FDFDFD` / `#202020` canvas colors. The sidebar starts below that row. Swift desktop
geometry reserves 44 px plus its two existing 56 px viewer rows. Settings no longer
repeat the selected tab heading; native allowance configuration uses only the
shared parent chevron.

The left chevron and Command-[ follow route hierarchy. Task and schedule details
return to their respective lists, conversation Usage returns to the corresponding
conversation, and allowance configuration returns to overall Usage. Root pages
have no chevron. Forward UI, menu and Command-] are removed. Route-local state is
cached per Runtime/assistant, independently for local/Control modes, with separate
Tasks/Schedules list identities. Long title text is measured before truncation;
tooltips retain its full value.

## Validation

- `mise run test:app`: 22 passed, plus native client safety regressions.
- `mise run test:ui`: 13 passed.
- `mise run test:i18n`: 811 keys, native and embedded web localization passed.
- `mise exec -- cargo build --locked -p pot-app -j 4`: passed, including Swift bridge.
- Changed-source formatting checks and `git diff --check`: passed.
- Native CUA inspection used a separately named preview bundle. Verified light
  Chat and Conversation Usage, dark Settings, root Usage, allowance configuration
  with a single parent button, click return to Usage and Command-[ return from
  conversation Usage to Chat. Screenshots showed a continuous titlebar/canvas
  background and no separate navigation row. No messages were sent and no Sandbox
  sessions were started.

Automated route checks cover task/schedule parents, direct-link hierarchy, root
routes without Back, cache isolation and preserving parent state after unrelated
visits. Live populated task-list scroll restoration, minimum-window layout and an
active embedded desktop were not exercised in this preview. Native window control
accessibility was present; CUA's screen-sharing overlay obscured the traffic-light
pixels. Launching the preview executable directly from the shell aborted in
macOS `_RegisterApplication`; the subsequent CUA-launched bundle allowed visual
inspection, but inheritance of the disposable root environment was not established.
A further preview with fixed environment configuration was rejected by LaunchServices
with permission error -54. This run therefore does not qualify isolated Runtime
acceptance, native drag/double-click behavior, or clean shutdown.
