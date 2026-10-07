# Native multi-Server acceptance — 2026-10-07

Branch: `codex/multi-server-client`. Rebased onto `origin/main` at
`d0cb6fb41b7df925b5d03db272ab1269f92b5715` before implementation. The repository
has no separate upstream remote. Feature contract: [Multi-Server client](../multi-server-client.md).

## Automated verification

- `mise run check` passed formatting/script lint, locked workspace Rust checking
  and the Swift Helper release build.
- `mise run clippy` passed for every workspace target with warnings denied.
- `mise run test` passed the full backend suite; the existing model-call test
  remains ignored. No paid model calls were made.
- `mise run test:app` passed all 18 tests. Two real HTTP access listeners verify
  independent pairing and sessions, captured write destination and request UUID,
  settings routing, signed login recovery, identity mismatch rejection and
  revocation isolation. Metadata is mode 0600 and contains no private keys,
  sessions or internal bearer credentials.
- `mise run test:ui` and `mise run test:i18n` passed (763 translation keys).
- `cargo build --locked -p pot-app` passed, including native Swift compilation.

## Native UI verification

Used an isolated macOS App bundle (`dev.pot.multi-server-validation`) and roots
under `/tmp/pot-multi-native-validation` and `/tmp/pot-multi-remote-validation`.
The second Server was a real standalone CLI instance on localhost:18801. Native
UI actions used the accessibility interface; production App data was untouched.

Verified:

1. Settings → Servers exposes add, edit, pair again and remove. The toolbar
   picker switches saved connections.
2. A one-use code paired the second Server successfully and saved credentials
   in Keychain. Restarting the App restored the connection with signed login.
3. Saving remote concurrency 6 created the limits file only on the remote root.
   An unsaved concurrency draft of 7 survived switching away and back.
4. The remote chat draft survived switching to local and back, while the local
   conversation did not display it.
5. Disabling the remote access endpoint displayed a disconnected state and
   disabled Send while retaining the draft. Re-enabling restored Send
   automatically with the same draft.
6. Editing the display name saved successfully. Removing the test connection
   returned to local and left the saved connection list empty.

Switching to local initially exposed a crash when the Helper process had never
launched. Guarding `terminationStatus` behind a launched-process check fixed it;
the switch and draft-isolation scenarios were repeated successfully. The test
connection was removed and the validation App and standalone Server stopped.

The synthetic localhost provider was used solely to enable the chat editor;
no inference request was submitted and no provider credentials were used.

## Qualification limits

Localhost HTTP exercises the development transport. A real remote HTTPS
deployment, remote VM desktop display and provider device-account login were
not exercised end to end. Desktop gateway/session behavior has regression
coverage, but this report does not qualify a real remote VM. Remote workspace
files stay on the destination Server; artifact download/synchronization and
browser multi-origin switching are outside this increment.
