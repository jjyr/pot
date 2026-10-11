# Browser access

Web UI is hosted explicitly with `pot serve --ui --control URL` and a
server-held Control credential. Plain Runtime startup and the App's embedded
Runtime have no browser endpoint. The Web UI always uses its configured Control
and has no local/remote switch. See [Web deployment](web-ui-deployment.md) for
listener, certificate and credential setup. Native Runtime and guest brokers stay
private. Remote desktop and push notification transport remain pending in #39;
the sections below describe reusable browser authentication and transport mechanics,
not delivery of those remote features.

## Access Token

Each explicit Web host generates a persistent random 256-bit browser Token.
`pot open` and `pot access qr` use `#access_token=...` to configure the browser
automatically. This Token is separate from the server-held Control credential. Connection
links, QR codes and their screenshots contain full access credentials: do not share.

The browser removes the fragment immediately, verifies and saves the Token in
origin-scoped IndexedDB, and sends Authorization headers on API requests. Returning
browsers automatically connect. The static UI is available without authentication;
protected data and operations require the Token. Token-derived HttpOnly, Secure,
SameSite=Strict signed cookies support desktop navigation and WebSockets. Cookies
contain a random browser identity and HMAC, not the Token. Browser storage compromise
can expose this bearer credential; clearing browser storage requires configuration again.

CLI uses `pot access token`,
`pot access regenerate-token`, `pot access qr` and `pot open`. Regeneration invalidates
all old Tokens/cookies, ends protected streams/desktops and clears push subscriptions.
Browser More offers Change access Token and Clear local Token. There is no per-browser
permission list. Clearing a browser Token does not revoke the shared instance Token.

The instance stores Token/policy in `product/access-token.json`, mode 0600 and atomic
replacement. Only local owner administration can retrieve or regenerate it. Old
client authorization files are ignored. Token persists across restart and disabling
Web access. Changing domain or installing a separate Home Screen app may require
configuration in that origin/storage context. Same-origin checks, public API allowlists,
private inventory projection and bounded requests remain enforced.

Desktop tickets are bound to their creating browser cookie and expire after one
hour. Token regeneration or disabling Web access terminates their live display
streams and releases input. Desktop assets ship with the Web release; private
broker URLs, tokens and addresses are never exposed by inventory.

Stopping the Web host closes its browser listener. Restart restores the browser
Token but still requires explicit UI startup and the Control destination. Closing
App shuts down its local Runtime; it does not stop the independent Control or Web
host. No public Funnel endpoint is configured.

The browser runs GPUI Kit 0.7.1 WASM. The authentication shell and same-origin
transport adapter are JS. GPUI reuses `pot-ui` Chat/Tasks/Sandbox/Usage components, Markdown, page/chat state,
native design tokens and EN/zh-CN catalogs,
with touch navigation, per-conversation in-memory drafts, task filtering, browser
history, a Sandbox desktop launch button and read-only Usage screens. Tasks share rows, phase labels,
search/filter counts, empty states and server-authoritative pagination. Usage
shares date ranges, statistics, model grouping and charts. Browser allowance
editing and environment administration remain disabled. The desktop opens in a
separate same-origin tab with view-only mode until explicit takeover.
See the [shared-page validation](validation/shared-pages-20261007.md). Input retries preserve the original
request UUID; an ambiguous write is never replayed with a new UUID automatically.
No transcript, credential or session is stored in localStorage or an offline cache.

## Add Pot to the Home Screen

After configuring the access Token, browser **More** provides **Add to Home Screen**.
The Token configuration page has no installation action. Installation does not request notification permission.
The manifest uses a stable origin-scoped app ID, standalone display and dedicated
192/512 px icons. iOS also receives Apple Home Screen metadata and its touch icon.

Home Screen, browser/notification and login branding use the selected terracotta
Pot Companion. `scripts/make-icons.swift` generates both the rounded Mac icon
(`crates/app/assets/Pot.png` / `Pot.icns`) and an opaque, square cream-background master
(`PotHomeScreen.png`) so mobile operating systems can apply their own masks.
`scripts/build-mobile` produces the touch icon and 192/512 px `any maskable` icons
from that master. Versioned icon URLs distinguish this design from previously
installed icons without changing the manifest's stable application ID. Mobile's
GPUI header embeds the matching Mac image. README uses the same application icon.

On Android Chrome/Edge or another supporting browser, the button opens the
browser's installation prompt when available. If it is unavailable or dismissed,
use the browser menu's Install app / Add to Home Screen entry. Embedded QR
scanner browsers may require opening the URL in the system browser first.

On iPhone/iPad, the button explains Safari → Share → Add to Home Screen. Enable
Open as Web App when that option is shown. Launch the Pot icon to use a standalone
window. The app detects standalone mode and browser installation events rather
than treating acceptance of a prompt as completed installation.

A browser-trusted HTTPS certificate is required for remote Service Worker and push support. A Service Worker
registers even when VAPID/push is not configured, but uses network-only fetches:
there is no offline transcript or credential cache. Installation does not grant
access, change authorization or request notification permission. A newly installed
Home Screen app may use a separate storage context and require its own Token configuration. Notifications remain a separate explicit opt-in.

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

Subscriptions are bound to the current Token generation and a browser installation.
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
current installation; Token regeneration removes all subscriptions. Session
expiry alone does not revoke consent. Seven consecutive delivery failures pause
an installation until explicit re-consent. Delivery older than 24 hours is skipped.
An already accepted push cannot be recalled. A crash after provider acceptance
can resend it; stable notification tags replace the same message on that device.
Exactly-once OS delivery is not promised.

## Verification

Server access, CLI, multi-client and TLS suites exercise Token authentication,
persistence, rotation, concurrent receipts and desktop isolation. Physical phone
interaction, real tailnet enrollment/certificate issuance and push-provider delivery
require live qualification; builds/local fixtures alone do not establish them.
