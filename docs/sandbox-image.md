# Sandbox image

Updated: 2026-10-06. The separately distributed Linux ARM64 OCI image contains the execution service, system dependencies and optional desktop/browser. Agent CLIs, product Server/Runtime and provider configuration live on Host.

## Build and startup

`scripts/pot prepare` downloads checksum-verified kernel, noVNC 1.7.0 and Host Node 24.21.0. `build` compiles Host App, Server and Helper. `bundle` packages Host binaries and the Node runtime for JavaScript tools/plugins.

The image builder uses a separate Apple VM with no user workspace or credentials, pinned Alpine/system packages, and `/opt/pot` executor/desktop resources. The guest Rust binary is cross-compiled for Linux ARM64 musl. Build hashes include guest sources and that binary; identical inputs reuse the immutable image. The image builder is invoked separately with `mise run build:sandbox-image`. Client bundles contain Host binaries; kernel/vminit/image are separate checksum-pinned Release assets. See [Sandbox resource delivery](sandbox-resources.md). VM networking requires Apple Virtualization permissions.

Helper starts Host Server immediately and imports/boots each Linux image only when requested. `/opt/pot/sandbox.sh` mounts cgroup v2 and starts the authenticated executor on port 17701. Shell/file subprocesses run as `potdesktop` with a minimal environment. Each owner/Agent-epoch/Sandbox scope uses a root-owned cgroup and a credential-free Rust tool runner. Live pipe sessions share that scope; cancellation, failure and shutdown drain it, including detached descendants. Live pipe/PTY sessions close when their execution ends and expire when idle; empty runners retain shell initialization caches without execution leases. See [command execution](executor.md). The executor token stays on Host outside the mount and is injected into the root executor at startup, then removed from child environments. The guest ledger/output live in root-private `/run/pot-executor`, outside virtiofs. Host receipts remain the durable authority across VM recreation. Desktop starts separately through the Sandbox desktop action.

Each Sandbox mounts only `root/sandboxes/ID/workspace` at `/data`; files live beneath `/data/workspace`. No Host product database, CLI sessions, provider secrets or Helper socket are mounted. Artifacts survive VM shutdown. The shared environment retains its writable rootfs across stop/start. Explicit
rebuild resets installed software and shared toolchain directories while preserving
Task work files. See [execution lifecycle](executor.md#shared-machine-execution-environment).

Resources and build caches are under `.local/d1/`; rebuild/export the image and embedded catalog before building clients after guest source changes. The new acceptance entry is `scripts/tests/sandbox-runtime.py`, documented in [Development](development.md), with [dated results](validation/host-pot-agent-20261006.md).

## Desktop and fonts

Minimal Xfce uses `xfwm4`, `xfsettingsd`, `xfdesktop`, Thunar, Xfce Terminal, and a persistent bottom panel without login manager/screensaver/compositor. Warm-white Pot background, adw-gtk3 widgets, Adwaita icons; direct Browser/Files/Terminal launchers, window switching, and clock on the bottom panel, with the same apps in the context menu. Click Take over in Pot before interacting. Theme defaults migrate by version rather than overwriting user fonts/preferences at every startup.

The neutral desktop wallpaper uses a subdued ochre Pot Companion watermark matching the application identity.

Desktop startup shows the desktop and panel directly, without launching Chromium or a welcome webpage. Browser launches on demand from the panel/context menu or the viewer's Open browser action, initially at `about:blank`, using the persistent profile and loopback-only debugging port. Desktop shutdown still waits for any manually opened browser to exit. Rebuild/export the Sandbox image to apply this startup change to newly booted sandboxes.

UTF-8 locale with Noto Sans, SC/TC/JP/KR Noto CJK glyphs, and emoji fallback covers simplified/traditional Chinese, Japanese, and Korean. Font rendering is not input-method support; CJK IMEs are not preinstalled and complete Unicode coverage is not promised.

## Linux/macOS template layout

The [image/template design](sandbox-providers.md#platform-and-resource-details) separates source recipes, installed templates and writable instances. Maintained recipes live under `crates/sandbox-images/linux/default/` and `crates/sandbox-images/macos/xcode/`; macOS has a prepared-instance bootstrap recipe rather than an installable image (see [macOS enrollment](macos-sandbox.md)); common descriptor/catalog types belong to `pot-sandbox-images`, and each Provider validates its own recipe and image format. Linux uses OCI content; macOS uses a complete stopped VM bundle prepared from IPSW plus Xcode/bootstrap provisioning. A Dockerfile is not required by the current Linux OCI builder.

Linux recipes have moved from `image/` to `crates/sandbox-images/linux/default/`. `scripts/build-image` reads `template.toml`, fingerprints it with the recipe, and emits resolved version/digest metadata. `.local/d1/` holds developer build caches; end-user prepared resources use a private shared digest cache. Actual OCI layers, IPSWs and VM disks do not go into Git. Templates have pinned content identity and no credentials; project instances keep separate writable state.

## Daemonless Linux OCI build

`scripts/build-container-image` prepares the target executor and checksum-pinned
noVNC assets, then invokes `podman build` with
`crates/sandbox-images/linux/default/Dockerfile`. `install.sh` is the shared package/user/
asset installation recipe also used by the Apple image builder's `build.sh`.
The Dockerfile uses the same pinned Alpine base and emits an OCI image with the
executor bootstrap label. It builds ARM64 or AMD64; package availability for each
architecture is validated by a real build, not assumed from the ARM64 lockfile.
The Apple export/kernel/vminit pipeline is retained; it has not been replaced by
an unqualified OCI import path. Images contain no Server, model keys or Host Agent.

The default GUI image also includes pinned C/C++ build tools and Git, so user-level
Rust installations have a linker and common native build dependencies. Rustup
versions themselves remain installed in the shared development directory; a Task
can install a required version once and subsequent Tasks reuse it.
