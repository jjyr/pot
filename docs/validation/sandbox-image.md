# Completed Sandbox image acceptance

Date: 2026-10-04, local Apple Silicon VM. See [Sandbox image](../sandbox-image.md) for implementation/size metrics. No real models called; existing request ledger stays 27.

- Optimized Swift Helper/ImageBuilder/ImageCheck builds pass; Python AST, shell syntax, `git diff --check` pass.
- Import App image into separate store/fresh `/data`; VM `networking: false`. Desktop/broker/Chromium CDP/Runtime/Pi 0.87.1 start without apk/npm installation; Chromium exit marker confirmed after SIGTERM.
- CJK font checks cover simplified/traditional Chinese, Japanese kana/kanji, Korean Hangul/Hanja. UI evidence confirms characters/emoji in terminal, not full Unicode/IME acceptance.
- Fluxbox/Xfce APK metadata: archives increase 21, 073, 898 bytes, installed size 52, 451, 059 bytes; do not compare base Alpine layer directly with full desktop image.

Local evidence: `.local/d1/xfce-check/`, `.local/d1/offline-image-check*/data/`; do not commit user data/log tokens/build artifacts.

Final image `pot.local/sandbox:989970b5d23191baf89f`: offline full check (import/unpack/services/normal exit) cold 10 seconds, reused-data startup 8 seconds. Fixed recursive chown failure from stale sockets. Final App confirms warm-white background/three context-menu apps/Thunar/terminal, CJK/emoji rendering; existing tasks/configuration readable.

Launcher fix: offline check asserts `xfce4-panel`. Actual App validates Browser close/reopen, Files workspace, Terminal, minimized browser restoration. Independent panel-version marker migrates old user directories without resetting themes.
