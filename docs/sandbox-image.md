# Sandbox image

Updated: 2026-10-06. The bundled Linux ARM64 OCI image contains the execution service, system dependencies and optional desktop/browser. Agent CLIs, product Server/Runtime and provider configuration live on Host.

## Build and startup

`scripts/pot prepare` downloads checksum-verified kernel, noVNC 1.7.0 and Host Node 24.21.0. `build` compiles Host App, Server and Helper. `bundle` builds the guest image and installs pinned Host Codex 0.160.1 dependencies from npm locks, with lifecycle scripts disabled.

The image builder uses a separate Apple VM with no user workspace or credentials, pinned Alpine/system packages, and `/opt/pot` executor/desktop resources. The guest Rust binary is cross-compiled for Linux ARM64 musl. Build hashes include guest sources and that binary; identical inputs reuse the immutable image. Bundle contains image/vminit/kernel and Host binaries. VM networking requires Apple Virtualization permissions.

Helper starts Host Server immediately and imports/boots each Linux image only when requested. `/opt/pot/sandbox.sh` mounts cgroup v2 and starts the authenticated executor on port 17701. Shell/file subprocesses run as `potdesktop` with a minimal environment. Each owner/turn/environment scope uses a root-owned cgroup and a credential-free Rust tool runner. Live pipe sessions share that scope; cancellation and turn completion drain it, including detached descendants. The executor token stays on Host outside the mount and is injected into the root executor at startup, then removed from child environments. The guest ledger/output live in root-private `/run/pot-executor`, outside virtiofs. Host receipts remain the durable authority across VM recreation. Desktop starts separately through the managed environment action.

Each Sandbox mounts only `root/sandboxes/ID/workspace` at `/data`; files live beneath `/data/workspace`. No Host product database, CLI sessions, provider secrets or Helper socket are mounted. Artifacts survive VM shutdown. Temporary rootfs changes disappear on a new instance.

Resources and build caches are under `.local/d1/`; rebundle after source changes. The new acceptance entry is `scripts/tests/sandbox-runtime.py`, documented in [Development](development.md), with [dated results](validation/host-pot-agent-20261006.md).

## Desktop and fonts

Minimal Xfce uses `xfwm4`, `xfsettingsd`, `xfdesktop`, Thunar, Xfce Terminal, and a persistent bottom panel without login manager/screensaver/compositor. Warm-white Pot background, adw-gtk3 widgets, Adwaita icons; direct Browser/Files/Terminal launchers, window switching, and clock on the bottom panel, with the same apps in the context menu. Click Take over in Pot before interacting. Theme defaults migrate by version rather than overwriting user fonts/preferences at every startup.

The neutral desktop wallpaper uses a subdued terracotta Pot Companion watermark matching the application identity.

Desktop startup shows the desktop and panel directly, without launching Chromium or a welcome webpage. Browser launches on demand from the panel/context menu or the viewer's Open browser action, initially at `about:blank`, using the persistent profile and loopback-only debugging port. Desktop shutdown still waits for any manually opened browser to exit. Rebuild/rebundle the Sandbox image to apply this startup change to newly booted environments.

UTF-8 locale with Noto Sans, SC/TC/JP/KR Noto CJK glyphs, and emoji fallback covers simplified/traditional Chinese, Japanese, and Korean. Font rendering is not input-method support; CJK IMEs are not preinstalled and complete Unicode coverage is not promised.
