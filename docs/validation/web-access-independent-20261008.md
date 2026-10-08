# Independent Web UI access — 2026-10-08

App settings now have separate Web UI and Tailscale tabs, reachable from every
settings page. General retains language, appearance and shortcut help. New
instances default to no enabled browser entry points. Existing enabled tailnet
HTTPS instances retain their mode on upgrade.

Web UI provides independent LAN HTTP and Tailscale access switches plus optional
Tailscale HTTPS. LAN listeners bind active private IPv4 addresses on port 17701;
there is no user-facing localhost entry. Embedded tailnet HTTP uses port 80 and
HTTPS uses port 443. The embedded node is connected independently of browser
access. Turning browser access off leaves the node connected; disconnecting the
node leaves LAN listeners available and preserves tailnet Web UI intent.

All entry points use the existing persistent instance Token. HTTP listeners use
HttpOnly, SameSite=Strict cookies without Secure or the __Host- prefix. HTTPS
retains the secure cookie policy. Each listener checks its own exact origin and
has an independent live authorization guard, including scoped desktop authority.
UUID generation falls back to getRandomValues when HTTP lacks randomUUID.

## Verification

- `cargo test --locked -p pot-server --tests -j 4`: Server, CLI, lifecycle,
  standalone TLS, multi-client streams and desktop isolation passed.
- Added an isolated real HTTP listener test covering default-off policy, LAN
  operation without a tailnet node, Token/cookie login, rejected cross-origin
  requests, failed tailnet enable leaving LAN usable, Token rotation, endpoint
  guard invalidation and persisted policy.
- Strict App/Server all-target Clippy passed; App compiled with both navigation
  paths exposing the new settings tab. Settings navigation wraps when needed.
- `mise run test:i18n`: 752 translation keys and native/web localization passed.
- `mise run build:mobile` and `mise run build:tailscale` passed. The ignored FFI
  loading test passed explicitly, including the new listener-mode symbol.
- Chrome via Playwright against an isolated Server on the Host's actual private
  IPv4 address: Token URL fragment configured access and disappeared; GPUI canvas
  rendered over HTTP using WebGL2; restarting Server and reloading the plain URL
  restored saved LAN policy and IndexedDB Token. The page reported
  `isSecureContext=false`, no native randomUUID, a loaded canvas and a hidden Token
  form. Final reload had no browser errors; WebGPU fallback/renderer warnings
  remained. No model, user credentials or user instance were used.

## Limits

No real tailnet login, ACL connectivity, HTTP/HTTPS mode switch or certificate
issuance/renewal was exercised. Physical phones, live Sandbox desktops over
remote HTTP and the complete signed App bundle were not qualified. LAN currently
supports private IPv4 interfaces; changing network interfaces requires refreshing
LAN access or restarting App. HTTP does not enable browser secure-context features
such as Web Push or Service Workers. Tailnet HTTP remains network-encrypted but
is not a browser secure context.
