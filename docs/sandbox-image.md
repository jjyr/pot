# Sandbox image

macOS and Linux use the same architecture-specific Debian 13 stable
(`trixie-slim`) OCI image with glibc through [Podman](sandbox-providers.md).
It contains the execution service, development dependencies and optional desktop.
Product Runtime, model credentials and provider configuration stay on Host.

## Recipe and contents

`resources/sandbox/linux/default/` owns the Dockerfile and installation recipe.
`base-image.json` pins the Debian digest and signed package snapshot;
`packages.lock` selects direct packages. The snapshot fixes dependency versions,
and `/opt/pot/installed-packages.txt` records the installed inventory. Debian
archive signatures remain verified. `desktop-assets.json` pins theme/font URLs
and hashes; Node and noVNC archives also have pinned checksums.

The image includes Bash, Git, C/C++ build tools and common native dependencies.
Its glibc userland supports conventional GNU/Linux Rustup/Node distributions;
individual tools can need extra packages. Official Node matches the guest
architecture. The Go executor is cross-compiled with CGO disabled; JavaScript
plugins and Python guest services retain their runtimes.

Build/export commands and immutable bundle pins belong to
[Development](development.md#sandbox-image-resources). Each architecture
requires a real build and qualification. Developer artifacts live in `.local/go/`;
OCI layers and VM disks do not belong in Git. Native macOS VMs use
[owner enrollment](macos-sandbox.md), separately from this Linux recipe.

## Runtime

Podman starts the verified image on request. `/opt/pot/sandbox.sh` mounts cgroup v2
and starts the authenticated executor on port 17701 under `tini -s`, which reaps
orphaned descendants inside the container's PID namespace. Tools run as
`potdesktop` with a minimal environment; the broker owns credentials and cleanup.
Browser profile removal/plugin reset require confirmed process cleanup.

The Provider mounts its workspace at `/data`, with Task files beneath
`/data/workspace`; additional mapping/Skill boundaries are defined in
[Providers](sandbox-providers.md#machine-and-mount-boundaries). Root-private
ledger/output live in `/run/pot-executor`; Host receipts remain the durable
authority across container recreation. Process scopes, sessions, retained
software/toolchains and rebuild behavior belong to [command execution](executor.md).

## Desktop and fonts

Minimal Xfce uses `xfwm4`, `xfsettingsd`, `xfdesktop`, Thunar, Xfce Terminal and a
persistent bottom panel without login manager, screensaver or compositor. Both
hosts keep the warm-white background, subdued ochre Pot Companion watermark,
adw-gtk3 widgets, Adwaita icons, fonts and launcher layout. Browser, Files and
Terminal launchers, window switching and clock stay on the bottom panel and in
the context menu. Click Take over before interacting. Versioned theme defaults
preserve owner changes to fonts/preferences.

Startup opens the desktop/panel directly. Browser opens on demand at `about:blank`
from the panel, context menu or viewer, using a persistent profile and loopback
only debugging port. Desktop shutdown waits for manually opened browsers.

UTF-8, Noto Sans, SC/TC/JP/KR Noto CJK glyphs and emoji fallback cover simplified/
traditional Chinese, Japanese and Korean. Fonts do not supply input methods;
CJK IMEs are not preinstalled and complete Unicode coverage is not promised.
