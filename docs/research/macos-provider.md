# Concrete macOS Provider research

Reviewed: 2026-10-07. Proposal for `pot-sandbox-macos`, implementing the [Sandbox Manager contracts](../sandbox-providers.md). Checked Pot source and primary Apple/upstream documentation. No VM, firmware, Xcode installation, executable prototype or performance/isolation measurement was performed.

## Minimum delivery plan after scope reduction

Current scope: Linux remains the existing/default sandbox, macOS is the only new first-phase Provider, and Android/iOS are deferred. Keep `pot-sandbox` + separate Linux/macOS crates and composition-root registration. Do not build device tooling or require generalized device schemas to deliver macOS.

The minimum end-to-end result is: main assigns a macOS Sandbox -> Manager starts that exact enrolled VM -> worker runs command/stdin/patch operations as the standard guest user -> Xcode builds/tests a Mac sample project -> artifacts are published on Host -> scope cleanup is confirmed -> ordinary restart preserves disk/workspace without replaying old operations.

| Step | Concrete work | Exit evidence |
|---|---|---|
| 1: prepared instance | Create one macOS VM locally through a trusted installer/sample; configure full Xcode and execution account; enroll its complete platform/disk bundle | Guest boots locally; original platform state preserved; no image conversion or cloning assumed |
| 2: helper/lifecycle | Implement per-instance Swift VZ helper with versioned private IPC and `probe/start/inspect/request_stop/force_stop`; wire `pot-sandbox-macos` | Manager distinguishes VM running from execution readiness; confirmed stop; restarting original bundle succeeds |
| 3: guest execution | Install native broker/runner; enroll per-instance credentials through owner-controlled bootstrap; enable authenticated transport | Shell, output, interactive stdin and patch work only for current issued scope; execution user cannot read broker secrets |
| 4: development | Configure private project root, toolchain descriptor, dependency access and artifact export | A Mac sample builds/tests; `.app`, logs and test results remain accessible after VM stops |
| 5: integration/recovery | Bind Main/worker tools, exclusive lease, normal/cancel cleanup, epochs, crash reconciliation and busy lifecycle handling | Worker cannot retarget/manage VM; escape/crash cases are stopped or quarantined; no action replay; Linux regression acceptance passes |

Only after this path works, add product IPSW installation/clone automation, optional VirtioFS, desktop observation/input and embedded UI. A complete template catalog and Tart-format import are not prerequisites. Initial creation may use a manual owner setup step; product enrollment must record provenance and rotate bootstrap credentials before untrusted execution.

Manager allocates/authorizes the enrolled definition. The Provider gets a resolved root/manifest and narrowed operation context, never model-selected Host paths. Helper lifecycle IPC carries request IDs and bounded progress/errors. A prepared manifest records schema/engine, guest build, architecture, original platform-file references, disk reference, resource settings, broker protocol and Xcode profile. Private credentials stay outside public descriptor fields and artifacts.

The first transport is authenticated guest networking; choose and implement one concrete enrollment protocol before the spike. mTLS with a Host-pinned broker certificate and per-instance client credentials is the recommended candidate. Temporary bootstrap is owner-controlled and disabled/rotated after enrollment. Do not ship a shared default password or accept a guest identity solely from IP/hostname. Keep boot identity owned by Manager, independently of Guest responses.

The initial workspace is private native guest storage exposed through bounded file/artifact APIs. Host source inputs are imported as enrolled files/artifacts; do not mount the whole repository home or product data. This keeps Guest development working before shared-directory semantics are qualified. Templates and project VM disks are separate concepts: updating a base does not mutate a project's existing VM automatically.

One running macOS VM accepts one execution/control owner. The UI shows the assigned sandbox, preparation/starting/ready/busy/stopped/unknown state, guest/Xcode version and declared persistence/cleanup policy. A helper-owned external viewer is sufficient; UI tests require the real logged-in guest session and relevant permissions. Basic command builds do not require general screenshot or Accessibility tools.

Persistent disk and confirmed process cleanup are distinct guarantees. Until native containment is demonstrated, the conservative first slice can stop the exclusive VM at normal turn closure as well as cancellation; report this explicitly and allow it only under declared policy. Startup latency, build/cache behavior and GUI usability must be measured before promising a permanently running development sandbox. Published artifacts survive; unsaved application state does not.

## Recommended implementation

Use public Virtualization.framework through a separate signed Swift VM helper. The Rust Provider crate owns the helper client, typed configuration and guest execution adapter; Manager owns grants, reservations, epochs, receipts and recovery. Start with a Pot-format prepared macOS VM and full Xcode. Native Mac builds/tests come first; general desktop automation is a separately qualified capability.

