# Mobile owner access validation — 2026-10-07

## Baseline and scope

Issue [#8](https://github.com/jjyr/pot/issues/8), branch
`codex/issue-8-mobile-access`, based on upstream main
`f2d092d0c71b9c11206ff2bec4b06755179a3bf8`.
This is an owner-instance pilot, not full issue acceptance. No public endpoint,
production certificate, DNS entry or vendor push account was provisioned.

The browser runs the actual pinned GPUI Kit 0.7.1 WASM app. Native Rust remains
1.98.1; WASM uses nightly-2026-10-06 and wasm-bindgen CLI 0.2.129. Release WASM
measured approximately 17 MB raw / 5.4 MB gzip. This is transfer-size evidence,
not proof of the proposed physical-device startup or responsiveness targets.

## Automated security and persistence checks

Server tests exercise owner approval, browser-bound ceremonies, registration and
repeat authentication with a software Passkey authenticator. Negative cases cover
expired/replayed challenges, changed signatures/client data, missing user
verification, unauthorized Origins, disallowed proxy routes, session/credential
revocation, restart behavior and disabled-policy persistence. Streaming revocation
is checked while a response is open. HTTP/2 split Cookie headers are covered.

An adversarial inventory fixture includes viewer URLs/tokens, private IPs and
unexpected fields. The public response omits them and retains only the supported
display/hardware fields. Runtime management-tool replies use a restricted display
projection as well; private native Helper communication retains its existing
credentials for the Mac viewer.

A real Server subprocess uses a generated isolated certificate and a TLS 1.3
client that explicitly trusts that certificate. A hostname mismatch fails. A
local TCP-only relay forwards bytes without a key; the client authenticates the
owner certificate through it. Disabling closes the accepting HTTPS socket;
re-enabling binds it again. This qualifies the local transport contract, not an
external relay deployment or certificate renewal process.

Web Push unit checks build actual RFC 8291 aes128gcm messages with VAPID,
decrypt using the test recipient key, reject tampering and enforce payload bounds.
Subscription endpoints are restricted to supported vendor hosts. Installation
locale and logout behavior are covered. Storage tests verify transactional
completed-main-message outbox eligibility, uniqueness and persistence after
restart; synthetic tool-only wrappers do not notify. A delivery cursor cannot
regress when re-consent races with an in-flight accepted send.

## Browser observations

An isolated Chromium session used a virtual CTAP2 authenticator with user
verification, a loopback HTTPS endpoint and a deterministic local model fixture.
Test certificate bypass applied only to this isolated browser session; no root
certificate was installed. All test data and keys stayed under ignored `.local`.

- Owner-approved registration opened GPUI chat. A canonical user message received
  the mock main Agent reply and the composer cleared after acknowledgement.
- Server restart required login; the previously registered Passkey authenticated
  without another owner enrollment window.
- 390 × 844 and 360 × 780 CSS-pixel viewports rendered without horizontal DOM
  overflow. Dark mode and browser-local English/Chinese changes rendered live;
  model text stayed literal. Physical touch/IME and canvas text layout are separate
  qualification items.
- A focused unsent draft survived opening More and browser Back.
- Injecting the first POST network failure exposed explicit retry. Retry retained
  the same request UUID, and the canonical native snapshot contained exactly one
  matching user record. The injected network error was expected.
- The self-signed certificate prevented Service Worker registration despite the
  page-level certificate bypass. The client handled this failure while retaining
  chat access. Real encrypted vendor push delivery was not tested.

Retained screenshots contain only synthetic fixture conversations:

| Evidence | Artifact |
| --- | --- |
| GPUI chat and main reply | [Chat](../../output/playwright/mobile-chat-final.png) |
| Mobile More | [More](../../output/playwright/mobile-more.png) |
| 360 px dark mode | [Dark chat](../../output/playwright/mobile-chat-360-dark.png) |
| Uncertain write | [Retry prompt](../../output/playwright/mobile-retry-error.png) |
| Same-ID retry acknowledgement | [Retry result](../../output/playwright/mobile-retry-success.png) |
| Draft after browser Back | [Draft](../../output/playwright/mobile-draft-back.png) |

## Checks

Final regression after vendored OpenSSL and push-cursor corrections:

| Check | Result |
| --- | --- |
| `mise run test` | 244 passed; 1 existing paid-model test ignored |
| `mise run test:app` | 17 passed |
| `mise run clippy` | All workspace targets passed with warnings denied |
| `mise run lint` | Rust/Swift/script formatting and script checks passed |
| `mise run test:i18n` | 706 shared translation keys passed |
| `git diff --check` | Passed |
| `cargo build -p pot-server --bins` through mise | Server and admin CLI built |
| `otool -L` on built Server | Only Apple system libraries; no Homebrew OpenSSL dependency |

The earlier `mise run check` also passed workspace compilation and Swift Helper
release compilation. The pinned `mise run build:mobile` produced the actual
release WASM used in browser validation; no mobile source changed afterward.
The existing `block` dependency emits a future-Rust compatibility notice; it
is not a test or clippy failure. Bundling now includes both Server and `pot-access`
plus the mobile assets. Complete App bundle startup remains a separate gate.

## Remaining acceptance work

- Physical iPhone Safari/Home Screen and Android Chrome: scanning, synchronized
  Passkeys in another browser, touch/selection, Chinese IME, keyboard resizing,
  safe areas/rotation, resume, accessibility, reduced-motion preferences,
  performance, memory and battery. Canvas accessibility is not qualified by a
  DOM snapshot or an emulated viewport.
- Trusted public certificate issuance/renewal, external TCP relay availability,
  host sleep/wake and production deployment failure modes.
- Real Google/Mozilla/Apple push permission, background/closed-page delivery,
  subscription replacement and provider-error retries. Encryption tests do not
  prove OS delivery. Already accepted alerts cannot be recalled; tags reduce
  visible duplicate alerts after a crash, not guarantee exactly-once delivery.
- Notification links open main chat after authentication; exact-message
  locating/scrolling is still unfinished.
- Complete cold App bundle boot and distribution signing/notarization. The
  Server crypto dependency must be bundled rather than depend on Homebrew.
- Desktop viewing/control and Terminal remain unavailable through mobile.
  Generation-bound leases and Agent shell/CDP/X11 fencing require independent
  implementation and negative acceptance before exposure.
- Restricted-Sandbox grants, principal propagation, separate transcript isolation
  and multi-user collaboration are outside this owner pilot. Main-chat authority
  includes the entire owner instance and its managed environments.

Issue #8 remains open because physical-device and control acceptance is incomplete.
No public deployment or issue edit was performed. The initial validation preceded
the subsequent user-authorized commit and PR publication.

## Follow-up: Home Screen installation

Added a login-page and GPUI More installation entry, localized iPhone/iPad Safari
instructions, deferred supporting-browser installation prompts, standalone state
recognition, stable manifest ID, Apple metadata and actual 192/512 px PNG icons.
Service Worker registration now works before authentication and without VAPID.
Fetch remains network-only; installation does not grant access or request push.

An isolated localhost Chromium fixture reported no manifest parse errors. Its
installability diagnostic reported only `in-incognito` (the test runner's private
context), so actual native OS installation was not performed. Before login and
with push disabled, the worker was registered and controlling the page and Cache
Storage was empty. Synthetic installation events verified one prompt invocation,
dismissal leaving the install label unchanged, and `appinstalled` updating the
label. Notification permission remained `default`.

At 390 × 844 with emulated iPhone identification and Chinese locale, the install
button opened localized Safari/Share/Home Screen instructions. This checks the
platform guidance branch, not physical iOS installation. Physical Android/iOS
installation, OS icon launch and Home Screen push delivery remain acceptance
work. The mobile release build, all 18 Server tests, lint and 713 translation keys
passed after this follow-up.

The actual isolated owner HTTPS Server served the installation JS, manifest,
Service Worker and both icons without authentication, with correct MIME types
and `no-store`. PNG headers confirmed the advertised 192/512 px dimensions.
Clippy and `git diff --check` passed. The iPhone guidance had no horizontal
overflow; [retained screenshot](../../output/playwright/mobile-install-ios.png).
