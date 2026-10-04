# pot remote desktop POC (P2)

Requires a reachable Linux amd64 Docker node and SSH authorization. Select the destination from `~/Workspace/Machines`; never commit connection secrets here. No Compose installation is needed.

```sh
export POT_REMOTE_SSH='user@your-test-node'
./poc/remote-desktop/poc doctor
./poc/remote-desktop/poc build
./poc/remote-desktop/poc run
./poc/remote-desktop/poc tunnel
```

Open the printed loopback URL in a browser. The SSH tunnel is the authenticated boundary. Only the container's noVNC port 6080 is published, on remote host loopback port 17680 (`POT_REMOTE_PORT`); raw VNC and CDP are not published. This is a trusted single-owner POC, not public WSS authentication.

`POT_POC_ROOT` defaults to repository `.local/poc`. `POT_REMOTE_PORT` / `POT_LOCAL_PORT` default to 17680. Build context lives under the remote SSH user's `deployments/pot-poc/remote-desktop`; all managed Docker resources are named `pot-p2-desktop` with a POC ownership label. Built image ID is recorded and used for launch rather than a mutable tag. Base image, apt snapshot and noVNC archive are fixed by source/checksum.

Lifecycle:

```sh
./poc/remote-desktop/poc status
./poc/remote-desktop/poc logs
./poc/remote-desktop/poc driver
./poc/remote-desktop/poc untunnel
./poc/remote-desktop/poc stop
# To recreate with the same workspace and browser profile:
./poc/remote-desktop/poc remove
./poc/remote-desktop/poc run
```

Stop/remove preserve both named volumes. The driver runs CDP only through `docker exec`, against the headed browser shown by VNC; it is a test driver, not the P3 takeover broker. Do not give this Docker access to an untrusted agent.

The browser runs as UID 1000, with 3 GiB RAM, 2 CPUs, 256 PIDs, 512 MiB private shm, dropped capabilities and no-new-privileges. The default is a pinned Moby profile with clone/setns/unshare/chroot allowed for the inner user-namespace sandbox. `POT_CHROMIUM_SECCOMP=0` reproduces the failing default-policy probe; it is not the normal launch mode. No `--no-sandbox`, privileged mode, host IPC/network, or host browser profile is used.

Validation and actual limits are recorded in `docs/validation/remote-docker-gui.md`. Startup alone is not evidence of remote input/IME or persistent-profile correctness.

For Chinese text, open noVNC's Clipboard panel, enter text there, then focus the remote textarea and press **Ctrl+V** (Linux). Reverse copy uses Ctrl+A/C and the same panel. Direct canvas Unicode input did not arrive in this experiment; macOS native IME composition is untested. Default local scaling is intentional: remote framebuffer resizing worked, but Chromium/fluxbox did not reliably fit the smaller desktop.

The launch script exclusively locks this POC's profile volume before removing stale Chromium hostname locks; it also clears private X99 locks left by container restart. Do not mount this profile in unrelated browser containers. Stop/remove retain saved browser data and workspace files; a saved test-page value persists only after clicking Save profile marker.
