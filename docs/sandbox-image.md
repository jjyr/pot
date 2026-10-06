# Sandbox image

Sandbox starts from a completed Linux ARM64 OCI image bundled in App. Desktop, fonts, Chromium, noVNC, Pi, Codex, Grok, and Pot Runtime are installed at build time. Startup neither runs `apk add` / `npm ci` nor pulls the Alpine base. Kernel/vminit are bundled too.

## Build and startup

See [Development guide](development.md) for entry points. `scripts/pot bundle` invokes `scripts/build-image`:

1. In a separate build VM, use the pinned Alpine digest, install system packages from `image/packages.lock`, and Pi/official Codex CLI (including native platform binaries) from npm locks.
2. Write `/opt/pot` and `/opt/pi`, export rootfs, and generate the OCI image plus `sandbox-image.json`.
3. Bundle image/vminit/kernel in App. A build-input hash identifies the local image; identical inputs reuse caches, with separate APK/npm download caches.
4. Helper imports the local image initially, creates the VM, and runs `/opt/pot/desktop.sh`. Initial import/unpack has a cost but does not depend on online package/npm sources.

Build requires networking and Apple Virtualization permissions; image startup is offline, while models/web browsing need networking. The build VM mounts no user workspace/credentials. User configuration, chat databases, browser profiles, and files mount separately at `/data`; image upgrades neither bundle nor clear them. Software temporarily installed in rootfs does not persist into a newly created VM; permanent dependencies belong in the image.

Artifacts live in `.local/d1/resources/`; caches/build evidence in `.local/d1/image-build/`, outside Git. Rebundle and restart App after an image update.

## Desktop and fonts

Minimal Xfce uses `xfwm4`, `xfsettingsd`, `xfdesktop`, Thunar, Xfce Terminal, and a persistent bottom panel without login manager/screensaver/compositor. Warm-white Pot background, adw-gtk3 widgets, Adwaita icons; direct Browser/Files/Terminal launchers, window switching, and clock on the bottom panel, with the same apps in the context menu. Click Take over in Pot before interacting. Theme defaults migrate by version rather than overwriting user fonts/preferences at every startup.

UTF-8 locale with Noto Sans, SC/TC/JP/KR Noto CJK glyphs, and emoji fallback covers simplified/traditional Chinese, Japanese, and Korean. Font rendering is not input-method support; CJK IMEs are not preinstalled and complete Unicode coverage is not promised.

## Size comparison (2026-10-04, ARM64, initial version without panel)

| Metric | Old Fluxbox | New Xfce | Increase |
|---|---:|---:|---:|
| APK archive total | 415.60 MiB | 435.70 MiB | 20.10 MiB |
| APK installed total | 1, 018.06 MiB | 1, 068.08 MiB | 50.02 MiB |
| System package count | 314 | 350 | 36 |

Compare the old lockfile with new image `/lib/apk/db/installed` metadata; both include CJK, counted once. Archive totals do not represent whole OCI compression.

New completed image compressed layer: **656, 413, 994 bytes (626.00 MiB)**; rootfs tar: **1, 758, 677, 504 bytes (1.64 GiB)**. The old approach downloaded only a roughly 3.93 MiB Alpine base before installing the entire environment; base size is not total desktop size. No equivalent-content old completed OCI comparison exists here.

## Offline acceptance

```sh
codesign --force --sign - --entitlements helper/virtualization.entitlements .local/d1/helper-build/release/PotImageCheck
.local/d1/helper-build/release/PotImageCheck .local/d1/offline-image-check .local/d1/resources scripts/tests
```

`PotImageCheck` imports the local image into a fresh image store/data directory and starts a VM with `networking: false`. It checks broker, Chromium CDP, Runtime, Pi version, desktop processes, four-language sample glyphs, and normal browser exit. Results are in validation `data/offline-result.json`; no model calls. Use a fresh validation directory for each cold run.

Later usability fix: persistent bottom launcher/window list adds 4 pinned packages. Current image `pot.local/sandbox:b637617123a0ce749783` has a 657, 315, 671-byte compressed layer (626.87 MiB). The table retains the initial no-panel acceptance scope.

Antigravity and Claude CLI are no longer downloaded or installed in newly built images. Claude API remains available through the generic provider gateway. Older running VMs need the rebuilt image to remove previously bundled binaries; persistent user files are preserved.

Grok uses the official `@xai-official/grok` 1.0.46 distribution and its pinned Linux ARM64 platform package. Build-time Node Brotli decompression writes the static native binary, then `grok` links directly to it. npm lifecycle scripts and the vendor launcher/updater are not run. Native authorization and session files stay in the persistent guest HOME; no host credentials are imported.
