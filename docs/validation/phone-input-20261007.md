# Physical iPhone input preparation (2026-10-07)

This is a prepared manual test, not physical-device acceptance. The owner will
use iPhone Safari over Tailscale. No iPhone is attached to the development host.

A fresh Linux Server instance and the qualified final Podman desktop image are
running on the authorized verification machine. Operational paths and listener
state belong to the machine inventory. Chromium inside the desktop has opened a
local test form with a text area, click counter and tall scroll target. A CDP
read confirmed its title, input element and ready event. Test events record
character counts and event metadata in page memory, without collecting text or
persisting a transcript. This form is temporary qualification material, not a
product feature.

Remote HTTP cannot enroll Safari: browser WebCrypto requires a secure context,
and the remote Tailscale IP is not the localhost exception. The existing Pot
listener also restricts HTTP origins to localhost. Tailscale certificate issuance
initially returned `your Tailscale account does not support getting TLS certs`.
The owner selected enabling HTTPS Certificates in the Tailscale admin DNS page.
The full phone entry point remains pending successful certificate issuance.

## Manual checks

1. Open Safari and configure browser access. Record iOS version, browser and keyboard.
2. In the main chat draft, compose `你好，Pot！abc 123 😀`, replace a Chinese word,
   delete characters, insert a newline, and switch conversations and back. Leave
   the draft unsent: this isolated instance has no configured model credentials.
3. Open Sandbox, choose Start desktop, and take over. Tap the click counter three
   times and try scrolling to the bottom. Record any offset, duplicate click or
   page-versus-desktop scrolling problem.
4. Tap the remote text area and check whether the soft keyboard appears. The
   current viewer embeds noVNC core without its full mobile keyboard UI; direct
   phone keyboard/IME entry is an open capability gap. This check must not be
   replaced by a claim that clipboard composition proves direct keyboard input.
5. Open the viewer Clipboard dialog, compose the same known text and check
   candidate selection, replacement, deletion and emoji. Sending fills the remote
   clipboard; pasting is a separate remote application action. Record whether
   Safari/desktop exposes a usable paste action.
6. Rotate the phone, return control, close and reopen the desktop tab. Verify
   layout, connection and control state. Optionally repeat as a Home Screen app;
   its independent storage context may need a new browser access configuration.

Report each check as pass/fail with expected and actual behavior. Main chat IME,
clipboard composition and direct remote keyboard input are separate outcomes.
Physical touch, IME, clipboard delivery and Home Screen acceptance remain pending.

## Preparation follow-up and shutdown

Tailscale certificate issuance subsequently succeeded. The tailnet-only HTTPS
listener returned HTTP 200 with TLS verification successful, using explicit DNS
resolution on the development client. The guest test form was restored after
Server restart. The then-current access, CLI and multi-client tests passed (17 tests);
those results predate the Token authentication redesign.

At the owner's request, the temporary Server was stopped before manual test
results were supplied. The browser listener and control socket were gone and no
qualification Podman containers remained. Physical iPhone acceptance is still
pending; prepared resources were retained for a later test.
