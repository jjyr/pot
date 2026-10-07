# Podman Web desktop acceptance — 2026-10-07

## Delivered behavior

`LinuxPodmanProvider` implements desktop readiness, private RFB transport,
browser launch and metrics through the shared guest broker. The executor starts
the desktop lazily and supplies a separate private broker credential over its
authenticated channel. Both published container ports bind Host loopback; the
browser receives neither port nor credential. Existing Manager RFB parsing,
one-hour tickets, generation checks and exclusive input ownership apply.

The Web Sandbox page opens the existing noVNC viewer in a separate same-origin
tab. Paired clients can view, explicitly take/return control, use clipboard and
launch the container browser. Environment administration remains CLI-only.
Pinned noVNC 1.7.0 JS assets are bundled into the embedded-Web CLI, with the
existing source archive SHA-256 check. Native-only viewer menu items are hidden
in ordinary browsers. Leaving the viewer closes its ticket, preserving the
container and workspace.

Server binds each public desktop ticket to the login session that created it.
A different client or new login cannot reuse that ticket. Public desktop assets
require authentication. Exact configured Origin, restrictive route matching and
same-origin page policies apply. A server-only Runtime extension checks the
login on active WebSocket input and periodically while viewing; logout,
revocation, disable and expiry disconnect the stream and release held input.
Forwarding preserves the HTTP upgrade extension while discarding outer route
parameters. Desktop openings are serialized and bounded (16 tickets per login,
64 across the endpoint); expired login mappings and closed tickets are removed.

Podman metrics read its delegated cgroup CPU/memory data rather than host-wide
`/proc` totals. Uptime is measured from container PID 1. Filesystem quota/capacity
is reported unavailable for the container; workspace logical size remains
available. Disk quotas remain unsupported.

## Verification

- Complete backend suite: 296 passed, one existing paid-model test ignored.
- Five real-HTTP multi-client regressions passed. The desktop test uses a mock
  display stream, not Podman: client ticket isolation, cross-Origin rejection,
  authenticated assets, concurrent viewers/exclusive takeover, credential
  revocation and selective disconnect, logout and re-login rejection.
- Existing Runtime real-WebSocket view-only/control/Origin/revocation test
  passed. Backend Clippy with warnings denied and source/script lint passed.
- Executor lifecycle tests passed. Web/WASM, final Linux embedded-Web CLI and
  macOS CLI release archive built successfully.
- Owner-authorized Arch Linux/x86_64 verification used system Podman 6.1.3,
  existing cgroup v2/systemd delegation, pasta and isolated temporary storage.
  The final shared Dockerfile image built with cached package installation.
- An independently authenticated browser loaded the real XFCE/Xvnc desktop over the
  public Server gateway. It started in viewing mode, took control, opened a
  terminal using mouse input and entered a keyboard command. Container file
  `desktop-input.txt` contained `desktop-keyboard-ok`.
- Browser clipboard text was sent and pasted into the remote terminal;
  `desktop-clipboard.txt` contained `podman-clipboard-ok`. The menu launched
  Chromium, and return-control completed with the viewer in observation mode.
- Clicking **Start desktop** in the actual shared Web Sandbox page opened a new
  viewer tab, which reported Connected with an RFB canvas. Leaving the prior
  viewer preserved its running container.
- Final-image Sandbox Manager execution qualification passed commands, receipts,
  patches, cross-turn PTY, detached cancellation/reaping, persistence, two
  environments and shutdown. Desktop readiness/metrics additionally asserted
  the configured 2 GiB memory limit, 2 vCPU and container-relative uptime.

Screenshots and logs are local ignored artifacts under `output/playwright/` and
`.local/qualification/`. Machine operating notes belong in `~/Workspace/Machines`.
No Podman integration suite was added to CI. Temporary browser/Server/container
sessions were closed after verification; no permanent service was installed.

## Limits

Qualification covers one Arch Linux/x86_64 host and a desktop Chromium client.
Physical iOS/Android touch, IME/accessibility, latency/bandwidth matrices, external
HTTPS deployment, real models and the macOS prepared-VM desktop remain unqualified.
The gateway arbitrates connected viewers; independent processes already inside
the same guest can still manipulate its desktop. No stronger task-to-desktop
isolation or additional Agent desktop automation tools are claimed here.
