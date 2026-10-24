# Install Pot with an agent

Give your installation assistant this prompt (the same prompt appears on the
[website](https://pot.jjylab.com/#start) and in the [README](../README.md#install)):

```text
Read https://github.com/jjyr/pot/blob/main/docs/install-with-agent.md and help me install and configure Pot on this computer.
```

This guide is for installing and configuring Pot with the user. It does not
change the instructions of assistants running inside Pot.

Let the user handle secrets in their own terminal, local UI or authorization
browser. The `access ... token` commands below are for the user to run locally
and enter into the UI; do not echo tokens into chat or capture them in tool
output. Never ask the user to paste an API key or token into the conversation.

## Choose the installation together

1. Inspect the OS, architecture, available tools and graphical session. Check for
   an existing Pot executable, running instance and data roots (normally `~/.pot`;
   server roots can be explicitly chosen). Ask which existing installation to use
   before replacing files or opening its data with a newer version. Never start
   two hosts against the same Runtime root. Back up the complete root and its
   secret master key with Pot stopped before an upgrade.
2. Establish whether the user wants a **Desktop app** or **Web UI**. For Web UI,
   choose a directly installed **Linux Server** or a **Docker** deployment; both
   provide browser access. Agent-assisted installation can help with either path.
   macOS desktop requires Apple Silicon and macOS 26+.
   Native Linux desktop is **Experimental**, with separate GTK/WebKit and display
   requirements. Linux server/Web UI works from source on glibc amd64/arm64;
   Docker uses a Linux Engine. Windows is currently unsupported. See
   [desktop requirements](desktop-installation.md) and [Docker](docker.md).
3. Visit <https://github.com/jjyr/pot/releases>. Select the latest stable,
   non-draft, non-prerelease version with an asset matching the actual OS and
   architecture. Read that version's release notes and installation documents
   (use the selected tag's docs when they differ from `main`). Verify any supplied
   checksums before installing. Do not invent asset URLs, a Homebrew cask, a curl
   installer, or a `latest` image. If access fails, report it rather than treating
   the failure as evidence that no releases exist.
4. At the time this guide was added, the repository was private, the release list was empty and no Pot container
   package was listed for `jjyr`; packaged desktop/CLI downloads and a registry
   image were pending. Recheck the live channels rather than assuming that state
   is permanent. If there is still no matching public artifact, explain the
   available official source build below. Select a reviewed tag or revision and
   record it; do not silently upgrade an existing checkout.

## Official download channels

During the unsigned release phase, the website and README offer macOS installation
through Homebrew only. Their direct download links remain available for
Experimental Linux desktop and Web UI / Server via
[Releases](https://github.com/jjyr/pot/releases). The release selector is intentional
until assets exist; match the selected version and architecture rather than
constructing a download URL. The
[release contract](releases.md) defines these filenames:

- macOS Apple Silicon: use this repository as the tap (the explicit URL is required
  on first use), then install:

  ```sh
  brew tap jjyr/pot https://github.com/jjyr/pot.git
  brew install --cask jjyr/pot/pot
  ```

  Upgrade with `brew upgrade --cask jjyr/pot/pot`. The cask is disabled until the
  first macOS release and its cask update PR are merged; see
  [cask maintenance](releases.md#homebrew-generation). The cask installs the
  release's `Pot-VERSION-arm64.zip`, which remains published with its checksums
  even while the App download entry is hidden. Read `MACOS-RELEASE.txt`:
  `unsigned` means ad-hoc integrity signing without Developer ID or Apple
  notarization. Follow the
  [macOS first-launch guide](desktop-installation.md#macos) only for trusted,
  checksum-verified downloads; do not disable Gatekeeper globally.
  Homebrew preserves quarantine and may require that same App-specific first-launch
  allowance. Use the same-repository tap commands above after verifying availability.
- Linux desktop: `pot-desktop_VERSION_linux_amd64.deb` is Experimental. Check
  its checksum, then `sudo apt install ./THE_DOWNLOADED_FILE.deb` and launch
  `pot-desktop`. Use the actual downloaded filename. Do not offer arm64 until a
  corresponding qualified asset exists.
- Web UI / Server: `pot-VERSION-linux-ARCH.tar.gz` (0.0.1: `pot-go-0.0.1-linux-ARCH.tar.gz`), with `ARCH` matching
  `amd64` or `arm64`. Verify the checksum and extract into a new directory. From
  the extracted `pot-linux-ARCH/bin` (`pot-go-linux-ARCH/bin` for 0.0.1), run
  `./pot serve -root "$HOME/.pot-node" -ui 127.0.0.1:17703`. In another terminal
  use `./pot access -root "$HOME/.pot-node" token` locally. See the server section
  below for browser access, stopping, backups and first verification.
- Web UI (Docker): the configured image is `ghcr.io/jjyr/pot`, with package channel
  <https://github.com/users/jjyr/packages/container/package/pot>. This page is not
  available until the package is published. Check actual tags there; publication
  uses `VERSION` without the `v` prefix and does not publish `latest`. Use
  [Docker instructions](docker.md) for the versioned image and persistent volumes.

These channels are published independently; a stable release may contain only
some assets. Use the source paths below when the requested package is still
unavailable.

## Build from source when a matching package is unavailable

Install Git and [mise](https://mise.jdx.dev/getting-started.html) using their
own official instructions, then use a new checkout:

```sh
git clone https://github.com/jjyr/pot.git
cd pot
mise install
```

Use the chosen revision's `mise.toml` and scripts. See [Development](development.md)
for toolchains; do not run the whole development/test workflow just to install.
If the repository is inaccessible, report that limitation and request access or
an official artifact; do not substitute an unofficial binary.

### Desktop app

On supported macOS:

```sh
mise run prepare
mise run build
mise run open
```

This creates the independent development bundle `.local/pot/PotPreview.app`,
not a production release. Launch that bundle again to start; use **Quit** to
stop it (closing the macOS window leaves Pot running). Rebuild with these same
commands after quitting and backing up before updates. For published
packages in either release mode, follow their release instructions and [Updates](updates.md).

On native Debian/Ubuntu amd64, build the Experimental Linux desktop package:

```sh
sudo apt install build-essential pkg-config binutils libgtk-3-dev \
  libwebkit2gtk-4.1-dev dbus-user-session bubblewrap socat xvfb xauth
POT_APP_VERSION=0.1.0 POT_BUILD_REVISION=local \
  mise exec -- python scripts/build-desktop --platform linux-amd64 --output .local/pot/dist
cd .local/pot/dist
sha256sum -c pot-desktop_0.1.0_linux_amd64.deb.sha256
sudo apt install ./pot-desktop_0.1.0_linux_amd64.deb
pot-desktop
```

`0.1.0` / `local` identify this local build, not an available release. Use
`linux-arm64` and the `arm64` filename on a native arm64 host. Build on a compatible
glibc distribution; check the package's dependency metadata. A graphical session
and D-Bus are required. Closing the Linux window exits Pot; quit before manually
replacing the complete package to update. Removing it with
`sudo apt remove pot-desktop` retains user data. Read
[Linux desktop limitations and prerequisites](desktop-installation.md#linux-experimental),
including unqualified arm64, Wayland, notification and sandbox behavior.

### Linux Server / Web UI

In the checkout on a glibc amd64/arm64 host:

```sh
mise run prepare
mise run build:go
.local/pot/bin/pot serve -root "$HOME/.pot-node" -ui 127.0.0.1:17703
```

Leave that terminal running. In another terminal in the same checkout:

```sh
.local/pot/bin/pot access -root "$HOME/.pot-node" token
.local/pot/bin/pot status -root "$HOME/.pot-node"
```

The first run initializes the chosen private root. Open
<http://127.0.0.1:17703> and enter the token locally. For a remote host, first run
`ssh -N -L 17703:127.0.0.1:17703 user@your-server` on the user's computer.
Use Ctrl-C to stop the foreground server, and the same `serve` command to restart.
For updates, stop it, back up the complete root, select the new revision, rebuild
resources and CLI, then restart against the same root. For packaged CLI archives,
services, TLS and the separate `web-ui-server`, use [Deployment](deployment.md).
Keep the default loopback listener unless setting up the documented secure proxy.

### Docker

From the checkout with Linux Docker Engine and Buildx installed:

```sh
docker buildx build --platform linux/amd64 --load \
  --build-arg POT_APP_VERSION=0.1.0 \
  --build-arg POT_BUILD_REVISION="$(git rev-parse HEAD)" \
  -t pot-server:local .
docker compose -f docker/compose.yaml up -d --no-build
docker compose -f docker/compose.yaml exec pot \
  pot access -browser-root /home/pot/.pot-web token
```

Choose `linux/arm64` on arm64. This builds a local image without publishing it.
Open <http://localhost:17703> (use this exact hostname, matching the default
origin). For remote access, forward port 17703 over SSH as above.

```sh
docker compose -f docker/compose.yaml ps
docker compose -f docker/compose.yaml logs --tail=100 pot
docker compose -f docker/compose.yaml exec pot pot status -root /home/pot/.pot
docker compose -f docker/compose.yaml down
docker compose -f docker/compose.yaml up -d --no-build
```

`down` stops the services and retains the volume; **do not use `down -v`**, which
deletes it. Back up the complete `/home/pot` volume with services stopped. It
contains `.pot`, `.pot-web` and workspaces; preserve the master key with the data.
Update by rebuilding/replacing the image and recreating with that same volume.
Read [Docker operations and isolation](docker.md): health checks do not qualify
Protected execution; run the documented sandbox probe on the actual host. Do not
silently weaken machine or container isolation to make tools run.

## First configuration and verification

1. Verify the process remains running and the UI opens. Confirm that you are
   using the intended instance/root. Default desktop data lives in `~/.pot`;
   this guide's server uses `~/.pot-node`. Linux credentials use `~/.pot/secrets/master.key` by default, including when
   the Runtime uses a custom root such as `~/.pot-node`; an explicit
   `POT_SECRET_KEY_FILE` overrides it. macOS uses Keychain.
   Preserve the complete root and applicable key material; see [Secrets](secrets.md).
2. Choose **Add assistant → API**, configure a model Provider and select its
   model. Follow [Model directory](model-directory.md) for provider setup. Have
   the user enter API keys in the local UI, never paste them into this chat or
   commit them into configuration. Agent Link to another Pot or Codex is also an
   option; see [Agent Link](agent-links.md) and [Codex setup](../integrations/codex/README.md).
3. Read [Credentials](credentials.md), especially GitHub browser authorization,
   and the [GitHub section of Plugins](plugins.md#github-notifications). If the
   user wants GitHub work, open **Settings → Third-party accounts → GitHub** and
   have them complete device authorization in their own browser. Review the
   requested scopes with them. Install/enable the required plugins through
   [Extensions](extensions.md); the bundled GitHub plugin provides notification
   tools. GitHub login is optional for running Pot.
4. Review GitHub notification settings with the user before enabling or changing
   subscriptions. The first connected bootstrap can create a default `mention`
   subscription for the first assistant. Explain it and let the user disable or
   delete it, or choose the receiving assistant and explicit filters in the
   GitHub account's expanded subscription list. Do not send messages, create
   remote work, or broaden subscriptions just to verify installation.
5. With the user's agreement, try a small chat on their chosen model (it may
   incur provider charges). Check model errors without exposing credentials.
   Restart Pot and confirm that the assistant and configuration persist. If a
   model call or platform-specific desktop behavior was not checked, say so.

Finish with the installed version/revision, launch and stop commands, UI URL,
data/backup location and any unresolved requirements. Link the relevant operating
guide for troubleshooting instead of claiming unperformed checks passed.
