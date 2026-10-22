# Desktop installation

Pot's native desktop hosts the shared UI, a local Control and Runtime, and Agent
Link connections in one application. macOS uses WKWebView; Linux uses GTK3 and
WebKitGTK 4.1; Windows uses the installed Microsoft WebView2 Runtime through
Wails v3. The Linux desktop is enabled explicitly with the
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

The existing Apple Silicon app requires macOS 26 or later. Build its independently
identified development bundle with `mise run build`, or use the production app
from the project's release channel. See [Development](development.md) and
[Updates](updates.md) for macOS packaging and signed updates. This Linux adaptation
does not change macOS window, tray, Keychain or notification behavior.

## Linux

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

The default data root is `~/.pot-go`. An independent instance can use
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
POT_APP_VERSION=0.1.0 POT_BUILD_REVISION=local \
  mise exec -- python scripts/build-desktop --platform linux-amd64 --output .local/go/dist
xvfb-run -a mise exec -- python scripts/smoke-desktop \
  --platform linux-amd64 --archive .local/go/dist/pot-desktop_0.1.0_linux_amd64.deb
```

Use `linux-arm64` on a native arm64 runner. Cross-compiling a CGO desktop requires
a matching C library, GTK and WebKit sysroot; this script intentionally requires
native builds. `POT_APP_VERSION` must be `major.minor.patch`; `POT_BUILD_REVISION`
uses the same linker contract as the CLI and macOS app. The default artifact
directory is `.local/go/dist`; `--output` accepts an explicit destination.

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

## Windows

The initial Windows package is an amd64 portable ZIP for Windows 10 version 1809
or later and Windows 11. Extract the **complete** `Pot` directory into a user-owned
location and launch `Pot.cmd`, or `bin\pot-app.exe`. Microsoft Edge WebView2
Evergreen Runtime must already be installed; download it from
[Microsoft](https://developer.microsoft.com/microsoft-edge/webview2/). The package
uses the system WebView2 installation and the Wails embedded loader. It does not
install a browser engine, drivers, services or administrator components. No
administrator privileges are needed to run Pot.

```powershell
Get-FileHash .\pot-desktop_VERSION_windows_amd64.zip -Algorithm SHA256
Expand-Archive .\pot-desktop_VERSION_windows_amd64.zip -DestinationPath .\Pot-install
& .\Pot-install\Pot\bin\pot-app.exe
```

Compare the SHA-256 value with the adjacent `.zip.sha256` before extraction. Paths
containing spaces work; resources are resolved from `bin\..\Resources`, never
from the shell's current directory. The ZIP includes the native app, CLI, pinned
Windows Node/npm/npx distribution, plugins, Skills, sandbox SDK assets and license
notices. Install Git and project toolchains separately. Browser tools need a
separate supported Chromium browser configured with `POT_BROWSER_EXECUTABLE`;
WebView2 is the application's UI engine, not the computer-use browser.

The default data root is `%USERPROFILE%\.pot-go`. To isolate another instance,
pass `-root C:\absolute\private-root`. Pot creates missing data roots with a
protected current-user/SYSTEM DACL and rejects an existing root that grants other
principals access. `chmod` is not used as proof of Windows privacy. Keep runtime
state on a local filesystem supporting Windows ACLs and file locks. A byte-range
lock prevents two local Runtimes from owning the same root. Local HTTP bridges use
current-user/SYSTEM named pipes, retaining the existing per-boot bearer token.
The browser engine's user data is stored under the selected root's `webview2`
directory. Closing the main window exits the local app; a tray icon is also
available. Notifications follow the user's Windows notification preferences and
may be suppressed by Focus Assist or system policy. Toast delivery and activation
require the Windows desktop shell and should be qualified on the target runner.

Windows secrets use Generic credentials in Windows Credential Manager, scoped to
the logged-on user, with target `pot.control.secrets/go/<Runtime ID>`. The database
contains only AES-GCM ciphertext. A missing credential with existing encrypted
secrets fails; it is never replaced automatically. An explicit `POT_SECRET_KEY_FILE`
still requires a 32-byte regular file with a private current-user/SYSTEM DACL.
Preserve the Windows user credential store alongside backups; copying SQLite or
the installation directory alone cannot restore encrypted credentials on another
account. Data roots and key sources are not portable between operating systems.

Execution defaults to Windows PowerShell 5.1 with `-NoProfile`; an installed
absolute `pwsh.exe` path can be selected in shell settings. `login:true` means
loading the selected PowerShell profile and requires the existing login policy.
POSIX shell paths and `cmd.exe` are not supported Windows execution shells. Shell
commands use PowerShell syntax; there is no implicit WSL, Git Bash or POSIX path
translation. User terminals also use PowerShell without persistent PSReadLine
history. ConPTY supports interactive input, resize and Ctrl+C. For commands with
pipes, `interrupt` terminates the Job Object; it does not emulate Unix SIGINT.

Every Windows command, retained Node plugin and user terminal enters a
kill-on-close Job Object before workload code executes. Descendants cannot opt
out of that Job. Cancellation terminates the Job and confirms zero active
processes before cleanup succeeds. The initial process starts suspended, is
assigned to the Job, and is resumed through `NtResumeProcess`. Admission failure
keeps it suspended and kills it. Embedders cannot disable this tracking by
omitting a Unix supervisor command. This controls process lifetime, not filesystem
or network authority.

**Protected execution is unavailable on Windows.** Such launches fail before
running a workload, including patches and sandboxed plugins. Pot preserves the
machine's default policy; it never silently converts Protected to Host. Local
chat, Agent Link and tasks remain local. A user can explicitly authorize Host
execution according to the machine policy, or select Trusted in machine settings.
Host execution has the account's privileges and is not an OS sandbox.

Windows portable packages use manual upgrades. Quit every process using the
installation, back up the data root and credential store, and replace the whole
`Pot` directory. No signed in-app updater metadata is installed. The archive is
unsigned; Windows reputation checks may warn before first launch. Removing the
installation leaves user state and credentials intact.

## Building and checking Windows packages

Use Go 1.27.2, Node 24.21.0 and Python 3.12 from `mise.toml`. A CGO compiler, Swift,
GTK and a Wails CLI installation are not needed. The script can cross-build the
Windows archive on Linux; only a native graphical Windows runner can qualify the
WebView, ConPTY, credential and Job Object behavior.

```powershell
$env:POT_APP_VERSION = "0.1.0"
$env:POT_BUILD_REVISION = "local"
python scripts/build-desktop --platform windows-amd64 --output .local/go/dist
python scripts/test-windows
python scripts/smoke-desktop --platform windows-amd64 --archive .local/go/dist/pot-desktop_0.1.0_windows_amd64.zip
```

The artifact contract is `pot-desktop_VERSION_windows_amd64.zip` and its adjacent
`.zip.sha256`, using the same version/revision linker inputs as Linux. The native
tests check shell authority, Protected refusal, current-user DACLs, root locks and
identity reuse, Credential Manager restart, private IPC, Job admission and detached
child cleanup, ConPTY and Node plugins. The package smoke extracts into a path
containing spaces, runs bundled Node/npm/CLI, starts the actual app with fresh
private state, waits for the WebView's native bridge callback, and reopens the
same Runtime identity after shutdown. Its own Job Object contains the app and
WebView2 children. Run it in a disposable Windows user session with WebView2;
notification registration is scoped to that test account. No model is configured
or called. Compilation alone does not qualify these native behaviors.
