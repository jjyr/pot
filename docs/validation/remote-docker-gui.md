# Remote Docker GUI: P2 minimum slice

> Old `poc/` sources are removed; commands apply only to the linked historical commit.

> Historical acceptance record: versions, allowances, UI reflect section dates. See [Development plan](../development-plan.md) / [Documentation index](../README.md) for current capabilities/contracts.

Date: 2026-10-03. **Limited pass**. Real Linux Docker -> SSH -> standard noVNC -> headed Chromium works end-to-end, not GPUI remote desktop/full P2 acceptance. P1 ended by user decision; P3/P4 untested when written, see later addendum.

## Reproduction

Machines owns machine selection/connection/deployment: `~/Workspace/Machines/machines/devpc/pot-p2-readiness-20261003.md`; no connection accounts here. [Remote desktop README](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/remote-desktop/README.md).

```sh
cd ~/Workspace/pot
export POT_REMOTE_SSH='<SSH destination selected from Machines>'
poc/remote-desktop/poc doctor
poc/remote-desktop/poc build
poc/remote-desktop/poc run
poc/remote-desktop/poc tunnel
```

Open `http://127.0.0.1:17680/vnc.html?autoconnect=1&resize=scale`. Experiment container/tunnel remain available. Check existing containers with `status`; image update/rebuild uses `remove` then `run`, preserving profile/workspace volumes.

Manual verification:

1. Type English in remote text box, click counter, observe result.
2. noVNC left toolbar -> Clipboard, enter Chinese; remote text box **Ctrl+V** (Linux shortcut). Reverse Ctrl+A/C to Clipboard panel. Panel and automatic system clipboard sync differ.
3. Save profile marker; `poc driver` reads same page. Close/reopen local page; content should persist.
4. `untunnel` disconnects; `tunnel` + refresh returns to same browser, not automatic reconnect guarantee.
5. `remove` / `run` + refresh retains saved text/session marker. Unsaved text is not guaranteed across browser restart.

Stop: `poc/remote-desktop/poc untunnel`, then `poc/remote-desktop/poc stop`. `remove` deletes container but retains volumes/network/image; no global prune. Persistent data deletion needs separate decision.

## Pinned versions and boundaries

- Debian base digest: `sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251`.
- Debian bookworm/security apt snapshot `20261001T000000Z`; full package list `/opt-packages.txt` in image.
- Chromium `154.0.8037.57-1~deb12u1`; TigerVNC `1.12.0+dfsg-8`; websockify `0.10.0+dfsg1-4+b1`.
- noVNC `1.6.0`, archive SHA256 `5066103959ef4e9b10f37e5a148627360dd8414e4cf8a7db92bdbd022e728aaa`.
- Final running image `sha256:f7167e4c5fe56a6a68342f0435a0835ded8599d9c8fae91438ae37108d80efee`; record/start by build ID, which may differ on rebuild.
- seccomp from Moby profiles `2ceae35d351c156cb5a8efc0fdc4a08cf94569d8`, adding clone/setns/unshare/chroot for inner Chromium namespaces; source/license retained.
- UID 1000, cap-drop ALL, no-new-privileges, 3 GiB RAM / 2 CPU / 256 PID / 512 MiB private shm; dedicated bridge/workspace/profile volumes. No host home/browser profile/Docker socket mounts.
- Host viewer loopback 17680; VNC 5900/CDP 9222/test page 8081 container loopback only. SSH authenticates/encrypts; noVNC unencrypted refers to inner WebSocket. Public WSS/multiuser auth/hostile-Agent isolation unimplemented.

## Observations and evidence

Isolated local headed Chrome with real Playwright CLI canvas clicks/keys/Clipboard UI; remote CDP only deterministic driver/readback. Not fixture/native IME acceptance. [evidence](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/remote-desktop/evidence).

| Item | Status | Facts / evidence |
|---|---|---|
| Remote desktop / same session | Pass | Driver writes multiline Chinese, screenshot displays; viewer appends `!`/Save, driver reads exact match. `driver-write.json`, `viewer-edited-saved.json`, `same-session.png` |
| Basic keys/mouse | Pass | Real canvas English/End/!/Ctrl+A-C-V/Save/counter. `viewer-keyboard.json`, `tiger-counter.json`. Corners/drag/held-key disconnect untested |
| Chinese paste | Pass | noVNC Clipboard -> Ctrl+V -> remote textarea exact `你好，Pot！中文 English 123`. `tiger-clipboard.json` |
| Reverse multiline Chinese copy | Pass | Remote Ctrl+A/C -> Clipboard exact `driver → viewer 中文\nsecond line`. `tiger-reverse-value.txt`, `same-session.png`; local system clipboard permissions untested |
| Direct Chinese input | Failed / IME untested | Playwright canvas Unicode not delivered, TigerVNC retest same; `tiger-unicode-direct.json`. Actual macOS pinyin composition/commit untested, paste is not equivalent |
| Viewer scale | Pass | Remote 1280x800 scaled local viewer usable; `restored-desktop.png` |
| Remote resize | Limited pass | noVNC resize/local 1000x700 yields xdpyinfo/page screen 1000x700, but Chromium innerWidth 1279 without reflow; missing fluxbox wallpaper utility. `remote-resize.json`, `resize-limitation.png`; scale remains default |
| Tunnel loss/recovery | Pass | SSH master close -> Disconnected; tunnel rebuild/refresh preserves session/text/events. `tunnel-disconnected.txt`, `tunnel-restored.json`; held-key/weak-network untested |
| Container restart / same-volume recreate | Pass | Final configuration restores session `0caac25c-c61f-46c8-a9c9-09286393027c`, saved Chinese/files. `container-{restart,recreate}.json`, `workspace-{restart,recreate}.txt` |
| Chromium sandbox | Pass | Final TigerVNC chrome://sandbox Namespace/PID-Network/Seccomp-BPF-TSYNC Yes. `tiger-sandbox.json`; no --no-sandbox |
| Performance / weak network | Untested | Idle roughly 283.5 MiB/0.61% CPU/129 PID only, not capacity/performance. No RTT/loss/p50-p95/bandwidth matrix |

## Findings and fixes

1. Docker default seccomp blocked Chromium namespaces. Old Playwright profile also caused node runc/libpathrs SIGSEGV before container start. Use pinned current Moby profile with explicit inner-namespace calls; preserve `default-seccomp-failure.txt`, `old-profile-runtime-failure.txt`.
2. Xvfb+x11vnc handled English but corrupted Chinese clipboard. Replace display server with TigerVNC; same real UI path reads Chinese exactly.
3. Recreate Chromium SingletonLock bound old hostname. Exclusively flock dedicated profile before clearing stale Singleton files to prevent concurrent POC profile use.
4. Restart retained `/tmp/.X99-lock` and blocked X. Entry clears private X99 residue/checks readiness; final restart/recreate pass.
5. Keep known resize limitations without expanding WM logic. Default scale usable; missing DBus logs do not affect test page, but service-dependent web features untested.

## Next step

Keep GPUI/WKWebView/noVNC with remote TigerVNC. P2 permits **P3: integrate standard viewer into WKWebView with minimum takeover/handback broker**. Explicit Chinese Clipboard fallback; confirm IME/resize separately. Current CDP driver is unrestricted, without P3 arbitration or arbitrary shell/CDP isolation. Swift Helper/local P4 remains separate unknown work.

## Later observation addendum

User confirmed input works without specifying method; this does not replace untested IME/failed automated Unicode. P3 completed minimum WKWebView/cooperative control and P4 local Helper/App; see their reports. Previous Next step records P2's historical state.
