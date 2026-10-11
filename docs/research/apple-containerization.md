---
{ tags: [ Architecture ] }
---
# Apple container: capabilities, architecture, and usage research

> Historical research: versions and “current” refer to the original review date. See the [development plan](../development-plan.md) for implementation status; this report contains recommendations and historical observations.

Research date: 2026-10-02  
Version baseline: `apple/container 1.5.0`, with `apple/containerization 0.47.0`.  
Scope: official releases, documentation at fixed tags, and key source files. No software installation, container execution, or performance benchmarks were performed.

Related notes: [current architecture](../architecture.md).

## 0. Key findings

1. **Apple container is Apple's open-source Linux container tool, not a container for macOS applications.** It targets Apple silicon Macs; the officially supported platform is macOS 26.
2. **It is not “Docker without VMs.”** Ordinary `container run` gives each container a separate lightweight Linux VM, rather than sharing one large Linux VM.
3. **Image compatibility does not imply control-plane compatibility.** OCI images, registries, and Dockerfiles are supported; Docker Engine APIs, sockets, Compose, and all Docker CLI flags are not implied.
4. **The latest stable release at review time was 1.5.0, released 2026-09-29.** It includes builds, image distribution, custom networks, forwarding, volumes, statistics, persistent Linux machines, and experimental local Kubernetes. The initial 2025 feature list is outdated.
5. **Containerization is an embeddable Swift library.** Integrating Linux sandboxes into a Mac app calls for evaluating this library, beyond treating the `container` CLI as an embedded runtime.
6. **GUI/Chromium can run as Linux workloads, but there is no built-in desktop.** Linux graphics, display transport, and browser sandboxing are still required; GPU passthrough and native macOS windows cannot be assumed.
7. **Separate VMs add kernel isolation, while the host management plane remains an attack surface.** Image extraction, registry authentication, file sharing, SSH agents, and kubeconfig handling require review; recent releases include relevant fixes.

---

## 1. Versions and project status

### 1.1 Version snapshot at the research date

| Item | Verified result | Meaning |
|---|---|---|
| Latest stable CLI | `1.5.0` | Non-prerelease returned by GitHub releases/latest |
| Release timestamp | `2026-09-29T02:33:21Z`, 09-29 10: 33 Beijing time | Distinct from the 2026-10-02 query date |
| Bundled Containerization version | `0.47.0` | `scVersion` in `1.5.0/Package.swift`; not a claim about the library's latest independent release |
| Builder shim | `0.13.1` | Separate build-toolchain dependency |
| Signed installer | `container-1.5.0-installer-signed.pkg` | Official signed package recommended |
| Signed installer size | `118,045,087 bytes`, approximately `112.6 MiB` | Download size only; excludes workload images, initial kernel preparation, and runtime data |
| Supported runtime platform | Apple silicon + macOS 26 | Intel Macs are not a standard supported platform |
| Building the library from source | Apple silicon, macOS 26, Xcode 26 | Installing the signed CLI does not necessarily require full Xcode |

