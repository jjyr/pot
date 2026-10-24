# Desktop installation

Pot's native desktop hosts the shared UI, a local Control and Runtime, and Agent
Link connections in one application. macOS uses WKWebView; Linux uses GTK3 and
WebKitGTK 4.1 through Wails v3. The Linux desktop is enabled explicitly with the
`desktop` Go build tag. Headless CLI builds do not require Wails or GTK.

```text
shared web/src UI
       | native Wails bindings
desktop Service ---- local Control ---- Agent Link peers
       |                    |
native windows        local Runtime / executor
                            |
                 Resources: Node, sandbox, plugins, Skills
```

## macOS

The Apple Silicon app requires macOS 26 or later. During the unsigned release
phase, the website and README offer **Homebrew installation only**:

```sh
brew tap jjyr/pot https://github.com/jjyr/pot.git
brew install --cask jjyr/pot/pot
```

The first tap needs the explicit Git URL because this repository is named `pot`.
The root `Casks/pot.rb` is disabled until the first macOS release is published and
its cask update PR is merged. See [cask maintenance](releases.md#homebrew-generation).
Homebrew installs `Pot.app` in Applications from the release ZIP and verifies its
SHA-256. The ZIP remains a required release asset while its direct download entry
is hidden; `pot.rb` is maintained in source and is not a Release asset.
Upgrade with `brew upgrade --cask jjyr/pot/pot` after the corresponding cask PR is
merged. Until a matching release exists, see [Development](development.md) to
build the independently identified development bundle with `mise run build`.

Read `MACOS-RELEASE.txt` in the selected release for its signing status.
`unsigned` releases are ad-hoc signed for executable integrity;
they have **no Developer ID signature or Apple notarization**. Ad-hoc signing does
not verify the developer's identity. `signed` releases use Developer ID signing
and Apple notarization.

macOS may block an unnotarized App downloaded from the internet. Only for a download
you trust from the official release, try opening it, then allow that App in
**System Settings → Privacy & Security** if macOS offers that option. Alternatively,
after verifying the download and placing the App in Applications, remove quarantine
from this App alone in Terminal:

```sh
xattr -dr com.apple.quarantine /Applications/Pot.app
```

This is a trust decision about this one App. Do not disable Gatekeeper globally.
Removing quarantine does not repair a damaged signature, a corrupt package or
missing dependencies; recheck the archive and report a launch failure if it
persists. Quit Pot before manually replacing the complete App for an upgrade;
back up its data first.

Homebrew preserves quarantine and does not guarantee bypassing Gatekeeper; the
same first-launch guidance applies to cask installations.

The macOS CLI archive uses the same release mode. When macOS blocks a trusted,
verified extracted CLI installation, apply any quarantine exception only to that
specific extracted `pot-darwin-arm64` directory, never to a parent containing
other applications. Pot's [signed update feed](updates.md) authenticates package
origin separately from Apple signing in both modes; it does not grant Gatekeeper
approval. Published downloads and successful native smoke are prerequisites for
release availability, not claims about packages that have not been published.

## Linux (Experimental)

The native Linux desktop `.deb` is experimental. This status applies only to the
new desktop package; Linux server CLI/Web UI and Docker keep their own support
status. The amd64 package has passed a local native build and WebView smoke on
Arch Linux. Native arm64, Wayland and installation through `apt` have not been
fully validated; notification click behavior and Protected sandbox execution also
need qualification on the target host. Upgrades require manual replacement of the
complete package. No Linux desktop package has been published yet.

The initial package format is a `.deb` for glibc-based Debian/Ubuntu systems,
available for amd64 and arm64 when built on a matching native runner. It installs
the desktop under `/opt/pot-desktop`, a launcher at `/usr/bin/pot-desktop`, and a
desktop entry and icon. It contains the CLI, pinned Node distribution including
npm/npx, sandbox SDK, built-in plugins and Skills, shared frontend assets and
third-party license notices. The package uses the system GTK3, WebKitGTK 4.1 and
D-Bus libraries; it does not bundle an alternate browser engine.

Install a downloaded artifact and its dependencies with:

```sh
sha256sum -c pot-desktop_VERSION_linux_amd64.deb.sha256
sudo apt install ./pot-desktop_VERSION_linux_amd64.deb
pot-desktop
```

Use the matching `arm64` filename on ARM systems. The executable's required glibc
version is recorded in the Debian dependency metadata. Build on the oldest
distribution you intend to support; copying a package built on a newer glibc host
does not make it compatible with older distributions.

The app needs a graphical X11 or Wayland session and a D-Bus session bus. Tray
availability depends on the desktop environment's StatusNotifier support. The
main window works without a tray; closing it exits Pot on Linux. The tray menu
can also quit the app. macOS continues running when its main window is closed.
Notifications use the session's freedesktop notification daemon and are reported
unavailable when that daemon is absent. There is no macOS-style Linux permission
prompt; notification delivery and click actions depend on that daemon.

The default data root is `~/.pot`. An independent instance can use
`pot-desktop -root /absolute/private/root`. Do not run the bundled CLI's `serve`
command against a root already owned by the desktop app. Linux stores its master
key at `~/.pot/secrets/master.key` with owner-only access; preserve that key along
with the complete data root. See [Secrets](secrets.md).

Protected execution uses the packaged SRT SDK and requires bubblewrap, socat and
host support for unprivileged namespaces/seccomp. A distribution or container
policy can block those facilities. A failed Protected launch remains a failure;
Pot never silently executes it on the host. Host execution still requires the
machine policy and approval rules described in [Permissions](permissions.md).
Install Git and project toolchains separately. Browser tools require a local
Chrome/Chromium installation. Plugin child processes use bundled Node; ordinary
project shells keep their own PATH.

Upgrade the complete `.deb` after quitting Pot and backing up its data root and
master key. Linux desktop packages do not participate in the signed in-app
updater. Remove the package with `sudo apt remove pot-desktop`; user data and keys
remain in the home directory.

## Building and checking Linux packages

Use the pinned Go, Node and Python versions from `mise.toml`. On Debian/Ubuntu,
install build and smoke prerequisites:

```sh
sudo apt install build-essential pkg-config binutils libgtk-3-dev \
  libwebkit2gtk-4.1-dev dbus-user-session bubblewrap socat xvfb xauth
mise install
POT_APP_VERSION=0.0.0 POT_BUILD_REVISION=local \
  mise exec -- python scripts/build-desktop --platform linux-amd64 --output .local/pot/dist
xvfb-run -a mise exec -- python scripts/smoke-desktop \
  --platform linux-amd64 --archive .local/pot/dist/pot-desktop_0.0.0_linux_amd64.deb
```

Use `linux-arm64` on a native arm64 runner. Cross-compiling a CGO desktop requires
a matching C library, GTK and WebKit sysroot; this script intentionally requires
native builds. `POT_APP_VERSION` must be `major.minor.patch`; `POT_BUILD_REVISION`
uses the same linker contract as the CLI and macOS app. The default artifact
directory is `.local/pot/dist`; `--output` accepts an explicit destination.

`build-desktop` produces `pot-desktop_VERSION_linux_ARCH.deb` and the adjacent
`.deb.sha256`. It uses binutils `ar` to write the standard Debian package format;
it never installs the package or publishes a release. Assets are located relative
to the executable under `../Resources`, independent of the working directory.

`smoke-desktop` extracts the actual package into a temporary path containing
spaces, verifies bundled resources and runs the relocated Node, npm and CLI.
It starts the production app with an empty private root, isolated home and D-Bus
session, disables model-directory background refreshes, and waits for the main
WebView's ready callback through the real Wails bridge. No model is configured or
called. It does not qualify a display server, window manager, notifications daemon
or sandbox implementation beyond the environment in which it runs. For executor
and sandbox qualification, use the separate suites in [Development](development.md).

## Windows status

Windows is currently unsupported. There is no Windows installation or release
channel.
