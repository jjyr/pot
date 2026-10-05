# P3: Remote desktop and control in one window

> Old `poc/` sources are removed; commands below apply only to the linked historical commit.

> Historical acceptance record: versions, allowances, and UI reflect each section's date. See [Development plan](../development-plan.md) and [Documentation index](../README.md) for current capabilities/contracts.

Date: 2026-10-03. **Limited pass**. GPUI/WKWebView displays/operates a real remote Docker desktop; takeover, handback, tunnel recovery tested. Full IME/performance/final tray demo not counted as passed.

## Reproduction and fixed boundaries

See [README](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/integrated-desktop/README.md). Start P2's dedicated container/tunnel, then `poc/integrated-desktop/poc prepare`, `run`, `open` (GUI build: `poc/macos-shell/poc build`). Machines supplies connection settings; retain P2 image/TigerVNC/noVNC 1.6.0/GPUI 0.2.2, pinned requirements.txt. Environment: `~/Workspace/Machines/machines/devpc/pot-p2-readiness-20261003.md`.

Local broker listens on loopback 17681; random token in task's mode-0600 URL file. Do not share it. GUI exit leaves remote environment running; see README stop commands. No real web accounts logged in.

## Observed evidence

| Action | Status | Evidence |
|---|---|---|
| WKWebView same remote desktop / driver write | Pass | Native screenshot in execution record; native-human-input.json contains remote events/session |
| Native Take over / Driver write rejection | Pass | Human owns input UI; real protocol broker-acceptance.json |
| Chinese Clipboard -> canvas Ctrl+A/V -> handback read | Pass | Exact `P3 中文接管 123`; native-human-input.json/returned.json; paste, not IME |
| Tunnel loss/recovery/reconnect under human control | Pass | disconnected.json retains human; returned.json same text; no automatic reclaim |
| In-flight driver / RFB gate / modifier release | Pass | verify_broker.py real CDP/RFB checks unauthenticated 403, human-driver 409, in-flight completion, QEMU release/Fence; not fixture |
| Full P3 tray reopen sequence | Limited pass | P1 user confirmed reopen; P3 interrupted by menu-validation deadlock. Native UI works after fix, full tray sequence not repeated |
| IME / full mouse-scaling / weak-network latency | Untested | User narrowed scope; Chinese paste/protocol tests are not substitutes |

Small evidence: [evidence](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/integrated-desktop/evidence). Full native/broker logs in ignored `.local/poc/`; screenshots in tool record, no claimed recording.

## Findings and fixes

- RFB SetEncodings entries are 4 bytes; parser fixed. Python 3.9 async lock initializes during aiohttp startup.
- Native automation delivered WKWebView mousedown with `buttons=0`, Ctrl ctrlKey without modifier transitions. Page corrects inconsistent fields, synchronizes Ctrl through public noVNC sendKey, resets on control epochs. Real native clicks/keys remain the input, not DOM writes posing as human input.
- Return disables input, releases standard/QEMU keys/mouse, waits for real RFB Fence BlockBefore acknowledgment, reads page, then authorizes Agent. Broker Fence acknowledgments must be consumed; forwarding to noVNC caused disconnect, fixed/retested in same viewer.
- AppKit menu validation reentered GPUI mutex and deadlocked; sampled `NSMenu _enableItems -> validate_menu_item -> parking_lot`. Disable automatic enable in owned main/Edit/status menus; native access restored without expanding P1.

## Conclusion and boundaries

Retain GPUI/WKWebView + TigerVNC/noVNC; broker owner/epoch coordinates two cooperative input channels, restart defaults to no owner. P4 reuses viewer/broker. Stale disconnect error text may remain while authoritative owner/phase refreshes.

This is not malicious-Agent isolation: raw P2 tunnel/Docker/arbitrary shell-CDP bypass remains. Define permissions before product skeleton. User later confirmed input works without specifying method; do not upgrade to Chinese IME acceptance.
