# Sandbox image

The default Linux Sandbox uses Debian 13 stable (`trixie-slim`) with glibc. Its
separately distributed OCI image contains the execution service, development
dependencies and optional desktop/browser. The Apple image builder produces
Linux ARM64 resources; the rootless Podman builder targets ARM64 or AMD64.
Product Runtime, model credentials and provider configuration live on Host.

## Build and startup

`mise run prepare` prepares checksum-verified noVNC and Host Node runtime assets. `mise run build:go-app` compiles the Wails App, Go CLI/executor and Swift helpers, then packages the Host binaries and Node runtime for JavaScript tools/plugins. Kernel and OCI resources are prepared separately.

The image builder uses a separate Apple VM with no user workspace or credentials, a digest-pinned Debian base, snapshot-pinned Debian packages, and `/opt/pot` executor/desktop resources. The guest Go binary is cross-compiled for Linux ARM64 with CGO disabled. Build hashes include guest sources and that binary; identical inputs reuse the immutable image. The image builder is invoked separately with `mise run build:sandbox-image`. Client bundles contain Host binaries; kernel/vminit/image are separate checksum-pinned Release assets. See [Sandbox resource delivery](sandbox-resources.md). VM networking requires Apple Virtualization permissions.

Runtime starts independently of Sandbox preparation. Helper imports and boots each Linux image only when requested. `/opt/pot/sandbox.sh` mounts cgroup v2 and starts the authenticated executor on port 17701. Shell/file subprocesses run as `potdesktop` with a minimal environment. Each owner/Agent-epoch/Sandbox scope uses a root-owned cgroup and a credential-free Go tool runner. Live pipe sessions share that scope; cancellation, failure and shutdown drain it, including detached descendants. Live pipe/PTY sessions close when their execution ends and expire when idle; empty runners retain shell initialization caches without execution leases. See [command execution](executor.md). The executor token stays on Host outside the mount and is injected into the root executor at startup, then removed from child environments. The guest ledger/output live in root-private `/run/pot-executor`, outside virtiofs. Host receipts remain the durable authority across VM recreation. Desktop starts separately through the Sandbox desktop action.

Each Sandbox mounts only `root/execution/sandboxes/ID/workspace` at `/data`; files live beneath `/data/workspace`. No Host product database, CLI sessions, provider secrets or Helper socket are mounted. Artifacts survive VM shutdown. The shared environment retains its writable rootfs across stop/start. Explicit
rebuild resets installed software and shared toolchain directories while preserving
Task work files. See [execution lifecycle](executor.md#shared-machine-execution-environment).

Resources and build caches are under `.local/go/`; rebuild/export the image and embedded catalog before building clients after guest source changes. Build and operating commands are documented in [Development](development.md). Historical platform evidence remains under `docs/validation/`.

The distribution and its pinned package snapshot are image inputs. Updating a
client does not convert an existing Sandbox's retained writable filesystem to a
new distribution. Build and export new immutable resources, update the embedded
catalog, and explicitly rebuild the Sandbox to use them. Rebuild retains Task
work files while resetting the installed environment and shared toolchains.

## Desktop and fonts

Minimal Xfce uses `xfwm4`, `xfsettingsd`, `xfdesktop`, Thunar, Xfce Terminal, and a persistent bottom panel without login manager/screensaver/compositor. Warm-white Pot background, adw-gtk3 widgets, Adwaita icons; direct Browser/Files/Terminal launchers, window switching, and clock on the bottom panel, with the same apps in the context menu. Click Take over in Pot before interacting. Theme defaults migrate by version rather than overwriting user fonts/preferences at every startup.

The neutral desktop wallpaper uses a subdued ochre Pot Companion watermark matching the application identity.

Desktop startup shows the desktop and panel directly, without launching Chromium or a welcome webpage. Browser launches on demand from the panel/context menu or the viewer's Open browser action, initially at `about:blank`, using the persistent profile and loopback-only debugging port. Desktop shutdown still waits for any manually opened browser to exit. Rebuild/export the Sandbox image to apply this startup change to newly booted sandboxes.

UTF-8 locale with Noto Sans, SC/TC/JP/KR Noto CJK glyphs, and emoji fallback covers simplified/traditional Chinese, Japanese, and Korean. Font rendering is not input-method support; CJK IMEs are not preinstalled and complete Unicode coverage is not promised.

## Linux/macOS template layout

The [image/template design](sandbox-providers.md#platform-and-resource-details) separates source recipes, installed templates and writable instances. Maintained recipes live under `resources/sandbox/linux/default/` and `resources/sandbox/macos/xcode/`; macOS has a prepared-instance bootstrap recipe rather than an installable image (see [macOS enrollment](macos-sandbox.md)); common descriptor/catalog types belong to `internal/sandboxresources`, and each Provider validates its own recipe and image format. Linux uses OCI content; macOS uses a complete stopped VM bundle prepared from IPSW plus Xcode/bootstrap provisioning. The Podman build uses the shared Dockerfile and install recipe; the Apple builder uses the same guest assets.

Linux recipes have moved from `image/` to `resources/sandbox/linux/default/`. `scripts/build-go-image` reads `template.toml`, fingerprints it with the recipe, and emits resolved version/digest metadata. `.local/go/` holds developer build caches; end-user prepared resources use a private shared digest cache. Actual OCI layers, IPSWs and VM disks do not go into Git. Templates have pinned content identity and no credentials; project instances keep separate writable state.

## Daemonless Linux OCI build

`scripts/build-go-container-image` prepares the target executor and checksum-pinned
noVNC assets, then invokes `podman build` with
`resources/sandbox/linux/default/Dockerfile`. `install.sh` is the shared package/user/
asset installation recipe also used by the Apple image builder's `build.sh`.
The Dockerfile and Apple builder read the same
`resources/sandbox/linux/default/base-image.json` descriptor. It fixes the Debian
base digest and package snapshot; `packages.lock` selects the required top-level
Debian packages. The snapshot fixes their dependency versions, while the installed
image records the resolved package/version inventory in
`/opt/pot/installed-packages.txt`. Snapshot packages retain Debian archive
signature verification. The shared `desktop-assets.json` descriptor pins the
additional desktop theme and font archives by URL and SHA-256; both image build
paths consume these same inputs. The OCI image carries the executor bootstrap
label. Each architecture requires its own real build; source preparation alone
does not establish package availability or a bootable image.
The Apple export/kernel/vminit pipeline is retained; it has not been replaced by
an unqualified OCI import path. Images contain no Server, model keys or Host Agent.

The default image includes Bash, Git, C/C++ build tools and common native build
dependencies. Debian's glibc userland supports conventional GNU/Linux tool
distributions, including user-installed Rustup toolchains and Node native
extensions. This does not guarantee that every third-party binary or npm module
will run without additional dependencies. The image also includes the same
checksum-pinned official Node runtime used by packaging, selected for the guest
architecture; the executor and JavaScript capability checks use its stable
`node` entrypoint. Node and noVNC inputs participate in the image fingerprint.

Rustup versions themselves remain installed in the shared development directory; a
Task can install a required version once and subsequent Tasks reuse it. Debian
changes neither execution ownership nor process isolation: tools still run as
`potdesktop`, while the broker owns credentials and cgroup cleanup. The Go
executor remains cross-compiled with CGO disabled.
