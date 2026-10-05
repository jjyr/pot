# Docker GUI and embeddable runtimes research

> Historical research: versions/current refer to review date. [Development plan](../development-plan.md) defines current implementation.

Compiled: 2026-10-02

## Summary

- Docker has no desktop; GUI comes from display server/apps/transport or host display.
- Independent virtual display per container with noVNC/Selkies fits remote/multi-instance use.
- Parallel software-rendered GUIs usually no privileged; browser CPU/memory/encoding limit capacity.
- External runtime control/library linking/single-file environment are distinct costs.
- Linux libcrun/libcontainer/Bubblewrap; macOSARM64 libkrun; Apple silicon/macOS26 Containerization candidates.

## 1. Size with UI

### 1.1 Define metrics

| Metric | Meaning | Caveat |
|---|---|---|
| Download | Compressed registry layers | Architecture/version/compression matters |
| Logical image | Sum uncompressed layers | Shared layers not additive physical disk |
| Container growth | Writable/cache/profile | Multiple containers do not copy base each time |
| Runtime memory | Apps/display/encoder | Less shareable than image layers |
| Platform overhead | Docker Desktop VM etc. | Mac/Windows Linux virtualization |

### 1.2 Checked base images

README Shields.io image-size badges returned:

| Tag | Badge image size |
|---|---:|
| `jlesage/baseimage-gui:alpine-3.22-v4` | 66.3 MiB |
| `jlesage/baseimage-gui:ubuntu-24.04-v4` | 92.2 MiB |

