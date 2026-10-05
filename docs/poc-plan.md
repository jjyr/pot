# Historical POC index

P0-P4 experiments from 2026-10-03; source directory removed. See [Development plan](development-plan.md) for later product state. This is not a current backlog. Original staged plans remain in Git; links below target the fixed pre-removal commit and historical commands require that checkout.

## 10. Execution records

| Phase | Conclusion | Tested | Untested / failed | Report |
|---|---|---|---|---|
| P0 | Limited pass | doctor/build/run/status/stop, pinned dependencies, task roots, embedded resources; default-directory build/run; 29 baseline fixture regressions | Fresh machine/cold registry | [macOS shell](validation/macos-shell.md) |
| P1 | Limited pass | User-confirmed real tray reopen; GPUI/WebView focus, pinyin commit/cancel, navigation keys, single-screen 2x scaling, menu Quit; key routing/firstResponder/Edit menu/repeated signing fixes | Final-build 50 tray cycles, resource trends, multiscreen, sleep/wake, full IME/shortcut matrix | [macOS shell](validation/macos-shell.md) |
| P2 | Limited pass | Real remote Docker + TigerVNC/noVNC: basic keys/mouse, bidirectional Chinese clipboard, same-session driver, SSH reconnect, same-volume restart/recreate, Chromium sandbox | Chinese direct input failed, native IME untested; limited remote resize; weak-network matrix | [Remote Docker GUI](validation/remote-docker-gui.md) |
| P3 | Limited pass | Real WKWebView -> remote Docker, takeover/handback, Chinese clipboard, tunnel-disconnect ownership, real RFB/CDP arbitration | Full post-fix tray demo, complete IME/weak-network matrix, shell/CDP bypass isolation | [Vertical integration](validation/integrated-desktop.md) |
| P4 | Limited pass | Owned Swift Helper, relocated App standalone run, real local desktop/shared control, cancellation/guest/Helper/GUI crash recovery, Quit cleanup | **Chromium clean exit failed** (profile Crashed); local chrome://sandbox, distribution, cold install, full interrupted-download matrix | [Local Helper](validation/native-helper-bundle.md) |


Gaps reflect observations then, not necessarily current failures. D1 fixed Chromium normal exit; later Pi/task evidence is in [Pi MVP](validation/pi-mvp.md) and [Multitask](validation/multitask.md).

## 11. Next phase

[Development plan](development-plan.md) owns scope. User ended minimum GPUI/tray validation; do not reinstate 50 cycles/multiscreen/full IME as prerequisites. Regress concrete issues. Validate remote IME/weak networks/full isolation before corresponding promises.

## Baseline and reproduction

- [Experiment code and phase entry points](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/README.md)
- [Original baseline](validation/baseline-report.md), [reproduction](validation/baseline-reproduction.md), [revalidation notes](validation/baseline-verification-notes.md)
- [Original evidence](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/baseline/evidence), [note sources](source-manifest.json)

Historical fixtures/clipboard/handshakes each have limited coverage, not full model/native UI/IME acceptance. Machine connection/deployment information belongs in `~/Workspace/Machines`.
