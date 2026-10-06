# Mobile access

Issue [#8](https://github.com/jjyr/pot/issues/8), baseline `origin/main` f2d092d,
2026-10-07. This implementation is an owner-instance pilot. Desktop viewing and
control, physical-device qualification and public deployment remain acceptance
work, not enabled capabilities.

## Start the owner endpoint

Remote access is absent unless explicitly configured. The native Runtime must
remain on loopback. The mobile listener defaults to loopback too; opening another
interface is an explicit deployment choice.

```sh
mise run build:mobile
mise exec -- cargo build --locked -p pot-server --bins
POT_DATA_DIR=/path/to/isolated/product \
POT_WORKSPACE=/path/to/workspace \
POT_RUNTIME_BIND=127.0.0.1:17700 \
POT_ACCESS_ORIGIN=https://pot.example.com \
POT_ACCESS_BIND=127.0.0.1:17701 \
POT_ACCESS_CERT=/path/to/owner-fullchain.pem \
POT_ACCESS_KEY=/path/to/owner-private.key \
POT_ACCESS_ASSETS=/absolute/path/to/mobile/dist \
mise run server
```

Use a stable domain and a browser-trusted certificate whose private key is held
by the owner endpoint. TLS and VAPID keys must have mode 0600. Certificate renewal
currently requires restarting Server. No certificate provisioning, DNS changes,
public port opening or cloud tunnel is performed automatically.

If a TCP relay is needed, it must forward TLS bytes unchanged to this listener.
Do not terminate HTTPS at a CDN, reverse proxy, tunnel provider or other untrusted
service. Browser code and application traffic authenticate the same trusted Pot
origin. Session cookies, passkeys and certificates do not make a TLS-terminating
intermediary an end-to-end encrypted relay.

`build:mobile` uses native Rust 1.98.1 and a separate pinned
`nightly-2026-10-06` WASM toolchain because GPUI's wasm_thread dependency needs
an unstable WASM atomic intrinsic. It installs the matching wasm-bindgen CLI
0.2.129 under `.local/mobile-tools`, builds release WASM and precompresses it.
Generated assets are ignored by Git. It does not upgrade the native toolchain.

## Connect and revoke

GUI: Settings → General → Mobile access. Configure the Server first; when
launching an App-owned Server, provision its HTTPS environment. `scripts/pot build` builds the mobile assets and
`bundle` copies them beside the bundled Server; Server discovers that directory.
Standalone Server may use the explicit absolute asset path.
The GUI does not provision certificates. QR, pending approvals and revocation
use background native requests; no network call blocks the GPUI rendering thread.

Headless owners use local or SSH CLI administration:

```sh
POT_DATA_DIR=/path/to/product mise exec -- cargo run --locked -p pot-server --bin pot-access -- qr
POT_DATA_DIR=/path/to/product mise exec -- cargo run --locked -p pot-server --bin pot-access -- pair
POT_DATA_DIR=/path/to/product mise exec -- cargo run --locked -p pot-server --bin pot-access -- status
POT_DATA_DIR=/path/to/product mise exec -- cargo run --locked -p pot-server --bin pot-access -- approve REQUEST_ID
```

`pair` opens enrollment for five minutes. Scan the stable QR, request access,
and compare the code on the phone with the exact pending owner-side record.
Approve that record, then create the passkey on the phone. Approval is bound to
the requesting browser's HttpOnly ceremony cookie. The code is a comparison aid,
not a login credential. The QR contains only the public URL and never expires
as an invitation secret.

CLI also supports `close_enrollment`, `revoke_session ID`,
`revoke_credential ID`, `revoke_subscription ID`, `disable`, and `enable`. Disable persists, closes the
HTTPS accepting socket and invalidates sessions/challenges/pending enrollment.
Re-enabling does not restore previous sessions. The trusted loopback management
route remains available. Existing streams are checked at least once per second
and before chunks are delivered.

Passkeys persist in `access-credentials.json` with mode 0600 and atomic writes.
Sessions and challenges exist only in memory: 30-minute idle, 12-hour absolute,
5-minute ceremony expiry. Server restart requires login with the existing
passkey. Status checks and push heartbeats do not renew idle sessions; hidden
browser windows stop transcript polling. Synced passkeys prove the credential
holder, not one physical handset. User verification is mandatory.

Owner access includes the main chat and all instance environments. It is not a
single-Sandbox grant: the main Agent can indirectly operate other environments.
The public proxy exposes only chat/task input and cancellation plus read-only
snapshots, task queries, Sandbox inventory, usage and schedules. It injects the
native bearer internally. Provider settings, authorization endpoints, Terminal,
execution configuration, orchestration and desktop broker routes are denied.
Inventory exposes only display phase/generation and numeric hardware fields;
Helper viewer URLs, tokens and private network addresses are filtered. Agent
management-tool display replies use the same restricted state projection.

The browser runs GPUI Kit 0.7.1 WASM. The authentication shell and same-origin
transport adapter are JS. GPUI shares native design tokens and EN/zh-CN catalogs,
with touch navigation, per-conversation in-memory drafts, task filtering, browser
history and read-only overview screens. Input retries preserve the original
request UUID; an ambiguous write is never replayed with a new UUID automatically.
No transcript, credential or session is stored in localStorage or an offline cache.

## Add Pot to the Home Screen

Both the login page and mobile More provide **Add to Home Screen**. Installation
is available independently of enrollment, login and notification configuration.
The manifest uses a stable origin-scoped app ID, standalone display and dedicated
192/512 px icons. iOS also receives Apple Home Screen metadata and its touch icon.

On Android Chrome/Edge or another supporting browser, the button opens the
browser's installation prompt when available. If it is unavailable or dismissed,
use the browser menu's Install app / Add to Home Screen entry. Embedded QR
scanner browsers may require opening the URL in the system browser first.

On iPhone/iPad, the button explains Safari → Share → Add to Home Screen. Enable
Open as Web App when that option is shown. Launch the Pot icon to use a standalone
window. The app detects standalone mode and browser installation events rather
than treating acceptance of a prompt as completed installation.

A browser-trusted HTTPS certificate is required for deployment. A Service Worker
registers even when VAPID/push is not configured, but uses network-only fetches:
there is no offline transcript or credential cache. Installation does not grant
access, change session expiry or request notification permission. A newly installed
Home Screen app may require Passkey login again; an authorized synchronized
Passkey still works. Notifications remain a separate explicit opt-in.

## Main Agent message notifications

Provision a separate P-256 VAPID private key and contact:

```sh
openssl ecparam -name prime256v1 -genkey -noout -out /path/to/vapid.key
chmod 600 /path/to/vapid.key
# Add these to the Server environment before startup:
# POT_ACCESS_VAPID_KEY=/path/to/vapid.key
# POT_ACCESS_VAPID_CONTACT=mailto:owner@example.com
```

In mobile More, explicitly enable message notifications and grant browser
permission. Actual message text is the default. A separate private-preview choice
uses a generic message. Permission denial does not prevent chat. On iPhone,
Web Push requires a supported iOS release and the installed Home Screen web app;
ordinary Safari tabs and embedded scanner browsers are not equivalent.

Only completed, nonempty, persisted main Assistant messages enter an SQLite
outbox in the same transaction as their canonical chat record. Message-ID
uniqueness prevents replay/update duplicates. Worker databases, task lifecycle
events, System report wrappers, failed/cancelled partial replies and synthetic
tool-only display wrappers are not sources.

Subscriptions are bound to an authorized credential and a browser installation.
Consent starts at the current outbox sequence rather than notifying old history.
Each delivery encrypts the original-text excerpt using RFC 8291 aes128gcm and
signs VAPID authorization. Push providers receive ciphertext plus routing/timing
metadata. Payloads are bounded to 3000 bytes without translating model content.
Only the known Google, Mozilla and Apple push hosts are allowed; redirects and
arbitrary callback URLs are rejected.

Delivery cursors and retry state persist. Trusted `status` lists subscription
failure counts/paused state without revealing push endpoints or encryption keys.
Live language changes update only the current installation's notification locale. Foreground main-chat reading suppresses
that browser's push; other browsers and the Mac unread state are independent.
Notification clicks open main chat after authentication, carrying a message ID
in the link; locating/scrolling to that exact message remains unfinished. Logout unsubscribes the
current installation; credential revocation removes its subscriptions. Session
expiry alone does not revoke consent. Seven consecutive delivery failures pause
an installation until explicit re-consent. Delivery older than 24 hours is skipped.
An already accepted push cannot be recalled. A crash after provider acceptance
can resend it; stable notification tags replace the same message on that device.
Exactly-once OS delivery is not promised.

## Verification and remaining gates

See [2026-10-07 acceptance report](validation/mobile-access-20261007.md).
The desktop broker, RFB credentials and takeover remain unreachable. No
restricted-Sandbox authority, production relay, complete App bundle boot,
physical iOS/Android touch/IME/performance, reduced-motion preferences, mobile
screen-reader parity or real
push-provider delivery is declared accepted by these tests. The pilot preserves
those gates instead of exposing unqualified desktop control.
