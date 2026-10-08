# Sandbox desktop sessions acceptance — 2026-10-07

## Implementation

`pot-sandbox` owns optional display capabilities, one-hour opaque tickets, boot binding, exclusive control, release acknowledgement and the bounded RFB client gate. Provider crates return private byte streams. Runtime authenticates owner requests; Server mounts the scoped page/status/control/WebSocket routes. Capabilities explicitly declare the currently supported `rfb-3.8` protocol. Native App opens that gateway after an explicit desktop request and closes tickets when switching environments. The pinned noVNC 1.7.0 resource is unpacked and bundled for Host serving. Guest URLs/tokens are absent from the page and public inventory.

Linux retains its broker/RFB gate, browser launch, metrics and clipboard. macOS provides an authenticated pinned-TLS HTTP upgrade to an explicitly configured Guest-loopback RFB service; it does not install a VNC server or qualify Apple's Screen Sharing authentication. Closing a display does not stop a VM or command task.

## Automated observations

- `mise run test`: complete configured backend suite passed (one existing ignored Runtime test). Follow-up targeted Manager/Runtime/Server tests passed after extracting desktop ownership into its own module.
- `mise run check`: Rust workspace, script/format and Swift checks passed. Existing Swift/App and cache warnings remain; no new compile errors.
- `cargo clippy --workspace --all-targets --locked ... -D warnings`: passed.
- `mise run test:i18n`: native/web localization passed; both Linux and shared Host viewer HTML participate in translation generation.
- Manager tests cover absent adapter, default observation, two-viewer exclusivity, waiting for key release, same-ticket duplicate connection denial, expiry, VM boot changes, stop revocation and persistence after viewer closure.
- A real loopback WebSocket test verifies owner authorization, Origin rejection, viewing-only input rejection, controlled input forwarding, unsupported clipboard rejection, key release and connection revocation.
- RFB parser tests cover fragmented packets across a control change, clipboard gating and unsupported message rejection. Bounds protect packet buffering; QEMU key events are tracked for release. Unsupported desktop resizing fails closed.
- `scripts/tests/macos-provider.py` uses real local TLS certificates and HTTP upgrade against a fake loopback RFB endpoint. Unauthorized and disabled desktop access reject; authenticated duplex forwarding passes. Existing Guest execution/receipt/cleanup regressions pass.
- A Linux Provider regression verifies that desktop readiness refreshes the Helper credential after delayed Guest broker publication.

Logs are under `.local/validation/desktop-*`; they include the complete test/check commands' output. Local binaries and generated assets are not tracked source artifacts.

## Real Linux VM qualification

The first two-VM run reached the optional desktop step after task/command scenarios passed, then failed because readiness cached an empty token before Guest broker startup. Provider now rereads private Helper state on each readiness probe. A regression test covers that sequence. The subsequent VM run uses fresh debug Server binaries, the signed Helper and existing Linux OCI image, with the new authenticated gateway/RFB handshake and control-return checks added to `scripts/tests/sandbox-runtime.py`.

The final follow-up adds a conservative error if a viewer disconnects while awaiting control-return acknowledgement; its Manager/Runtime tests pass. That disconnected-return branch is covered by automated protocol behavior rather than claimed as a real VM fault injection.

## Untested scope and limits

No prepared macOS VM was available. Actual macOS boot, logged-in desktop export, selected VNC server compatibility, sustained TLS screen traffic, TCC/input permissions and Xcode/UI-test behavior remain unqualified. The macOS adapter requires owner configuration; a successful fake transport test is not real Guest acceptance.

The native App bridge compiles, but this run does not claim interactive WKWebView/noVNC rendering acceptance or a full packaged App launch. Viewer input arbitration covers gateway sessions; it cannot stop independent Guest-local automation. Release acknowledgement establishes private-transport writes, not a universal OS input fence. Unknown remote release after transport loss is not reported as proven settlement. Worker/mobile desktop access remains disabled.
