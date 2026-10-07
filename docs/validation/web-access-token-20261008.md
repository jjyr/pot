# Web access Token redesign — 2026-10-08

Implemented: one persistent random instance Token for App/CLI, automatic browser
configuration from URL fragments, dedicated App Web access settings, embedded
libtailscale tailnet HTTPS, and removal of browser enrollment APIs/commands/docs.
CLI localhost/manual HTTPS continue independently of the optional Tailscale mode.

## Verification

- `cargo test -p pot-server --tests`: Server access, CLI, lifecycle, standalone
  HTTPS/relay, multi-client receipts, streams and browser-bound desktop tickets passed.
- Token persistence across restart, direct Authorization headers, signed browser
  cookies, ignored legacy client authority, private API denial and inventory secret
  projection passed. Regeneration invalidated both clients and their live desktops.
- `cargo check -p pot-app` and strict App/Server all-target Clippy passed.
- `mise run build:mobile` built the pinned WASM client; `mise run test:i18n` passed.
- `mise run build:tailscale` built the actual vendored libtailscale adapter and
  collected notices for linked Go modules and the Go runtime.
- The explicit ignored `libtailscale_ffi_loads_and_returns_owned_errors` test was
  run after building the library. Dynamic loading, exported symbols, owned JSON
  response/free and disconnected-node errors passed without network enrollment.
- Real in-app browser against an isolated localhost Server: connection fragment
  configured the Token and disappeared from the address bar; plain-page reload
  restored access; rotation displayed the Token configuration form and stopped
  protected content; manual Token replacement and a same-page connection fragment
  recovered access. Test instances had no configured model or user credentials.

- Follow-up: removed Add to Home Screen from the Token configuration page while
  retaining the authenticated GPUI More action. JavaScript syntax, localization
  catalog validation (742 keys), and whitespace checks passed.

## Limits

No real Tailscale account login/device approval, tailnet ACL connectivity, trusted
certificate issuance/renewal, physical phone scan, or complete signed App/CLI release
archive was exercised. Those remain live/distribution qualification gates. Tests
and library construction do not establish real-tailnet acceptance. No user network,
Tailscale account, system client or production instance was changed.
