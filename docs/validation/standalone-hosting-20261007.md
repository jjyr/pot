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

## Browser observation

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

## Native observation and remaining gates

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