Sources: [README](https://github.com/jlesage/docker-baseimage-gui), [Alpine badge](https://img.shields.io/docker/image-size/jlesage/baseimage-gui/alpine-3.22-v4), [Ubuntu badge](https://img.shields.io/docker/image-size/jlesage/baseimage-gui/ubuntu-24.04-v4).

**Dynamic third-party badges, not local unpack measurements or Chrome-inclusive.** Not full browser image; Hub API failed, no architecture/digest/compressed-layer cross-check.

### 1.3 Planning orders, not measurements

| Approach | Order |
|---|---|
| Minimal OS/virtual display/light app | Potential hundreds MB unpacked, dependencies vary |
| Chromium/fonts/display | Hundreds MB to >1GB unpacked budget |
| Full XFCE/KDE/browser/tools | GB-scale, possibly several |

Engineering estimates, not version guarantees. Try0.5-2GiB/browser; pages/tabs/video/resolution may exceed.

Chrome-only needs no full desktop; headless removes display/remote layers, not browser.

Local measurement:

```bash
docker image inspect <image> --format '{{.Size}}'
docker system df -v
docker ps --size
docker stats --no-stream
```

Separate logical/shared disk/writable/memory; volumes/cache/logs/Desktop VM disk separately.

## 2. Concurrent GUI containers / permissions

**Yes: separate display/profile/persistence per container.**

### 2.1 Display permissions

| Mode | Needed | Risk |
|---|---|---|
| Virtual display/noVNC-Selkies/software | Normal container/ports | Authenticate desktop, no host display socket |
| Host X11 | Socket/DISPLAY/Xauthority | Weak same-server isolation, no global xhost+ |
| Host Wayland | Compositor/socket/UID/runtime dir | Host graphical interface exposed, app support varies |
| Linux GPU | Render nodes/groups, NVIDIA toolkit/driver | Device surface/compatibility |

GUI usually no privileged/SYS_ADMIN/host network/Docker socket.

Three permissions:

1. **Docker management**: rootful socket/group roughly host-root.
2. **Guest user**: normal browser user; container root differs from host root.
3. **Browser sandbox**: namespace/seccomp permit inner sandbox, not disable it.

Rootless reduces host privilege but needs user namespaces/UID-GID; test devices/network/cgroups on target.

### 2.2 Isolation per instance

- Same internal ports, different host e.g.loopback5801/5802->5800.
- No untrusted cross-user profile/download/cache/credential sharing.
- CPU/memory/PID caps prevent one-page exhaustion.
- Chromium shm default64MiB; try512m/1g, capacity not immediate allocation.
- Playwright host IPC recommendation weakens isolation; prefer private expanded shm/test.
- Do not broadly privileged/unconfined/no-sandbox to fix startup.

## 3. Direct UI access

Three meanings:

### A. Browser desktop/application

```text
Container app -> virtual display -> VNC/WebSocket -> noVNC -> browser
```

noVNC client, not server; VNC plus websockify when no native WebSocket.

Selkies alternative; Webtop HTTPS host3001, jlesage HTTP5800/VNC5900.

**Xvfb alone provides no accessible UI**; needs screenshot/remote service.

### B. Container windows on host

Linux X11/Wayland/Xpra single apps, more auth/security/compatibility.

Mac Linux cannot create native AppKit; remote browser/VNC/XQuartz remains Linux GUI.

### C. Embed in own App

noVNC WebView/JS connection-scale-input; iframe depends auth/CSP/frame/source, streaming needs codecs/HTTPS/WebRTC validation.

**Remote pixels/input, not native Linux widgets.**

Loopback/SSH/authenticated HTTPS, WebSocket session authority; no raw VNC/CDP/desktop.

## 4. Chrome/browser execution

**Yes**, three uses:

| Need | Approach |
|---|---|
| Navigate/screenshots/DOM/download | Headless + Playwright/Puppeteer/Selenium/CDP |
| Visible/manual browser | Headed + virtual display/noVNC-Selkies |
| Automation with takeover | Same headed browser automation/display |

CDP controls/debugs, not remote desktop or arbitrary GUI control.

### Compatibility / security

- Root Playwright loses Chromium sandbox; untrusted pages normal user/proper seccomp.
- Alpine musl not universal packages; Playwright Firefox/WebKit unsupported, validate distro Chromium.
- Official Linux Chrome commonly amd64; ARM64 verify Chromium/Playwright rather than assume parity.
- amd64 emulation on Apple silicon extra performance/sandbox-graphics risks.
- Test fonts/IME/clipboard/audio/camera/WebGL/DRM/uploads-downloads per product.
- Linux GPU not automatic Mac GPU; VM/runtime devices/drivers determine.

## 5. Docker alternatives embedded in binary

### 5.1 What to eliminate

- **Daemon install**: daemonless OCI/runtime library.
- **Programmatic environments**: link libraries/control sidecar.
- **One file/no resources**: browser/fonts/libs/rootfs/kernel remain, bundle or download/extract.

### 5.2 Comparison

| Approach | Platform/integration | GUI/Chrome | Tradeoff |
|---|---|---|---|
| crun/libcrun | Linux C/shared FFI | Own rootfs/display | Daemonless library; own images/network/storage/lifecycle |
| youki libcontainer | Linux Rust library | Own GUI stack | Rust fit, namespace-cgroup-seccomp/API validation |
| Bubblewrap | Linux subprocess | Installed GUI isolation | No full image/not Engine or fixed policy, host deps |
| Podman | Linux CLI/API, MacVM | Yes | Daemonless sidecar, not tiny linked library |
| libkrun | KVM/HVF ARM64, C/dylib | Linux userspace, test devices/display | Lightweight linked VM, own kernel-firmware/rootfs/UI |
| Apple Containerization | Swift/Apple silicon macOS26 | GUI in networked VM | Per-container VM, resources/kernel/rootfs/signing |
| Firecracker/Cloud Hypervisor | Linux KVM sidecar | Software/network principle | Stronger tenant boundary, not turnkey GUI/single-bin |

Can host processes, not built-in desktop/image-manager/Chrome turnkey promise.

OCI shares host kernel; microVM adds boundary, security still devices/network/mounts/control.

## Selection advice

### Web automation only

Headless Playwright, no desktop. Context profiles simpler, not process/tenant security boundary.

### Multi-GUI with external Docker

Per-session headed/private display/noVNC-Selkies/auth gateway, software first, then measured GPU need.

### Linux native binary

POC libcrun C-FFI/youki Rust; installed-app-only compare Bubblewrap before image manager.

### macOS App

Linux need: compare libkrun/Containerization. Browser-only native profiles/contexts simpler, different isolation.

## Reference architectures / path

### Architecture1: Fast GUI feasibility

```text
Local browser
    | HTTPS/auth
    ▼
Host loopback / SSH tunnel
    │
    ▼
Webtop container
    |-- Desktop/window manager
    |-- Chromium/GUI
    |-- Display/compositor
    +-- Selkies pixels/input/audio
```

Proves display/input/browser, not smallest image; Webtop extras/privilege conveniences need audit for untrusted tenants.

Two software sessions template; GUI_IMAGE fixed verified platform tag/digest, not tested deployment.

```bash
export GUI_IMAGE='lscr.io/linuxserver/webtop:<verified-tag-or-digest>'
export GUI_PASSWORD='<strong-password>'

docker run -d --name gui-session-1 --init \
  --cpus=2 --memory=2g --pids-limit=512 --shm-size=512m \
  -p 127.0.0.1:3001:3001 \
  -e CUSTOM_USER=viewer -e PASSWORD="$GUI_PASSWORD" \
  --mount type=volume,source=gui-session-1-config,target=/config \
  "$GUI_IMAGE"

docker run -d --name gui-session-2 --init \
  --cpus=2 --memory=2g --pids-limit=512 --shm-size=512m \
  -p 127.0.0.1:3002:3001 \
  -e CUSTOM_USER=viewer -e PASSWORD="$GUI_PASSWORD" \
  --mount type=volume,source=gui-session-2-config,target=/config \
  "$GUI_IMAGE"
```

HTTPS localhost3001/3002, self-signed warnings. Basic auth trusted-local only; production trusted cert/auth/session grants. Docker managers see env passwords; use product secret injection.

Resource caps POC starting point, not performance; no privileged/host IPC/GPU required. Diagnose kernel/image/errors before broad namespace relaxation.

### Architecture2: Product multi-browser UI

```text
Application binary / control plane
    |-- Images/resources
    |-- Create/stop/reclaim sessions
    |-- Per-session tokens/routes/ports
    +-- Monitoring/resource limits
          │
          ▼
Session container / microVM
    |-- Normal-user Chromium/sandbox
    |-- Private profile/download/tmp
    |-- Display/remote endpoint
    +-- Internal/loopback automation
          │
          ▼
Auth gateway -> WebView/noVNC + manual takeover
```

Docker first separates session lifecycle/runtime, then replace libraries without debugging all display/images/isolation simultaneously.

### Responsibilities remaining after embedding

1. **Images**: manifest/architecture/digest/layers/whiteouts/rootfs.
2. **Isolation**: UID-GID/namespaces/mounts/read-only/network/seccomp/caps/cgroups-VM.
3. **Browser/GUI**: launch/sandbox/display/encoding/input/fonts/profiles.
4. **Lifecycle**: init-reaping/health/timeouts/failure/ports-temp/recovery.
5. **Distribution**: runtime/browser/rootfs/kernel pins/security/signing.
6. **Management security**: auth/redaction/privateCDP/data permissions/deletion.

Linked OCI handles only some execution/isolation, not all Docker image/network/storage.

## Capacity budgets / measurements

### Disk

```text
Disk ~= unique layers + writable sessions + profile/download/log volumes + VM overhead
```

10 same-image containers not10bases, users' downloads/cache grow. Independent VM block sharing/COW/cache must be checked, not assumed OverlayFS.

### Memory

```text
Host ~= platform/control + sum(browser+display+encoder+actual shm) + margin
```

If measured1GiB/session, target10 means10GiB plus overhead/margin; arithmetic example, not Chrome measurement. Count guest kernel/memory overhead too.

shm counts budget; 1g neither exempt nor instantly1GiB consumed.

### Benchmark scenarios

| Dimension | Fixed/variable | Record |
|---|---|---|
| Versions | Digest/arch/browser/runtime/kernel | Reproduction |
| Concurrency | 1/5/10 | Steady/peak/OOM-crashes |
| Pages | Blank/business/SPA/video-WebGL | Workload differences |
| Display | Resolution/FPS, 720p-1080p | Bandwidth/encodeCPU/input latency |
| Startup | Cold/cached/existing profile | Download/process/interactive time |
| Features | Chinese/clipboard/files/audio | Usability/host grants |
| Isolation | Cross-files/network/CDP/UI | Forbidden tenant access |

Container stats/RSS/host-VM totals differ; record container+host, use PSS for sharing as needed.

## Security / deployment checklist

- Separate GUI/VNC/CDP/control auth, not obscure ports.
- Normal browser user/check actual sandbox.
- No unnecessary Docker socket/home/SSH/display-DBus mounts.
- No default privileged/host network-IPC/broad devices.
- Network may reach host/LAN/internet; arbitrary URL SSRF/metadata/probing needs egress policies.
- Image/browser updates; digest repeatability, not eternal freeze.
- CPU/memory/PIDs/duration/disk/download caps, cleanup exits.
- Minimum clipboard/files/mic-camera, no tenant volume sharing.
- Hostile multitenant evaluate microVM, not Docker=VM boundary.

## Still to validate

1. Linux server/Mac/cross-platform?Rust-Go-C++-Swift?
2. Chrome-only or arbitrary apps?
3. Untrusted users/pages/process vs kernel boundary?
4. Pin arch/digest, measure compressed/unpacked/disk growth.
5.1/5/10 CPU-RSS-shm/first screen/input/bandwidth, same pages/resolution/tabs.
6. Chinese fonts/input/clipboard/files/WebGL/audio/takeover, not launch only.

Docs research, no containers/benchmarks; estimates distinguished from confirmed official capabilities.

## Evidence / sources

- [jlesage](https://github.com/jlesage/docker-baseimage-gui): minimal GUI/ports/auth/devices.
- [Webtop](https://github.com/linuxserver/docker-webtop): desktops/Selkies/HTTPS/shm-GPU; auth needs config, no internet exposure.
- [noVNC](https://github.com/novnc/noVNC): browser client/JS.
- [Playwright Docker](https://playwright.dev/docs/docker): root/sandbox/seccomp/IPC/distros.
- [Rootless](https://docs.docker.com/engine/security/rootless/): host prerequisites.
- [OverlayFS](https://docs.docker.com/engine/storage/drivers/overlayfs-driver/): shared/writable layers.
- [crun](https://github.com/containers/crun): shared-libraries libcrun.
- [youki](https://github.com/youki-dev/youki)/[libcontainer](https://github.com/youki-dev/youki/tree/main/crates/libcontainer): Rust runtime/library.
- [Bubblewrap](https://github.com/containers/bubblewrap): configuration-dependent sandbox.
- [libkrun](https://github.com/containers/libkrun): C/dylib/KVM-HVF/virtio/targets.
- [Apple container](https://github.com/apple/container)/[Containerization](https://github.com/apple/containerization): VM/Swift/platform.
- [Podman](https://podman.io/): daemonless, Mac LinuxVM.
- [Firecracker](https://github.com/firecracker-microvm/firecracker)/[Cloud Hypervisor](https://github.com/cloud-hypervisor/cloud-hypervisor): KVM candidates, no GUI POC.

## Categories / tags

- Category: Technology / containers and sandboxes
- Tags:#Docker #GUI #Chromium #noVNC #Containers #Sandbox #EmbeddableRuntime
