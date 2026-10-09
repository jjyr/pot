# macOS and Windows Sandbox providers

Research date: 2026-10-07. Scope: current Pot source and primary platform documentation. No VM installation, executor prototype, performance measurement, or Windows host testing was performed. The designs below are proposals, not delivered features. [Current scope](../development-plan.md) and [Agent/Sandbox boundaries](../agent-sandbox-design.md) remain authoritative.

Subsequent scope decision: the minimum redesign now targets Linux and macOS, with Android, iOS and Windows reserved as future extensions. See [Sandbox providers](../sandbox-providers.md) for the target contract; this page retains platform background rather than defining delivery priority.

## Recommendation

Support actual guest operating systems through separate execution providers while keeping Agent loops, model credentials, task assignments, leases and operation reconciliation in the Host Runtime.

| Guest / location | Recommended provider | Role |
|---|---|---|
| Linux on the current Mac | Existing Apple Containerization provider | Preserve current behavior |
| macOS on Apple silicon Mac | Direct Virtualization.framework provider | First native OS addition |
| Windows on the current Apple silicon Mac | Windows 11 ARM64 VM, initially controlled through UTM; evaluate bundled QEMU/HVF later | Local Windows applications and tools |
| Windows on a separate Windows host | Host service managing persistent Hyper-V VMs | Native Windows x64 testing and multiple persistent environments |
| Disposable Windows on a Windows host | Optional Windows Sandbox adapter | Clean, temporary execution with provider-specific limits |

This research interprets “macOS / Windows sandbox” as an isolated OS environment that can execute native applications. Restricting commands on the user's actual desktop would be a separate product boundary; Pot currently excludes direct Host environment tools.

Apple Containerization runs Linux workloads, including on macOS. Changing the OCI image cannot produce a macOS or Windows guest. See the [existing Containerization research](apple-containerization.md).

## Current implementation and required changes

The execution authority split is already useful; the infrastructure is not yet a generic provider system.

| Area | Current source / assumption | Required extension |
|---|---|---|
| Inventory | `crates/runtime/src/sandbox.rs`: Sandbox contains only `id` and `name` | Persist provider, guest OS/architecture, image identity, placement and capabilities; migrate old entries to Linux |
| Lifecycle | `Manager::helper` calls one Unix socket; Swift `Runtime` owns `LinuxContainer` / `ContainerManager` | Dispatch lifecycle to typed provider adapters without exposing raw management to workers |
| Readiness | `Manager::ready` assumes guest IP and HTTP port 17701; preparation polls for roughly 120 seconds, excluding request latency | Return transport descriptor, authenticated executor handshake and boot identity; separate installation from boot |
| Guest broker | `image/executor.py`: Linux cgroup v2, `pwd`, `prctl`, `/proc`, root-private `/run` | Platform-specific privilege separation, scope containment and cleanup confirmation |
| Process runner | `crates/tool-executor/src/processes.rs`: Unix `process_group`, `libc::kill`, shell `-c` | Separate Unix/Windows process backends; define supported shell arguments explicitly |
| Files / patch | `crates/tool-executor/src/tools.rs`: Unix modes, rename and directory fsync | Windows ACL/replace semantics and native path validation |
| Workspace / skills | `crates/runtime/src/sandbox.rs`, `crates/runtime/src/skills.rs`: `/data/workspace`, locally readable host mirror; `image/skill_mounts.py`: mount namespaces | Provider-owned path mapping, skill views and managed artifact transport |
| Desktop | Swift Helper returns a noVNC URL; `crates/app/swift/LocalRuntime.swift` loads a WebView | Typed desktop descriptors for native VM view, external viewer and authenticated remote stream |
| Packaging | `scripts/pot`: Linux ARM64 musl runner; `crates/core/src/agent.rs`: default `/bin/sh` | macOS ARM64 and Windows ARM64/x64 runners plus OS-compatible shell policy |

Do not merely extend a UI OS dropdown. Windows execution currently encounters unguarded Unix APIs, and the Python broker intentionally refuses to operate without Linux containment. Remote providers also break the assumption that Runtime can read every guest workspace and skill file through a local mount.

