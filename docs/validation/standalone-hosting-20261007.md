# Standalone hosting and pairing validation — 2026-10-07

Branch: `codex/issue-8-mobile-access`; pre-change baseline:
`830ade63d6f56fd822b64ef3a95fb27d66a876b1`. This report records the replacement
of WebAuthn and the introduction of standalone CLI / embedded App hosting.
Earlier mobile and shared-page reports remain historical evidence, not proof
that the new authentication flow or embedded App boot has passed UI acceptance.

## Automated results

- Full backend regression (`mise run test`) passed without paid model calls;
  the existing paid-model test remains ignored.
- Latest targeted run: 22 Server tests, 16 App tests and 8 shared UI tests passed.
  Coverage includes one-use/replaced/expired pairing codes, guessing limits,
  signed key login after restart, revocation, cookie-bound challenges,
  CSRF/private-route rejection, legacy disabled-policy preservation,
  encrypted push, TLS passthrough and listener lifecycle.
- Hosting tests verify in-memory App dispatch without a management socket,
  restricted integration TCP routes, same-root exclusion, tokenless local CLI
  control, browser-Origin refusal, standalone operation without App and orderly
  shutdown. CLI service tests only write a private LaunchAgent definition under
  a temporary HOME; they neither register nor start a system service.
- Workspace Clippy with warnings denied, formatting/script lint, 711 translation
  keys and the Swift Helper release build passed. Native App and release WASM
  builds passed.
- Release CLI with embedded Web assets built successfully; the Darwin ARM64
  archive was generated. The binary reports version `0.1.0`. An isolated prefix
  installation served the login shell without repository assets or App.

## Initial browser observation (before UI confirmation)

An isolated Chromium profile opened `http://localhost:17915` from an empty
instance with no configured model credentials, Helper or VM. The login shell
loaded, `isSecureContext` was true, WebCrypto was available and no Passkey
buttons remained. [Login screenshot](../../output/playwright/standalone-hosting/pairing-shell.png).
Localhost HTTP is not encrypted remote transport. Remote deployments require
trusted owner-terminated HTTPS with TLS passthrough relays.

No pairing code was submitted. Browser pairing, IndexedDB key persistence,
automatic signed login, restart/revocation through the real browser and the
post-login WASM flow are **not accepted by this observation**. Their backend
contracts passed automated tests, but UI end-to-end validation remains pending.

## Initial native observation and gates (before UI confirmation)

The native App compiled, including the Rust/Swift in-process request bridge and
Sandbox-only Helper lifecycle. An isolated fixture App was prepared but not
launched. Native boot, settings interaction, restart and quit under the new
hosting model remain unverified. Previous shared-page screenshot comparisons
predate this host change and do not establish current runtime parity.

Automatic approval review rejected browser pairing submission and launching the
locally built fixture App pending action-time user confirmation. Neither action
was retried through an alternative path. These two UI gates remain pending.
Physical iOS/Android installation, IME/accessibility/performance, vendor push
delivery, production provider packaging and public deployment remain outside
this run. No VM, public relay or background service was provisioned.

## Follow-up after user confirmation — 2026-10-07

The user explicitly confirmed the two isolated UI actions. The checks below
supersede the pending browser/native gates recorded above. Test data roots were
`/tmp/pot-cli-debug` and `/tmp/pot-embedded-check`; neither contained configured
model credentials. The native fixture Helper only answers stopped-state status
requests and cannot start a VM. No real model request, VM or service was started.

### Browser pairing and recovery

- The installed release CLI served embedded assets at `localhost:17915`.
  A fresh CLI-generated code paired `Synthetic CLI Browser`; Server showed
  one authorized client/session and closed pairing immediately.
- IndexedDB inspection returned a private ECDSA P-256 CryptoKey with
  `extractable: false`. No key material was exported. The auth shell disappeared
  and GPUI initialized BrowserWebGpu with one canvas.
- Server was stopped and restarted. Browser automatically signed in with the
  same client ID and a new session ID, without entering another code.
- CLI revoked that synthetic client. Browser returned to the login shell with
  `client_revoked`; status was unauthenticated and snapshot access returned 401.
  The client remains revoked. Expected connection/401 console errors occurred
  during restart/revocation; graphics initialized successfully.
- Screenshots: [connected](../../output/playwright/standalone-hosting/pairing-connected.png),
  [revoked](../../output/playwright/standalone-hosting/pairing-revoked.png).

### Embedded App lifecycle

- The isolated App started with the final Rust/Swift bridge. General Settings
  loaded Server state and generated a five-minute pairing code in memory;
  closing pairing worked. AI Configuration and its Custom API form loaded
  without entering credentials or saving a provider. Sandbox showed the stopped
  fixture environment; no desktop-start action was performed.
- The App process owned both its restricted loopback integration listener and
  mobile listener. There was no `product/control.sock` in the App data root.
- Quit removed both listening ports and the synthetic Helper socket. Relaunch
  succeeded against the same root, demonstrating that shutdown released instance
  ownership. The final build showed the corrected owner-scope text and
  **Close pairing** label. It was then quit again.
- Screenshots: [General Settings](../../output/playwright/standalone-hosting/embedded-general.png),
  [configuration form](../../output/playwright/standalone-hosting/embedded-settings.png),
  [stopped Sandbox](../../output/playwright/standalone-hosting/embedded-sandbox.png).

The checks establish isolated boot, navigation, pairing management and lifecycle
behavior; they do not establish pixel parity across all native states, streaming
model interaction, production Helper/VM execution or physical-device acceptance.
The final EN/zh-CN scope text removes obsolete phone-code approval guidance, and
Close enrollment is relabeled Close pairing. App/Web release assets were rebuilt.
The dedicated browser and CLI host were closed after the run.
