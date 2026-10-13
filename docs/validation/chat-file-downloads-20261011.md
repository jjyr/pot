# Chat file downloads — 2026-10-11

## Delivered behavior

Ordinary assistant Markdown can contain `pot-file://TASK_UUID/encoded/relative/path`
links. The client captures the assistant and Runtime mode when clicked. Reads bind
to the task's immutable Host/Sandbox execution target, never a local cache fallback.
Control relays joined-machine reads over the existing signed outward connection.
Completed tasks need no active execution lease to read their retained workspace.
No model-visible tool name, argument or responsibility changed.

Regular files are read in 192 KiB chunks, with a 1 GiB maximum. Relative paths are
validated and each path component is opened without following symlinks. Metadata
revisions detect changes between/during chunks. Worker and cross-assistant reads
are rejected. Removal/offline failures remain explicit. App saves through
NSSavePanel into a private temporary file, replacing the destination only after
successful completion. Web uses a cookie-authenticated binary attachment stream;
download URLs contain no credentials.

## Verification

- Protocol: 2 tests passed, including Unicode link paths and traversal rejection.
- Runtime: 163 tests passed, including binary chunks, changed files, symlinks,
  missing files and owner/worker/cross-assistant endpoint boundaries.
- Server: 24 unit tests passed, 1 existing ignored. Joined TLS-node file tests
  verified multiple chunks, no active lease, different owners, traversal, changed
  files and removed membership. Access 5 and TLS 1 integration tests passed.
- App: 22 tests and Swift client safety passed. UI: 13 tests passed.
- Final WASM/App/Server builds, Clippy and 806-key localization checks passed.
- Native UI acceptance used an isolated Pot Control Acceptance App and disposable
  Control/local roots. A synthetic assistant reply was inserted only into the
  fixture chat; no real model generated it. Clicking its visible link opened the
  native save panel. Saving to `/private/tmp/pot39-native-download.bin` produced
  exactly 409,600 bytes, identical to the task workspace source:
  SHA-256 `870130e6ddddd5d74acfa65ae6e060c0bdc135930cc55562c696737c6d046aee`.
  Cancelling the save panel returned to Chat without a partial file. Temporarily
  hiding the disposable source produced a visible File download failed alert
  identifying the missing file; the source was restored afterwards.
- HTTP acceptance of the standalone Web listener used a disposable login token,
  session Cookie, assistant-scoped metadata and the binary proxy route. The
  attachment stream had the same bytes/hash and UTF-8 Content-Disposition.

Screenshot: `/private/tmp/pot39-file-chat.png` (native Chat fixture).

## Browser follow-up

After the user manually opened the public shell in Chrome's agent profile, the
existing page rendered Control Assistant and the synthetic Chat download link.
Clicking that link produced a completed browser download event whose saved path
was `/Users/jjy/Downloads/pot39-download-fixture.bin`. Screenshot:
`/private/tmp/pot39-file-web-chat.jpg`. Browser interaction required no model call.
macOS denied reading that Downloads file, including outside the workspace sandbox,
so the browser-saved file's hash was not independently verified. The matching
App file and authenticated HTTP attachment hashes above remain separately verified.

## Limits

Chrome agent-profile automated navigation received `cross_site` for
the public shell; the follow-up used the page manually opened by the user.
A narrow top-level document-navigation exception allows public-shell
links while retaining API/download/frame/mutation boundaries; it does not resolve
the automated navigation failure. Browser clicking/completion, native clicking
and HTTP streaming were tested independently, with the browser-file hash limitation
above. No real model, VM Sandbox, remote desktop or remote plugin was
qualified here; joined file transport tests used separate TLS nodes on one host.