## macOS provider

### VM engine and installation

Use Virtualization.framework directly from Swift. Apple's supported Apple silicon flow installs an IPSW with `VZMacOSRestoreImage` / `VZMacOSInstaller`, boots with `VZMacOSBootLoader`, and persists a compatible hardware model, machine identifier, auxiliary storage and disk. Query the restore image requirements rather than reusing Linux resource minima. [Apple installation guide](https://developer.apple.com/documentation/virtualization/installing-macos-on-a-virtual-machine), [sample app](https://developer.apple.com/documentation/virtualization/running-macos-in-a-virtual-machine-on-apple-silicon).

Proposed installation flow:

1. Import a user-prepared VM or download compatible installation media from Apple.
2. Install and expose first-run setup as an explicit provisioning state. Do not promise unattended account creation or permission approval from the installer API.
3. Install a signed guest broker and a credential-free runner; establish a standard execution account and a separate broker identity.
4. Validate executor health, permissions, workspace and shell before marking the definition ready.
5. Preserve the installed disk across ordinary stop/start. Clone templates only while stopped, with new instance identity and credentials and verified boot compatibility.

A minimum prototype should import a prepared guest first. That separates executor qualification from automated OS provisioning.

### macOS image preparation and initial setup

Follow-up review on 2026-10-07 found an important version distinction: Apple's `VZMacGuestProvisioningOptions` is introduced on host macOS 27 and requires guest macOS 27+. On first boot after restore it can create the account and optionally enable auto-login/SSH. Older guests ignore it; already provisioned guests cannot be reconfigured through these options. This supplies a public first-setup path on supported versions, correcting any inference above that account creation always needs manual/UI automation. The current Pot macOS 26 baseline still needs a fallback. [Apple API](https://developer.apple.com/documentation/virtualization/vzmacguestprovisioningoptions), [WWDC26 example](https://developer.apple.com/videos/play/wwdc2026/224/).

| Tool / route | What it provides | Pot use |
|---|---|---|
| Tart | VM creation/clone/run and OCI registry push/pull | External preparation or provider prototype; complete VM images, not Docker images |
| Packer with Tart plugin | Declarative base image plus file/shell provisioners; templates automate Setup Assistant | Reproducible template build with pinned guest version |
| Lume | IPSW creation, built-in unattended presets and VM management | Alternative external preparation; exact preset/build needs qualification |
| Direct Apple API | OS install plus macOS 27+ first-boot provisioning | Preferred integrated implementation where host/guest versions support it |