```text
Sandbox Manager
  -> pot-sandbox-macos (Rust lifecycle / execution client)
      -> PotMacOSHelper (Swift, Virtualization.framework, private IPC)
          -> macOS VM
              -> privileged guest broker
                  -> standard-user runner / logged-in GUI execution session
```

| Route | Value | Decision |
|---|---|---|
| Direct public VZ helper | Matches existing Swift integration; owns protocol, lifecycle and packaging | Product engine |
| Tart + Packer | VM/Xcode images, cloning and reproducible preparation already exist | Reference or owner-local preparation prototype; separate format/identity import qualification |
| Lume | Alternative unattended installer/VM tooling | Preparation option, not a required runtime dependency |
| Host process restrictions | Can add defenses to a process | Does not supply an independent macOS guest |

Tart publishes base/Xcode VM images and preparation guidance, but these are full VM images with default credentials. Do not enroll them unchanged or assume disk/config compatibility. Its license and image distribution need independent assessment; the prior [current architecture](../architecture.md) records those limits. [Tart quick start](https://github.com/openai/tart/blob/main/docs/quick-start.md), [Packer templates](https://github.com/cirruslabs/macos-image-templates).

## Rust crate and Swift helper

Proposed modules: `config`, `provider`, `helper_client`, `guest_client`, `workspace`, `display`, `preparation`. Keep VZ/AppKit on the Swift side. Use one helper process per running instance initially for fault attribution and native viewer ownership; actual memory overhead remains unmeasured. Manager coordinates cross-instance capacity. Linux continues using its existing helper rather than loading macOS features into every Linux installation.

IPC uses a private Unix socket under an owner-only directory, bounded messages and version/feature handshake. Validate helper identity/version, enrolled root and peer ownership. Never accept model-supplied Host paths, arbitrary helper commands or argv. Helper exit requires reconciling VM state; if guest termination cannot be proved, quarantine rather than allocate another controller. App and standalone Server share the Rust facade.

The helper requires `com.apple.security.virtualization` and distribution signing appropriate to its bundle. Public macOS virtualization does not require vphone research entitlements or relaxed Host security. Resource minima/compatibility come from the selected restore hardware configuration. [Virtualization framework](https://developer.apple.com/documentation/virtualization).

## VM bundle and preparation

Maintain the macOS source description under proposed `crates/sandbox-images/macos/xcode/` (template metadata, versioned VZ recipe and bootstrap assets). Installed stopped templates and per-project writable VM bundles live under the injected private Sandbox storage root, not inside the Rust Provider crate or Git. Common catalog types are owned by Manager; `pot-sandbox-macos` validates VZ-specific fields. See [template storage](../sandbox-providers.md#platform-and-resource-details).

Proposed private bundle:

```text
instances/<sandbox-id>/
  manifest.json            # versioned engine, guest and toolchain identity
  platform/               # hardware model, machine identifier, auxiliary storage
  disk.img                # private writable installed OS
  helper-state/           # private process/control metadata
workspaces/<sandbox-id>/   # only enrolled project data, if shared
```

Apple's install flow selects a compatible restore configuration and persists hardware model, machine identifier, auxiliary storage and disk. Reloading must restore the original platform values. Distinct VMs require independent identity/auxiliary storage; template cloning must qualify this as a complete platform/disk operation, not blindly copy a disk or regenerate auxiliary state. [Installation](https://developer.apple.com/documentation/virtualization/installing-macos-on-a-virtual-machine), [platform configuration](https://developer.apple.com/documentation/virtualization/vzmacplatformconfiguration).

Preparation pipeline:

1. Enroll a pinned local IPSW or prepared Pot-format bundle; verify build/architecture/digest and host compatibility.
2. Install into a fresh VM, returning progress through Manager preparation operations.
3. Complete first-run account setup. Current macOS 26 baseline uses an owner step or qualified pinned automation; host and guest macOS 27+ can use public first-boot provisioning. This API does not reconfigure an existing account. [Provisioning options](https://developer.apple.com/documentation/virtualization/vzmacguestprovisioningoptions).
4. Bootstrap full Xcode, guest broker/runner, standard execution account, optional logged-in GUI session and workspace/skill permissions. Accept applicable installation terms through the authorized preparation workflow.
5. Qualify broker identity, tools, scopes and cancellation. Freeze a stopped, versioned template without product credentials, signing keys or developer login.
6. Clone into private state, establish independent platform identity and fresh broker/account credentials, then verify health before enabling execution.

Installer cancellation uses its progress cancellation mechanism. Apple's installer documentation warns that pausing/stopping the VM during installation has undefined behavior; generic lifecycle `stop` must not be used to cancel installation. Record partial preparation distinctly and resume/recreate only under a qualified policy.

Do not promise template import from Tart/Lume until their manifest, disk, platform and auxiliary-state conversion is tested. Automated installation follows a working prepared-guest slice.

## Xcode development profile

Pin guest OS build, full Xcode version/build, selected developer directory, SDK set, bootstrap version and runner protocol. Command Line Tools alone do not supply the full Xcode tool suite. Validate `xcodebuild -version`, `xcrun --find swift`, selected SDKs and a sample project build inside the guest. [Xcode tools](https://developer.apple.com/documentation/xcode/xcode-command-line-tool-reference).

Use private project source, DerivedData and dependency state. Native guest storage is the default for build outputs/caches; an optional VirtioFS share exposes only the enrolled project root. Sharing UID/GID semantics differ from a guest-private filesystem, so control credentials and ledgers never live on a share. Measure build behavior before relying on shared storage for all workloads. [VirtioFS configuration](https://developer.apple.com/documentation/virtualization/vzvirtiofilesystemdeviceconfiguration), [permission behavior](https://developer.apple.com/documentation/virtualization/vzvirtiofilesystemdevice).

Typical artifact workflow: resolve dependencies -> build -> run tests -> export `.app`, logs and `.xcresult`. Use a unique output/result path per operation. Mac GUI/UI tests run as the standard user within its actual graphical login session; a root launchd daemon is not a substitute. Test plans/schemes are project inputs, not arbitrary privileged broker settings. [Test results](https://developer.apple.com/documentation/xcode/running-tests-and-interpreting-results).

Default development builds have no owner distribution keys. Project-specific ad hoc/local test signing may be necessary; disabling signing globally is not assumed to work. Developer account, provisioning profiles, keychain access and release signing are explicit additional grants, with separate acceptance.

## Guest execution transport

First qualification uses a bundled native broker with an authenticated guest-network transport (for example mTLS TCP) and credential-free Rust runner. Pin the guest identity to enrollment; discovering an IP is not identity. Define a Manager-private bootstrap/enrollment channel, provision broker credentials on private guest disk, and generate per-instance credentials before running untrusted commands. SSH with verified Host keys is a preparation option, not a model-visible Host command tool.

Keep the shell/stdin/patch API compatible while removing Linux-specific paths, `/proc`, cgroups and system-Python assumptions. Run tools as the standard user with a sanitized environment; root broker policy must reject arbitrary privilege escalation and execution-account access to broker secrets. Host Helper IPC and guest broker transport are separate protocols.

VSOCK is a candidate optimization. Apple's generic Virtio socket configuration documents the Host device, not proof of a working macOS guest-side API for this broker. Do not make first-phase delivery depend on it without a real authenticated echo/health test. [Socket configuration](https://developer.apple.com/documentation/virtualization/vzvirtiosocketdeviceconfiguration).

## Cleanup, display and network

Use exclusive execution ownership initially. macOS process groups are best-effort cleanup, not Linux cgroup containment: escaped/delegated work remains a concern. Manager first requests broker cleanup; if termination is unconfirmed, stop the exclusively held VM and wait for confirmed stopped state. Normal scope closure needs the same proof; until stronger containment is qualified, that can require ending the VM at turn completion as well. Disk persists, but unsaved GUI state and active builds do not. Persistent background execution is outside this first slice.

VZ `requestStop` asks the guest to shut down; its return is not shutdown completion. Apply a bounded graceful wait, then authorized destructive `stop`, which completes after shutdown or error. Preserve unknown execution outcomes regardless of VM termination. [Graceful stop](https://developer.apple.com/documentation/virtualization/vzvirtualmachine/requeststop()), [destructive stop](https://developer.apple.com/documentation/virtualization/vzvirtualmachine/stop(completionhandler:)).

Start with a helper-owned external `VZVirtualMachineView` window. The view needs the same process's VM object; it cannot be embedded in the Pot App through a URL. Guest screen capture/Accessibility automation is independent and requires separately qualified login/TCC permissions. Full desktop capture/tree/input is not needed to prove `xcodebuild` development first. [VM view](https://developer.apple.com/documentation/virtualization/vzvirtualmachineview).

Network ingress to the broker is authenticated. Egress policy must restrict Host product/helper endpoints and unrelated guests; VZ NAT alone is not a tested policy. Dependency downloads need explicit permitted internet access. Signing/cloud accounts remain owner grants.

## First executable spike and acceptance

Implement in this order: Rust lifecycle client + Swift helper for one prepared bundle; broker authentication + simple command/patch; full Xcode sample build/test and artifact export; cancellation including detached processes/delegated launchd work; external viewer; clone/provisioning.

Acceptance evidence must cover cold/warm start, peak memory/disk, Xcode compatibility, native/shared workspace permissions, unreadable secrets, busy-stop, process escape attempts, helper/broker crashes, normal/cancelled scope cleanup, restart without command replay and artifact survival. Resource presets remain measured configuration decisions, not estimates presented as performance guarantees. Existing Linux acceptance must remain valid. This document makes no claim of delivered macOS support.