Sources: [1.5.0 release](https://github.com/apple/container/releases/tag/1.5.0), [pinned Package.swift](https://github.com/apple/container/blob/1.5.0/Package.swift), [pinned README](https://github.com/apple/container/blob/1.5.0/README.md).

### 1.2 Recent changes relevant to selection

| Version | Release date (UTC) | Relevant changes |
|---|---|---|
| `1.2.0 / 1.2.1` | 2026-07-29 / 08-07 | Local `k8s` plugin, running-container export, build SSH forwarding, masked/read-only paths; multiple security fixes in 1.2.0 |
| `1.2.2` | 2026-08-08 | Fixed Kubernetes plugin failure in release installers |
| `1.3.0` | 2026-08-24 | Default Kata kernel updated to `3.32.0-debug`; registry scheme defaults to HTTPS, with `auto` removed |
| `1.3.1` | 2026-08-29 | Security fixes in IDs, digests, image loading/extraction, and registry authentication |
| `1.4.1` | 2026-09-09 | Added `container clean` and improved system status; fixed OCI-layout symlinks and Unix-socket buffers. Official notes state 1.4.0 was not released |
| `1.5.0` | 2026-09-29 | Fixed empty entrypoints and egress failures after localhost DNS changes; custom Kubernetes CNI, removal of `k8s start`, and guest kubeconfig merge security fixes |

**New releases matter beyond additional commands.** Products executing arbitrary images or untrusted programs should not remain on versions preceding host parsing and extraction fixes.

### 1.3 Stability commitments

The pinned README states:

- `container` releases use product versioning, not strict semantic versioning.
- CLI compatibility generally holds within a major version, without an absolute guarantee.
- Experimental features such as `k8s` have no compatibility guarantee.
- The `container-apiserver` XPC API promises forward/backward compatibility within a major version.
- Private XPC helpers have no cross-version compatibility guarantee.
- Application data only promises forward compatibility within a major version; upgrades do not imply safe downgrades.
- Containerization source compatibility is narrower: guaranteed only within a minor version. Pin dependencies and evaluate each upgrade.

**Record separate versions for the CLI, library, guest init, kernel, builder, and workload images; pinning `container --version` alone is insufficient.**

---

## 2. Four distinct layers

| Name | Role | Developer relevance |
|---|---|---|
| `apple/container` | User tools, CLI, API server, image/network/runtime helpers | Reference implementation for daily usage and product control planes |
| `apple/containerization` | Swift container, image, filesystem, and VM management library | Main integration target for embedding in an app or binary |
| `Virtualization.framework` | macOS VM and virtual-device framework | Executes Linux VMs and supplies devices; not a Docker engine |
| `vminitd` / `vmexec` | Linux guest init, management, and execution | Receives host requests and manages Linux processes |

OCI images define application user space; the runtime separately supplies the Linux kernel and guest init. An Ubuntu image is not a complete Ubuntu VM download and does not imply use of that distribution's kernel.

### 2.1 Typical comparison with Docker Desktop

```text
Typical Docker Desktop model
macOS
  └── Linux VM
        └── Docker Engine / container runtime
              ├── container A ─┐
              ├── container B  ├── shared Linux kernel in this VM
              └── container C ─┘

Ordinary Apple container model
macOS
  ├── container control plane
  ├── lightweight VM A → kernel A + init + workload A
  ├── lightweight VM B → kernel B + init + workload B
  └── lightweight VM C → kernel C + init + workload C
```

This comparison concerns ordinary application containers. `container machine` and Kubernetes nodes are different workloads: a node VM may manage multiple processes/Pods, so one Apple VM per Pod cannot be inferred.

### 2.2 Direct implications

- **Isolation: ** other ordinary containers do not share the current container's guest kernel.
- **Resources: ** each instance adds guest kernel/init overhead. Measure dense concurrency rather than counting application memory alone.
- **Networking: ** each container has its own IP, directly accessible from the host. Port mapping is one access method.
- **File sharing: ** expose only the directories needed by each VM.
- **Kernel configuration: ** kernels can be selected per container for feature/version testing.
- **Startup: ** official library design notes claim subsecond startup in optimized environments. This excludes image downloads, cold filesystem preparation, and application first paint; it was not measured here.

---

## 3. Architecture

### 3.1 Host control plane

```text
container CLI / client
         │ XPC
         ▼
container-apiserver (launchd user LaunchAgent)
         ├── container-core-images
         │      └── Registry client + local image/content store
         ├── container-network-vmnet
         │      └── vmnet networking, IP allocation, and network management
         └── container-runtime-linux (one runtime helper per container)
                └── Containerization
                      └── Virtualization.framework
                            └── Linux VM for this container
```

`container system start` starts the API server and associated services; `container system stop` stops them and unregisters them from launchd.

**No Docker daemon is required, but background services exist.** The API server, helpers, and VMs are all runtime components.

Key macOS integrations:

| Technology | Role |
|---|---|
| Virtualization.framework | VM, CPU/memory, and virtual devices |
| vmnet | Virtual networking |
| XPC | CLI/API server/helper communication |
| launchd | Service lifecycle |
| Keychain | Registry credentials |
| Unified Logging | System logs |

Source: [technical overview](https://github.com/apple/container/blob/1.5.0/docs/technical-overview.md).

### 3.2 Guest internals: vminitd and workload PID 1 differ

```text
Host runtime helper
       │ gRPC over vsock
       ▼
guest kernel
       └── vminitd (initial VM process)
              ├── Mount, network, and environment management
              ├── Create and start containerized processes
              └── I/O, signals, and exit events
                    └── Application container process
```

vsock provides host/guest management transport without exposing guest management as a public TCP service.

Distinguish:

- `vminitd` is the VM's initial process.
- Within the application's process namespace, the application may be PID 1.
- CLI `--init` handles application signal forwarding and orphan reaping. Omitting it does not remove VM init.

Sources: [Containerization 0.47.0 README](https://github.com/apple/containerization/blob/0.47.0/README.md), [runtime configuration](https://github.com/apple/container/blob/1.5.0/docs/runtime-configuration.md).

### 3.3 Image and rootfs path

```text
OCI Registry
    → Select linux/arm64 or linux/amd64 manifest
    → Download config/layers into content storage
    → Construct Linux ext4 rootfs
    → Prepare guest kernel + init filesystem
    → Attach block devices, virtiofs, and networking
    → Start VM → vminitd → application process
```

Containerization supplies OCI, registry, archive/extraction, ext4, Linux process, and VM APIs. It is more than a thin Virtualization.framework wrapper.

Layer downloads can use content caching, but **Docker OverlayFS assumptions about shared lowerdirs do not establish Apple's disk usage**. CLI writable filesystems and named volumes use macOS sparse disk images; measure actual sharing/copying and allocation.

### 3.4 Build architecture

`container build` uses a separate BuildKit-based builder workload, managed by a builder shim:

```text
Dockerfile / Containerfile + build context
         → container build
         → BuildKit builder VM
         → OCI image → local storage / registry
```

Multi-stage builds, arguments, targets, secrets, SSH forwarding, multiple architectures, and OCI/tar/local outputs are supported. Include the builder VM's CPU, memory, and cache in resource budgets.

### 3.5 Library Linux backend versus CLI platform support

Containerization `0.47.0` documents two VM backends:

- macOS: `VZVirtualMachineManager` uses Virtualization.framework.
- Linux: `CHVirtualMachineManager` uses Cloud Hypervisor + KVM, requiring `cloud-hypervisor`, optional `virtiofsd`, `/dev/kvm` access, and network preparation.

Callers configure host networking for TAP/bridge/NAT. Integration tests currently cover only a cross-platform subset.

**The library's Linux backend does not make the `container 1.5.0` CLI an equally supported cross-platform Docker replacement.** Evaluate APIs, dependency distribution, networking, and Linux test coverage separately.

---

## 4. Capability boundaries

| Capability | 1.5.0 status / caveats |
|---|---|
| OCI pull/push/tag/list/inspect | Supported; default registry is docker.io |
| Dockerfile / Containerfile build | Supported through BuildKit |
| ARM64 Linux workloads | Standard preferred path |
| AMD64 user space | Rosetta support; validate target applications |
| create/run/start/stop/exec/logs/cp/export | Supported; consult pinned command documentation |
| Concurrent containers | Supported; each ordinary container has a separate VM |
| CPU, memory, ulimit, shm | Supported |
| bind mounts / named volumes / tmpfs | Supported; some data/lifecycle defaults differ from Docker |
| Custom networks, IPv4/IPv6, forwarding | Supported; official baseline is macOS 26 |
| Host/container DNS integration | Supported with configuration on both sides; bare-hostname discovery on custom networks has gaps |
| SSH agent / Unix sockets | Relevant options supported; explicit sharing expands authorization |
| Read-only rootfs, cap-add/drop | Supported |
| masked/read-only paths | Available, experimental |
| Custom kernel/init filesystem | Supported |
| Nested virtualization | Conditional: M3+ and a KVM-enabled guest kernel |
| Linux machine | Supported for persistent development environments |
| Local Kubernetes | Experimental plugin; custom CNI supported in 1.5.0 |
| Native macOS GUI/macOS containers | Outside scope |
| Linux GUI remote display | Can be assembled as a workload; no built-in desktop |
| Automatic Metal/GPU in Linux guests | No dependable standard CLI passthrough procedure was found in this review |
| Docker Engine REST API / socket | Not the public control model; do not treat it as a compatible endpoint |
| Built-in Docker Compose | Absent from pinned command reference; verify third-party adapters separately |

OCI compatibility concerns images and distribution. It does not guarantee unchanged execution of images depending on specific kernels, devices, permissions, or Docker APIs.

---

## 5. Installation, upgrades, and first commands

### 5.1 Official installation

1. Download the signed `.pkg` from [1.5.0 release](https://github.com/apple/container/releases/tag/1.5.0).
2. Open the installer; writing to `/usr/local` requires administrator authorization.
3. Start system services and prepare the default Linux kernel when prompted.

```bash
container --version
container system start
container system version
container system status
container run --rm alpine:3.22 echo hello
```

The daily control service is a user LaunchAgent, so each run does not require sudo. Installation, some DNS/network integration, protected-file access, and custom packaging still have permission requirements.

Offline/restricted environments must prepare the kernel, vminit, builder images, and workload images; copying the CLI binary alone is insufficient.

### 5.2 Basic lifecycle

```bash
container run -it --rm alpine:3.22 sh
container run -d --name research-web --cpus 2 --memory 1g \
  -p 127.0.0.1:8080:80 nginx:stable-alpine

container ls
container ls --all
container inspect research-web
container exec research-web nginx -v
container logs research-web
container logs --boot research-web
container stats --no-stream research-web

container stop research-web
container start research-web
```

`http://127.0.0.1:8080` is the host entry point. Example tags are educational; use validated architectures and digests for deployment.

Deleting a session and its writable layer destroys data; run only after confirming they are no longer needed:

```bash
container stop research-web
container rm research-web
```

### 5.3 Upgrades and downgrades

Official upgrade procedure:

```bash
container system stop
/usr/local/bin/update-container.sh
container system start
container system version
```

A specific signed version can also be installed manually. Back up before downgrading; retaining data does not guarantee older versions can read newer data.

Official uninstall script:

- `uninstall-container.sh -k`: removes tools and retains user data.
- `uninstall-container.sh -d`: also deletes user data; destructive and unsuitable as the first troubleshooting step.

No installation, upgrade, or uninstall was performed in this research.

---

## 6. Builds, image publishing, and migration

### 6.1 Minimal Dockerfile

```dockerfile
FROM python:3.13-alpine
WORKDIR /app
COPY . /app
EXPOSE 8000
CMD ["python3", "-m", "http.server", "8000", "--bind", "0.0.0.0"]
```

Place this Dockerfile and test files in a separate test directory, then run:

```bash
container build -t local/research-web:demo .
container run -d --name research-built-web \
  -p 127.0.0.1:8081:8000 local/research-web:demo
```

Do not use the entire home directory as build context; control sensitive and irrelevant files with `.dockerignore`.

### 6.2 Builder resources

The builder defaults to 2 CPUs / 2 GiB RAM; ordinary containers default to 4 vCPUs / 1 GiB RAM.

```bash
container builder start --cpus 4 --memory 8g
container builder status
container build -t local/research-web:demo .
```

For an existing builder, resource changes require the documented stop/delete/start procedure. Deletion clears state/cache; evaluate that cost.

### 6.3 Multiple architectures

```bash
container build --arch arm64 --arch amd64 \
  -t registry.example.com/team/research-web:demo .

container run --arch arm64 --rm alpine:3.22 uname -m
container run --arch amd64 --rm alpine:3.22 uname -m
```

Rosetta translates amd64 user space; **it does not launch a VM with a native x86 guest kernel**. `uname` reporting x86_64 alone does not prove kernel architecture or complete x86 behavior equivalence.

Validate browser sandboxing, JIT, CPU instructions, low-level debugging, kernel modules, and performance separately; prefer native ARM64 images.

### 6.4 Registry

```bash
container registry login registry.example.com
container image push registry.example.com/team/research-web:demo
container image pull registry.example.com/team/research-web:demo
container image inspect registry.example.com/team/research-web:demo
```

Avoid passwords in command lines or build arguments. Credentials integrate with host Keychain. HTTPS is the default; older `--scheme auto` examples no longer apply.

### 6.5 Docker migration checklist

- Pulling OCI images from the same registry is usually simplest; match tags, architecture, guest kernel, and application dependencies.
- Recreate container configuration, networks, and persistent volumes; engines do not necessarily share local caches or data directories.
- Evaluate Dockerfile and Compose migration separately.
- Docker sockets, `--privileged`, `--device`, `--security-opt`, and host IPC/network flags cannot be mechanically substituted with `container` options.
- Image save/load and filesystem export serve different purposes. Export is not full OCI config/history migration or a backup of all external volumes.

---

## 7. Networking, DNS, and host access

### 7.1 Basic network model

- `system start` creates the `default` vmnet network.
- Each container has its own IP accessible to the host and containers on that network.
- Obtain IPs through `container inspect`; addresses may change, so scripts should not hardcode examples.
- For containers on multiple networks, published ports forward to the first interface's IP.

**Loopback publishing constrains the forwarding listener, not access to the entire network.** Peers may directly reach VM IPs. Authenticate desktop/browser services; `-p 127.0.0.1:...` alone is not a tenant boundary.

### 7.2 Host name resolution requires two steps

Merge into the existing `~/.config/container/config.toml`:

```toml
[dns]
domain = "test"
```

Then:

```bash
container system stop
container system start
sudo container system dns create test
```

These steps set the container DNS domain and write macOS `/etc/resolver/` rules. Completing only one does not configure resolution on both sides.

For example, `research-web` becomes accessible as `research-web.test`. Official notes identify gaps in Compose-style bare-hostname discovery on custom networks; obtain IPs through inspect when needed. Do not claim complete Docker-equivalent discovery.

### 7.3 Custom networks

```bash
container network create research-net
container network list
container run -d --name research-net-web --network research-net \
  nginx:stable-alpine
container inspect research-net-web
```

Networks are isolated from each other; containers within a network are not individually isolated. Validate egress, host access, and routing against security goals.

### 7.4 Container access to host loopback services

An explicit domain can be created:

```bash
sudo container system dns create host.container.internal \
  --localhost 203.0.113.113
```

Containers can then access `http://host.container.internal:<port>`. Choose an address without network conflicts.

Documented side effects:

- Creating a localhost domain disables Private Relay.
- Related packet-filter rules are removed on reboot; revalidate configuration.

Unlike Docker's `host.docker.internal`, this requires configuration. The 1.5.0 localhost DNS egress fix does not remove these documented limitations.

---

## 8. Storage, persistence, and disk reclamation

### 8.1 Three storage modes

| Mode | Use case | Characteristics |
|---|---|---|
| Host bind mount / virtiofs | Mac editing, Linux builds, file exchange | Exposes authorized directories; test small-file I/O |
| Named volume | Databases, browser profiles, guest-only data | Guest ext4 block filesystem; officially recommended for improved I/O |
| tmpfs | Temporary files, shm, fast nonpersistent data | Uses guest memory; data disappears on stop |

### 8.2 Mount examples

```bash
container run --rm \
  --mount "type=bind,source=$PWD,target=/workspace,readonly" \
  alpine:3.22 ls /workspace

container volume create --opt size=10g research-data
container run --rm \
  --mount type=volume,source=research-data,target=/data \
  alpine:3.22 sh -c 'echo hello > /data/example.txt'

container run --rm \
  --mount type=volume,source=research-data,target=/data \
  alpine:3.22 cat /data/example.txt
```

Prefer read-only sharing. Read-only mounts still allow sensitive data to be read and exfiltrated.

### 8.3 Common misconceptions

1. Named volumes default to **512 GiB** virtual capacity using sparse images, not 512 GiB of immediate physical allocation.
2. Anonymous volumes **are not automatically removed by `--rm`**, unlike some Docker behavior. Session managers must track and reclaim them.

Distinguish sparse logical capacity, file logical length, and allocated disk space.

### 8.4 Reclamation

```bash
container system df
container clean research-net-web
```

Added in 1.4.1, `container clean` reclaims unused space in running-container rootfs and named volumes. Guest file deletion does not automatically shrink host sparse allocation; Linux `df` alone is insufficient.

The reclamation section in [resource usage](https://github.com/apple/container/blob/1.5.0/docs/resource-usage.md) does not fully describe the new clean feature; consult releases and command reference too.

These commands delete resources; run only after confirming stopped-container/unreferenced-volume data is no longer needed:

```bash
container prune
container image prune
container volume prune
```

Use application-consistent backups for databases and profiles. Stopping/deleting an environment is not a volume backup.

---

## 9. CPU, memory, runtime overhead, and observation

### 9.1 Resource configuration

```bash
container run --rm --cpus 2 --memory 2g \
  --shm-size 512M alpine:3.22 sh -c 'df -h /dev/shm'
container stats --no-stream
container stats --no-stream --format json
```

`--cpus` configures virtual CPUs and is not directly equivalent to Docker cgroup quotas. macOS schedules the VMs and host processes.

Overcommit is allowed: requests exceeding physical resources do not prevent creation. CPU contention reduces throughput; RAM demand above physical memory causes swap and latency.

### 9.2 Memory reclamation limits

The official overview describes limited ballooning support in Virtualization.framework. Pages released by guest processes to Linux are not guaranteed to return to macOS; long or memory-intensive sessions may require restarts to reduce host usage.

`--memory 16g` does not mean immediately touching all 16 GiB at startup, but guest memory release does not imply an immediate host RSS reduction. Observe:

- Container metrics from `container stats`.
- Activity Monitor and overall macOS memory pressure.
- Runtime helper/VM process memory.
- Swap, long-session peaks, and changes across restarts.

### 9.3 Total budget

```text
Disk = installation resources + kernel/init/builder + local image store
     + session rootfs/writable images + volumes + build cache + logs

Memory = host control plane + builder (when running)
       + Σ(guest kernel/init + workload + actual shm/file cache)
       + headroom for macOS and other applications
```

The 112.6 MiB installer does not establish browser-session size. Measure image downloads, rootfs, profiles, and actual RAM separately.

### 9.4 Layered debugging

| Symptom | First checks |
|---|---|
| CLI cannot connect | `container system status`, `container system version` |
| VM cannot start | `container logs --boot <name>` |
| Application exits | `container logs <name>`, entrypoint, working directory, architecture |
| Network/DNS failure | Network inspection, system DNS, egress, and localhost rules |
| Slow workload | Stats, host memory pressure, Rosetta, disk mount mode |
| Control-plane failure | `container system logs` |

Record versions, logs, and a reproducible workload before uninstalling or clearing data.

---

## 10. Machines, Kubernetes, and nested virtualization

### 10.1 Container machine: persistent Linux development

Ordinary containers center on an application process; machines center on a Linux environment and run the image's init system, supporting long-lived services, systemd, and distribution development.

```bash
container machine create alpine:3.22 --name research-dev \
  --cpus 2 --memory 4g --home-mount none
container machine run -n research-dev whoami
container machine run -n research-dev
container machine ls
container machine inspect research-dev
container machine stop research-dev
```

Machine run starts a stopped machine first. Images should provide `/sbin/init`; arbitrary minimal application images may be unsuitable.

**Machines map the host user and home directory by default, with `--home-mount rw`; default memory is half of host RAM.** These development conveniences are unsuitable defaults for untrusted code. The example disables home mounting and limits resources.

For source sharing, consider `ro` or an ordinary container sharing a dedicated directory, rather than exposing home-directory SSH keys, cloud credentials, and browser settings.

### 10.2 Local Kubernetes: available but experimental

```bash
container k8s create --name research-k8s --cpus 4 --memory 8g
container k8s list
kubectl --context research-k8s get nodes
container k8s load-image --name research-k8s local/research-web:demo
```

The plugin prepares node images, runs kubeadm, installs CNI, and merges `~/.kube/config`. The documented default in 1.5.0 is `kindest/node:v1.35.5`; alternative versions and custom CNI manifests are supported.

This targets local development/testing, not a general production Kubernetes platform on Macs. The VM-per-ordinary-container model does not imply a separate Apple VM for every Pod within a node.

**In 1.5.0, `container k8s start` was removed.** After stopping the control plane, official recovery guidance is to delete and recreate the cluster. This affects state/data and is not a lossless restart.

1.5.0 also fixed security issues when merging guest kubeconfig values into the host. Experimental status does not justify skipping updates.

### 10.3 Nested virtualization

- Requires M3 or newer Apple silicon.
- Requires a guest kernel with relevant KVM support; do not assume the default kernel qualifies.
- CLI uses `--virtualization` and a custom `--kernel`.
- Framework documentation mentioning macOS 15 does not establish official project support on macOS 15. This selection baseline remains macOS 26.

```bash
container run --rm --virtualization \
  --kernel /path/to/kvm-enabled-vmlinux \
  ubuntu:24.04 sh -c 'ls -l /dev/kvm'
```

This supplies nested execution for KVM-dependent workloads, rather than a general prerequisite for GUI, browsers, or Kubernetes.

---

## 11. GUI, Chrome, and multiple sessions

### 11.1 Available building blocks

Linux VM user space can contain:

```text
Native Mac app / host browser / WebView
          │ HTTPS + session authentication + input events
          ▼
Session VM's noVNC or other remote display service
          ▼
Linux X11/Wayland display environment
          ▼
Chromium / Chrome / other Linux GUI applications
```

A display server is not a remote UI. Xvfb supplies a virtual screen; VNC/WebSocket, streaming, or screenshot interfaces are still needed.

### 11.2 Multiple instances

Separate VMs provide separate kernels, IPs, displays, profiles, and resource configuration. Distinct host loopback ports identify entry points; profiles/volumes, authentication, and network permissions must still be isolated per session.

This is a **workload template, not a verified Apple container Webtop deployment**. First select a validated GUI image with a linux/arm64 variant:

```bash
export GUI_IMAGE='<validated-linux/arm64-GUI-image-with-pinned-tag-or-digest>'
container run -d --name research-gui-1 \
  --cpus 2 --memory 2g --shm-size 512M \
  -p 127.0.0.1:5801:5800 "$GUI_IMAGE"
```

Port 5800 is an example for noVNC-style images. Selkies/Webtop commonly use HTTPS 3001; adjust for the image and configure authentication. WebView, encoding, audio, and GPU requirements differ.

### 11.3 Browser compatibility and sandboxing

- Prefer ARM64 Chromium; do not assume official Linux ARM64 Google Chrome packages match amd64 availability.
- If amd64 Chrome is required, test Rosetta, sandboxing, JIT, WebGL, and headed mode together, beyond checking `uname`.
- Run browsers as ordinary users and verify actual sandboxing. VM isolation does not justify disabling the browser sandbox.
- Configure shm with CLI `--shm-size`.
- The `1.5.0` command reference lacks Docker-style `--security-opt seccomp=...`; Playwright Docker security flags cannot be copied unchanged.
- Containerization `0.47.0` `cctl` source states that seccomp requires the OCI runtime path; the default vmexec path ignores OCI seccomp. **A supported configuration structure does not prove enforcement by the selected execution path.**

### 11.4 Capabilities not yet established

- Standard CLI documentation did not confirm Metal/GPU passthrough usable directly by Linux Chrome.
- Linux UIs do not automatically become native AppKit windows.
- Cameras, microphones, clipboard, file transfer, DRM video, and WebGL require display-protocol and host-UI integration.
- The Swift library does not generate noVNC frontends, manage human takeover, or implement tenant access control.

**Prototype software rendering + headed Chromium + noVNC first; then test input, CJK fonts, downloads, and 1/5/10 sessions. Avoid introducing Rosetta, GPU, and library embedding simultaneously in the first stage.**

---

## 12. Embedding in a binary or Mac application

### 12.1 Three integration options

| Approach | Effort | Suitable stage |
|---|---|---|
| CLI subprocess with JSON output | Lowest; installed CLI/services required | Quick PoC or products accepting external installation |
| Public client/XPC controlling installed services | More API/packaging coupling; services still required | Efficient service management with accepted system deployment |
| Direct Containerization Swift package | Highest; manage resources and lifecycle yourself | Mac products requiring customized isolation without external container tools |

Go/Rust/C++ products can begin with a Swift sidecar and local IPC. FFI requires bridging, concurrency, asynchronous callbacks, and error handling; a Swift library does not imply a stable C ABI.

### 12.2 APIs and lifecycle

Relevant APIs include `ContainerManager`, `LinuxContainer`, `LinuxProcess`, `Kernel`, `VZVirtualMachineManager`, and OCI/ext4 modules.

```text
Prepare kernel / initfs / storage directories
       → Fetch and verify OCI image
       → Select platform and prepare ext4 rootfs
       → Create VM manager / networking
       → Create LinuxContainer with process and resource configuration
       → create → start
       → Connect I/O, send signals, wait for exit
       → Stop / delete temporary resources / retain required volumes
```

A pinned Swift Package dependency can use:

```swift
.package(
    url: "https://github.com/apple/containerization.git",
    exact: "0.47.0"
)
```

This is a dependency fragment, not a complete compilable project. Consult the pinned [cctl/RunCommand.swift](https://github.com/apple/containerization/blob/0.47.0/Sources/cctl/RunCommand.swift) for a full example; do not assume old API examples remain valid.

### 12.3 Signing, permissions, and distribution

The official Virtualization entitlement file contains:

```xml
<key>com.apple.security.virtualization</key>
<true/>
```

This entitlement concerns VM execution, not a complete product permission model. Assess signing, notarization, App Sandbox, file-selection authorization, vmnet, network-helper deployment, and distribution channels.

It does not establish unrestricted unsigned execution or Mac App Store acceptance. Validate networking and GUI in a real signed application.

### 12.4 Real deliverables beyond one binary

Embedding the library does not reduce everything to one binary:

- Swift application and any helper executables.
- Linux kernel.
- vminit/init filesystem.
- Workload OCI images/rootfs.
- Browser, fonts, dynamic libraries, and display services.
- Profiles/volumes, logs, image caches, and update data.

An App bundle or first-run downloads can package these, but version pinning, verification, licenses, cache paths, and updates remain necessary. The approximately 112.6 MiB installer is not a browser product's total distribution size.

### 12.5 Official examples can lag versions

In `0.47.0`, `examples/ctr-example/Package.swift` still pins `0.33.4`. CLI runtime documentation uses `0.34.0` in a custom-init example, while CLI 1.5.0 depends on `0.47.0`.

Official examples explain structure, but check compatibility before combining them:

- Confirm versions in the project's Package.swift/Package.resolved.
- Match kernel/initfs to the actual library/CLI.
- Run matching-version tests before GUI integration.
- Avoid private helpers without compatibility guarantees.

---

## 13. Security beyond VM boundaries

### 13.1 Benefits over shared-kernel containers

Separate guest kernels reduce the Linux kernel's role as a cross-session attack surface; sharing only required directories reduces data exposure.

VM isolation does not eliminate escapes, host parsing vulnerabilities, or cross-session leaks. Boundaries include:

```text
Image and registry input
   → Host parsing/extraction/content storage
   → Host control plane and virtual devices
   → VM boundary
   → Guest process permissions
   → Browser sandbox
   → UI/automation/network authorization
```

### 13.2 Untrusted workload checklist

- Use versions containing security fixes; track advisories in both repositories.
- Avoid machines with whole-home rw mounts for untrusted tasks.
- Do not forward SSH agents, share Keychain/credential files, or mount unnecessary host Unix sockets.
- Share only explicitly authorized directories; read-only access still permits reading and exfiltration.
- Authenticate UI, CDP, and management APIs separately, including direct network access.
- Use ordinary users, cap-drop, read-only rootfs/paths, and verify enforcement.
- Do not treat unenforced seccomp configuration as a guarantee.
- Control egress to LAN, host services, cloud metadata, and other sensitive destinations.
- Limit CPU, memory, file descriptors, process counts, session duration, and growing data.
- Track/reclaim rootfs, volumes, networks, ports, and temporary data, including anonymous volumes.
- Kubernetes modifies host kubeconfig; audit authorization and state merging for untrusted clusters.

Recent host-side image, path, registry, and configuration fixes show VM isolation is only part of security. No untested claim of production isolation for malicious tenants is made here.

---

## 14. Choosing between options

| Scenario | Apple container suitability | Main considerations |
|---|---|---|
| Single Linux service on Apple silicon | High | Clear installation, reusable OCI images |
| A few isolated sandboxes/agent environments | Worth validating | Separate kernels, Swift integration; audit host control plane and cleanup |
| Heavy Compose/existing Docker API products | Higher migration cost | Different engine/orchestration control model; renaming commands is insufficient |
| Local Kubernetes experiments | Usable with experimental constraints | Cluster lifecycle, data, and version risks |
| Persistent Linux development machines | High | Convenient default home sharing also carries risks |
| Many browser sessions | Benchmark first | Per-VM memory, reclamation, display transport, density |
| Native macOS GUI isolation | Not a direct solution | Linux workloads rather than AppKit containers |
| General Linux production container engine | CLI equivalence not established | Evaluate library backend and user-tool support separately |
| Cross-platform single Rust/Go binary | Complex | Swift/FFI/sidecar, kernel/rootfs, signing, platform networking |

Docker Desktop/Podman generally fit mature Docker workflows better. Apple Containerization merits evaluation for specifically scoped native Mac isolation products. Without benchmarks, do not claim universally better speed, size, or security.

---

## 15. Suggested validation sequence

### Stage A: basic model

1. Confirm Apple silicon/macOS 26 and install a pinned signed version.
2. Record CLI/server/library/kernel/initfs/builder versions.
3. Validate ARM64 hello, network services, stop/start, logs, and stats.
4. Test host forwarding, direct IP access, DNS, and custom networks separately.
5. Test binds/volumes, `clean` after guest deletion, and anonymous-volume cleanup.

### Stage B: target browser workload

1. ARM64 Chromium: headless first, then headed + software rendering + noVNC.
2. Verify browser sandbox and ordinary-user execution.
3. Test Chinese input, fonts, clipboard, downloads/uploads, and human takeover.
4. Measure startup, memory, CPU, bandwidth, and input latency at 1/5/10 sessions using identical resolution, pages, and tab counts.
5. Run long sessions and induce memory peaks; measure host usage after guest release, restarts, and clean.
6. Test Rosetta combinations only if Google Chrome amd64 is required.

### Stage C: product integration

1. Implement lifecycle through CLI subprocesses first, keeping the control plane independent of Docker/Apple-specific flags.
2. Build a minimal standalone Swift prototype with pinned Containerization.
3. Complete signing, notarization, networking/directory authorization, resource distribution, and updates.
4. Add WebView, session authentication, automation APIs, and concurrent resource pools.
5. Test crashes, host reboots, network changes, upgrades, and backup/restore.

### Questions still lacking measurements

- Headed/sandbox compatibility of the target Chromium image on this version.
- Per-session RAM, long-session reclamation, and maximum practical concurrency on the target Mac.
- Audio, video, encoding, and input capabilities of the chosen WebView/noVNC/Selkies stack.
- Exact GPU device path and availability.
- Networking/virtualization deployment allowed by the target App Sandbox/distribution channel.

Treat these as PoC acceptance criteria rather than documenting them as already supported.

---

## 16. Official sources and reading order

Suggested order: README → Technical overview → Networking/Volumes/Resources → Command reference → Containerization API and cctl source → Releases/Security.

| Source | Purpose |
|---|---|
| [container 1.5.0 release](https://github.com/apple/container/releases/tag/1.5.0) | Release, breaking changes, installer, security fixes |
| [GitHub latest release API](https://api.github.com/repos/apple/container/releases/latest) | Review-time version snapshot; URL changes with future releases |
| [Pinned README](https://github.com/apple/container/blob/1.5.0/README.md) | Platform, installation, upgrades, compatibility |
| [Technical overview](https://github.com/apple/container/blob/1.5.0/docs/technical-overview.md) | Control plane, per-container VMs, memory limits; some historical sections lag |
| [Command reference](https://github.com/apple/container/blob/1.5.0/docs/command-reference.md) | Actual run/build/machine/k8s/clean flags |
| [Networking](https://github.com/apple/container/blob/1.5.0/docs/networking.md) | IPs, two-step DNS, custom networks, discovery gaps |
| [Host integration](https://github.com/apple/container/blob/1.5.0/docs/host-integration.md) | SSH agents, host loopback, Private Relay/reboot limitations |
| [Volumes](https://github.com/apple/container/blob/1.5.0/docs/volumes.md) | Sparse ext4, default capacity, anonymous-volume lifecycle |
| [Resource usage](https://github.com/apple/container/blob/1.5.0/docs/resource-usage.md) | CPU/RAM defaults, overcommit, stats, disk usage |
| [Runtime configuration](https://github.com/apple/container/blob/1.5.0/docs/runtime-configuration.md) | Capabilities, init, path protection, nested virtualization |
| [Multiplatform images](https://github.com/apple/container/blob/1.5.0/docs/multiplatform-images.md) | ARM64/AMD64 builds and Rosetta |
| [Container machine](https://github.com/apple/container/blob/1.5.0/docs/container-machine.md) | Persistent Linux, home mounts, resource defaults |
| [Kubernetes](https://github.com/apple/container/blob/1.5.0/docs/kubernetes.md) | Local clusters, load-image, CNI, recovery limits |
| [Config reference](https://github.com/apple/container/blob/1.5.0/docs/container-system-config.md) | config.toml and kernel configuration |
| [Config source](https://github.com/apple/container/blob/1.5.0/Sources/ContainerPersistence/ContainerSystemConfig.swift) | Verify concrete values such as the default kernel |
| [container Package.swift](https://github.com/apple/container/blob/1.5.0/Package.swift) | Containerization 0.47.0 and builder shim 0.13.1 |
| [Containerization 0.47.0 README](https://github.com/apple/containerization/blob/0.47.0/README.md) | Library, VM backends, vsock, build requirements |
| [Containerization API docs](https://apple.github.io/containerization/documentation/) | API reference; online docs may change with versions |
| [cctl RunCommand](https://github.com/apple/containerization/blob/0.47.0/Sources/cctl/RunCommand.swift) | Lifecycle, guest overhead, seccomp execution paths |
| [Virtualization entitlement](https://github.com/apple/containerization/blob/0.47.0/signing/vz.entitlements) | VM signing entitlement example |
| [container releases](https://github.com/apple/container/releases) | Changes/security fixes across 1.2–1.5 |
| [container security advisories](https://github.com/apple/container/security/advisories) | Host control-plane updates |
| [Containerization security advisories](https://github.com/apple/containerization/security/advisories) | Library extraction, registry, and content-store updates |

### Resolving conflicting sources

- Prefer pinned README/source and release notes over historical overview prose.
- The overview still includes early macOS 15/initial-release descriptions; these do not override the 1.5.0 README's macOS 26 support baseline.
- Kernel/program output in documentation may be historical examples, not the current kernel version.
- Current source defaults to Kata archive 3.32.0 and kernel binary `vmlinux-6.18.35-197-debug`. Customization is allowed; inspect local configuration for actual values.
- New library features do not automatically imply CLI exposure, default enforcement, or full support.

## Categories and tags

- Category: Technology / Containers and sandboxes
- Tags: #AppleContainer #Containerization #macOS #AppleSilicon #Swift #Virtualization #OCI #Sandbox #Chromium