Sources: [Tart](https://github.com/openai/tart), [Tart quick start and registry format](https://github.com/openai/tart/blob/main/docs/quick-start.md), [Packer templates](https://github.com/cirruslabs/macos-image-templates), [Lume unattended setup](https://github.com/trycua/cua/blob/main/libs/lume/README.md). Third-party image/installer formats do not establish compatibility with a custom Virtualization.framework provider; qualify configuration, auxiliary storage and machine identity conversion before importing.

Recommended repeatable pipeline:

1. Install a pinned IPSW into a local build VM, or enroll a verified prebuilt base.
2. Complete initial account setup using the supported public API, a pinned setup preset or a one-time owner step. Provisioning credentials remain private.
3. Run an idempotent bootstrap script/package through a trusted temporary channel: install the signed Pot broker/runner, standard execution user, command-line/browser tools, workspace/skill permissions and launchd service.
4. Qualify command/file/control behavior and any required TCC permissions; setup account/SSH does not automatically grant Accessibility or Screen Recording.
5. Shut down and freeze a versioned golden template without product/provider tokens, login sessions or user data.
6. Clone per environment, establish independent identity, rotate all bootstrap/account/SSH credentials as appropriate, enroll a fresh broker credential, and publish readiness only after health checks. Harden any publicly documented default account/password before running model-selected commands.

This is closer to “Packer manifest + provisioning scripts + golden VM clone” than Dockerfile layers. A later OS/tool upgrade builds another template rather than mutating the frozen base. Templates are much larger than minimal Linux rootfs images; no download/startup measurements were performed.

Tart's current source license is `FSL-1.1-ALv2`, not an unconditional Apache/MIT grant; inspect permitted-purpose and redistribution terms before choosing a bundled product dependency. Owner-local image creation and macOS image redistribution have separate platform license considerations. Tool evaluation does not approve bundling. [Tart license](https://github.com/openai/tart/blob/main/LICENSE).

### Workspace and execution

macOS supports VirtioFS directory sharing; explicit mounting uses `mount_virtiofs`, and macOS 13+ supports a guest automount tag. Use a real guest root such as `/Volumes/PotWorkspace/workspace` and return it as metadata instead of requiring `/data`. Share only the environment's workspace. Broker credentials and receipts belong on the private guest disk, outside shares. [Apple shared-directory configuration](https://developer.apple.com/documentation/virtualization/vzvirtiofilesystemdeviceconfiguration).

Keep the existing authenticated execution protocol initially, with standard-user shell/patch execution, bounded output, operation IDs and turn sessions. Bundle the broker rather than relying on a system Python installation. A shared Rust protocol/broker with separate platform backends is the long-term recommendation; porting the Linux Python code verbatim is not.

Root ownership of files on a share does not establish guest-private credential protection. Apple's shared filesystem uses the host effective UID and ignores guest UID/GID changes. This reinforces the existing rule that credentials must stay on a private guest filesystem. [Apple permission semantics](https://developer.apple.com/documentation/virtualization/vzvirtiofilesystemdevice).

### Cancellation is the acceptance blocker

macOS has no Linux cgroup v2 equivalent available through the current implementation. Process-group termination does not cover `setsid`, double-forked processes, LaunchAgents or work delegated to another service. Do not claim `killpg` preserves Pot's current cancellation guarantee.

Investigate a privileged guest supervisor, per-scope users and launchd session management, but require evidence that descendants and delegated work cannot escape. UID scans alone are not race-free containment, and a user account is not a verified cancellation primitive.

For an initial provider, an **exclusive VM lease per execution owner** can make VM termination a reliable containment fallback. Explicitly advertise that cancellation may stop this dedicated environment. Main and workers must not execute concurrently in that VM; queue competing owners. Keep artifacts outside disposable OS state, quarantine unconfirmed outcomes and confirm VM termination before settling cleanup. This changes shared-VM scheduling and needs an explicit provider policy; it must never be applied to today's shared Linux environments.

If exclusive ownership or complete scope cleanup cannot be enforced, keep macOS as a developer prototype rather than silently weakening cancellation.

### Desktop

Apple's `VZVirtualMachineView` displays the VM framebuffer and forwards keyboard/mouse events through configured virtual devices. It is the preferred local macOS viewer. The current Helper owns VMs while the App owns views, so embedding this view requires a deliberate ownership/IPC design; passing a URL cannot transfer a `VZVirtualMachine` object across processes. An external provider window is a practical first step. [Apple VM view](https://developer.apple.com/documentation/virtualization/vzvirtualmachineview).

Human viewing/input and Agent automation are separate capabilities. Guest Accessibility, Screen Recording and Automation permissions must be provisioned and tested for the chosen automation API; root broker privileges do not establish those permissions. A framebuffer viewer is not a guest accessibility tree. [Apple UI automation guidance](https://developer.apple.com/documentation/XCUIAutomation/recording-ui-automation-for-testing), [screen capture permission](https://developer.apple.com/documentation/screencapturekit/capturing-screen-content-in-macos).

### License boundary

The macOS Tahoe SLA section 2B(iii), under its stated acquisition/use conditions, permits up to two additional virtual copies on an Apple-branded computer for specified purposes; volume agreements may differ. This is not a technical VM maximum or an unconditional permission for arbitrary commercial automation. Section 3 separately addresses developer-service leasing. Do not apply Pot's 16-definition limit as a macOS license entitlement or redistribute a prepared macOS image by default. [Apple SLA](https://www.apple.com/legal/sla/docs/macOSTahoe.pdf).

### Host process sandbox alternative

App Sandbox and Seatbelt can restrict a process but do not supply an independent macOS installation, desktop or guest filesystem. `sandbox-exec` is deprecated; Apple's discussion does not provide a supported drop-in replacement for a general command launcher. These can be researched as additional guest defenses, but they do not fulfill the proposed OS provider or Pot's existing execution boundary. [Apple developer discussion](https://developer.apple.com/forums/thread/661939).

## Windows providers

### Virtualization.framework versus Hypervisor.framework

Apple describes Virtualization.framework as the high-level API for macOS and Linux VMs, and Hypervisor.framework as the low-level API for building a virtualization solution. Windows is not an officially documented supported guest route in Virtualization.framework. This is a support/integration conclusion, not proof that every experimental Windows boot is technically impossible. QEMU/HVF uses the lower-level Hypervisor.framework and supplies its own machine/device implementation. [Apple framework distinction](https://developer.apple.com/forums/tags/hypervisor), [QEMU HVF](https://www.qemu.org/docs/master/system/introduction.html).

### Windows VM on the current Mac

Use Windows 11 ARM64 on Apple silicon. UTM's maintained Windows guide demonstrates this route and its guest drivers, and requires a Windows license. ARM64 Windows is a useful application environment, but must not be represented as native x64 Windows qualification; test application emulation, native modules and drivers independently. [UTM Windows guide](https://docs.getutm.app/guides/windows/).

For the prototype, import a licensed, prepared UTM VM and use its documented scripting surface for lifecycle. Keep the guest executor protocol independent of UTM. This tests Windows execution without immediately taking on firmware, TPM, display, drivers and installation. [UTM scripting](https://docs.getutm.app/scripting/scripting/).

For a self-contained provider later, evaluate a pinned QEMU sidecar with HVF acceleration and private QMP management. QEMU documents HVF on ARM macOS and a machine control protocol. This is a different stack from the Apple Containerization dependency; do not assume Apple's Linux/macOS samples establish Windows guest support in Virtualization.framework. Bundle/version/sign the firmware, TPM implementation and guest drivers as well as QEMU. [QEMU accelerators and management](https://www.qemu.org/docs/master/system/introduction.html), [QMP specification](https://www.qemu.org/docs/master/interop/qmp-spec.html).

Use a persistent guest disk and a broker-owned workspace/artifact API initially. A read-only bootstrap medium can distribute binaries, but must not contain reusable credentials accessible to execution users. Workspace sharing can be qualified separately; do not assume current Linux VirtioFS integration works with Windows. Desktop may start as UTM's external window, then SPICE/VNC/RDP or another typed viewer.

### Persistent Windows VM on a Windows host

A remote host service can own Hyper-V VM lifecycle and return an authenticated executor endpoint to Pot on the Mac. Hyper-V PowerShell supports inventory, VM creation, start/stop and checkpoints. Keep these privileged operations in the host service, outside the guest runner and model-visible command tools. [Microsoft Hyper-V automation](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/powershell).

This is the preferred route for native Windows x64 and multiple persistent Windows environments. It requires remote enrollment, capacity reporting, credentials and artifact synchronization; supporting a remote Windows execution host does not require porting the entire Swift/AppKit product to Windows.

Checkpoints are not an operation receipt or artifact backup. Restoring a disk can roll back the guest ledger while external side effects remain. Every restore/recreate must change a durable boot identity and invalidate old execution sessions; reconcile against the Host ledger without replaying commands. [Microsoft checkpoint behavior](https://learn.microsoft.com/en-us/windows-server/virtualization/hyper-v/checkpoints).

### Windows Sandbox as an optional adapter

Windows Sandbox is a disposable Windows desktop on supported Windows editions; Home is unsupported. Closing it discards installed software and guest state, although guest-initiated reboots can preserve state within the session. The current overview explicitly says multiple instances cannot run simultaneously. Advertise capacity one until exact-build tests establish otherwise; CLI IDs do not prove concurrent capacity. [Microsoft overview](https://learn.microsoft.com/en-us/windows/security/application-security/application-isolation/windows-sandbox/).

Microsoft documents `wsb` for Windows 11 24H2+, with start/list/exec/stop/share/connect/IP and JSON output. However, `wsb exec` cannot return process I/O. Its `ExistingLogin` mode needs an active user session; `System` is a separate context. Therefore use this CLI for lifecycle/bootstrap, then run Pot's broker for stdout/stderr, stdin sessions, patches and cancellation. Detect the actual installed CLI/features rather than relying on the OS label alone. [Microsoft CLI](https://learn.microsoft.com/en-us/windows/security/application-security/application-isolation/windows-sandbox/windows-sandbox-cli).

Mapped writable folders retain changes after disposal. Map only the declared workspace; read-only bootstrap resources are separate. Configure clipboard, microphone, camera and printer redirection explicitly. `LogonCommand` normally executes under an administrator guest account, so it must bootstrap a protected broker and standard execution account rather than run model commands directly. Network-disabled mode also requires a transport that does not depend on guest TCP. [Microsoft configuration](https://learn.microsoft.com/en-us/windows/security/application-security/application-isolation/windows-sandbox/windows-sandbox-configure-using-wsb-file).

This provider offers a clean OS per start, not persistent installed applications or arbitrary versioned OS images. Model OS persistence and workspace persistence separately in UI/status. CPU/disk settings unsupported by the provider must be rejected or omitted, not accepted as if effective. Running it inside the Mac's Windows VM adds nested virtualization requirements and is not the recommended local route.

### Windows guest executor

Use a Windows service broker under a protected identity and a credential-free runner under a standard user. Protect credentials, binaries and receipts with NTFS ACLs; do not pass broker tokens to child environments. A Job Object provides process grouping/resource control, not a substitute for account/ACL isolation.

Create each scope's runner suspended, assign it to a Job Object before resuming, prohibit breakaway and set `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`. Cancel through `TerminateJobObject`, then confirm no active processes before reporting settlement. Keep handles private to the broker. Test nested jobs and work delegated through services or scheduled tasks: those paths must be denied or independently supervised. [Microsoft Job Objects](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects), [assignment and suspended creation](https://learn.microsoft.com/en-us/windows/win32/api/jobapi2/nf-jobapi2-assignprocesstojobobject).

Choose an explicit shell: for example PowerShell with `-NoLogo -NoProfile -NonInteractive -Command`, or `cmd.exe /d /s /c` for a separate mode. The current Unix `-c` launch contract cannot be reused unchanged. Expose shell name and version to the Agent.

Path validation must account for drive letters, UNC/device paths, case collisions, reserved names, alternate data streams, junctions/reparse points and traversal races. Apply patch with native replace/error semantics, preserve intended ACLs and test locked destinations, CRLF, Unicode and replacement failures. Keep initial pipe sessions; ConPTY is a separate capability, not required to preserve the present pipe contract.

## Can Docker provide these environments?

Distinguish three architectures:

| Architecture | macOS guest | Windows guest |
|---|---|---|
| Ordinary Linux container on Mac | Cannot supply the macOS kernel/runtime | Cannot supply the Windows kernel/runtime |
| Native Windows containers on a Windows host | Not applicable | Suitable for supported noninteractive workloads; not a full interactive desktop |
| Linux container launching QEMU | A full macOS VM inside the container, subject to hardware/license constraints | A full Windows VM inside the container |

Docker Desktop on Mac runs containers in a Linux VM. Docker/OCI packaging does not turn that Linux kernel into another OS. Windows containers require a supported Windows host and image/kernel compatibility; Microsoft excludes applications requiring an interactive desktop from supported container workloads. [Docker platform architecture](https://docs.docker.com/faqs/platform/), [Windows host requirements](https://learn.microsoft.com/en-us/virtualization/windowscontainers/deploy-containers/system-requirements), [Windows desktop limitation](https://learn.microsoft.com/en-us/virtualization/windowscontainers/quick-start/lift-shift-to-containers).

Projects such as `dockur/windows` and `dockur/macos` package a VM launcher as a Docker image. Their documented accelerated route needs KVM; both currently exclude Docker Desktop on macOS because it does not expose KVM to containers. A specially configured nested-virtualization Linux VM would require separate hardware/hypervisor qualification. Software emulation is another possibility but has no performance qualification here. [Windows project](https://github.com/dockur/windows), [macOS project](https://github.com/dockur/macos).

For Pot on the current Mac, Docker adds a Linux VM/container layer around the actual OS VM without removing guest provisioning, executor or desktop requirements. A direct macOS VM and QEMU/HVF Windows VM remain the recommended local providers. A remote Linux/KVM host could expose container-packaged Windows VMs as a separate provider; preserve the VM boundary in capability and lifecycle reporting.

## Proposed provider contract

```text
Host Agent -> existing scoped execution tools -> Runtime authority
                                                     -> provider lifecycle
                                                     -> authenticated guest broker
                                                        -> OS-specific runner
```

The Runtime remains authoritative for immutable task bindings and unknown outcomes. Suggested fields/interfaces:

- Definition: `provider`, `guest_os`, `guest_arch`, version/image identity, `placement`, resource config and persistence policy.
- Provider operations: `probe`, `prepare`, `start`, `status`, `stop`, `delete`, `recover`, with structured progress/errors and busy checks.
- Execution handshake: protocol version, instance/boot identity, native workspace/home, available shells, platform and enforced capabilities.
- Capabilities: concurrent VM capacity, shared-owner execution, scope cancellation strategy, OS/workspace persistence, read-only skill view, pipe/PTY and desktop transport.
- Endpoint: authenticated transport selected by the provider; local guest HTTP may remain first, remote traffic uses TLS identity validation or an authenticated tunnel. An IP address alone is not identity.
- Workspace service: resolve guest paths, inspect instructions/skills and transfer artifacts without arbitrary Host paths. Use a local mirror only where the provider actually maintains one.

Use a durable instance/boot epoch across Helper restarts and restores; an in-memory generation counter alone is insufficient for snapshot-aware providers. Do not allow capability claims in guest output to grant authority. Trusted provider configuration plus qualification determines policy.

Retain the existing operation ID/input conflict checks and Host quarantine. Windows Sandbox disposal or a VM rollback may remove the guest receipt; a missing receipt does not authorize reexecution. Scope ownership still binds session IDs to task, turn, environment and boot identity.

## Delivery sequence and qualification

| Stage | Deliverable | Exit criterion |
|---|---|---|
| 1 | Extract current Linux provider, metadata/path/desktop descriptors and boot identity | Existing Linux acceptance stays green; old definitions still load |
| 2 | Imported macOS VM, broker, commands/patch, persistent workspace, external viewer | Credential isolation and exclusive-owner cancellation work; shared execution remains disabled |
| 3 | Imported Windows ARM64 UTM VM and Windows service/Job Object runner | Native shell/patch/session/cancellation contracts pass |
| 4 | Productize installation, native/embedded desktop and optional remote Hyper-V provider | Cold install, reconnect, packaging and version matrix qualified |
| 5 | Optional Windows Sandbox and bundled QEMU adapters | Provider limits, disposal and exact-build behavior qualified |

For each provider test: output/stdin/timeout/patch; worker target and lifecycle rejection; unreadable control credentials/ledger; escaped/delegated descendants; owner exclusivity or shared cancellation; disconnect/restart/restore with unknown side effects; task/schedule binding; artifact survival; read-only builtin/task skills; startup cancellation; desktop login/input/IME and permissions; denied Host/LAN access under the configured network policy. Bind shell policy to the resolved guest environment without mutating saved Agent profiles behind the user's back.

Record cold/warm boot, installation/download footprint, idle/active memory and disk growth separately. No startup, footprint or implementation-time numbers are established by this research.

The immediate engineering step is provider extraction followed by a prepared macOS guest and executor qualification. Windows ARM64 can then reuse the protocol and authority layer; native Windows x64 can use a remote Hyper-V provider. OS installation and desktop polish should follow demonstrated execution and cleanup correctness.
