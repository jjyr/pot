# Control Web notification relay — 2026-10-11

Explicit Web hosting polls the fixed authenticated Control for final replies from
all assistants. Each browser subscription binds Control identity and independent
assistant cursors. Initial consent starts at current watermarks rather than
replaying history. Successful delivery advances only that assistant cursor;
transport failure keeps the original cursor. Changed Control identity/configuration
requires renewed subscription, and never falls back to local messages.

Notifications preserve assistant ownership in their tag and click URL. Internal
Task reports, streaming/system/failed messages and private worker stores do not
produce duplicate notifications. Existing browser permission, preview preference,
notification toggle and push encryption remain in use. No new configuration UI.

Checks passed:

- Runtime owner feed covers multiple assistants, restart, current watermark,
  ordered final replies and exclusion of synthetic Task reports/private workers.
- Fixed Control HTTP fixture validates service authentication, per-owner cursor
  catch-up, no browser credential forwarding and disconnected feed refusal.
- Push payload encryption asserts owner-specific URL/tag and UTF-8 preview bounds.
- Server: 32 passed, one existing opt-in ignored; Runtime: 177 passed;
  Storage: two passed. Workspace/all-target Clippy and localization passed.
- An upstream Control HTTP 401 becomes a distinct 502 authentication failure,
  preserving the browser's own login credentials.

A live FCM delivery/browser OS notification was not exercised. The isolated Web
fixture has no VAPID credentials; actual Chrome permission was not changed. This
report qualifies the relay, cursor/authentication contracts and encrypted payload,
not external push-service availability.
