# Concrete Android SDK Emulator Provider research

Reviewed: 2026-10-07. Proposal for `pot-sandbox-android` implementing [Sandbox Manager contracts](../sandbox-providers.md). Checked primary Android docs and AOSP ADB source; no SDK/emulator installation, binary execution, app test or isolation measurements. Scope update: Android and iOS are now deferred; this page preserves research for a future device Provider and does not require a first-phase crate or SDK installation.

## Recommended implementation

Use the official SDK Emulator directly on Apple silicon Host with an `arm64-v8a` image. The Rust Provider owns emulator/ADB process clients and typed device adapters; Manager owns identity, policy, capacity, scopes and outcomes. Do not launch the accelerated emulator inside a Linux/macOS VM. Android build work belongs in a separate qualified development environment. [Acceleration](https://developer.android.com/studio/run/emulator-acceleration).

```text
Sandbox Manager
  -> pot-sandbox-android
      -> SDK Emulator process (private AVD data)
      -> private ADB server/client (enrolled transport only)
          -> Android guest
              -> package manager / screenshot / app-scoped logs
              -> qualified UI Automator instrumentation agent
```

## Build environment correction

The earlier Linux-build default is conditional, not established on Pot's Linux ARM64 guest. Android Studio's official installation page excludes ARM Linux, and the official NDK Linux package is x86. These do not prove that no Android build can work on ARM Linux, but they rule out assuming an official complete native ARM Linux SDK/NDK profile. AAPT2 and other host executables need architecture qualification independently of Java/Gradle. [Installation requirements](https://developer.android.com/studio/install), [NDK downloads](https://developer.android.com/ndk/downloads), [AAPT2](https://developer.android.com/tools/aapt2).

Future Android rollout recommendation on this Host: build Android projects inside a prepared macOS VM with JDK + pinned SDK/NDK + project Gradle wrapper; publish a signed debug APK; run device testing in a separate Android environment. Linux builds remain enabled only for a qualified toolchain/project profile. Future x86_64 Linux remote execution or ARM Linux custom toolchains are separate integrations. No silent Host build or worker retargeting.

## SDK preparation and configuration

Proposed modules: `config`, `sdk`, `avd`, `emulator_process`, `adb`, `device`, `automation`, `logs`, `display`. SDK packages/system images may be shared read-only; every Sandbox gets private writable AVD data, keys/config, subprocess state and staging. Do not use the owner's `~/.android` or Android Studio AVD inventory.

Preparation pins the SDK tools, emulator/platform-tools versions, image package/API/ABI, hardware profile, automation package/signature and bootstrap schema. Enroll an existing SDK or download a selected manifest through an explicit preparation operation with license/setup state. Start with one ARM64 Google APIs or AOSP phone profile selected by app requirements; Play Store/login is optional and separately qualified. SDK root binaries used by this Host are macOS binaries, distinct from guest APK ABI and build-VM tools. [sdkmanager](https://developer.android.com/tools/sdkmanager), [avdmanager](https://developer.android.com/tools/avdmanager).

Suggested private layout:

```text
sdk/<version>/                      # pinned Host tools/system images
instances/<sandbox-id>/avd/          # writable guest disks/config
instances/<sandbox-id>/control/      # keys, sockets, process ownership
instances/<sandbox-id>/staging/      # bounded APK/log exports
```

Versioned Provider config includes SDK reference, image/profile, actual CPU/RAM/display settings, cold/quick-boot policy and clean/persistent data policy. Manager resources/policy stay typed separately. Validate executable identities and real supported options; never pass arbitrary argv supplied by an Agent.

## Android CLI evaluation

Official Android CLI is a viable integration candidate, not the retired SDK `android` utility. Current official emulator docs deprecate the older `emulator` tool interface in favor of `android emulator`. The new CLI supplies create/start/stop, screenshot and JSON layout; install/layout support explicit `--device`. [CLI overview](https://developer.android.com/tools/agents/android-cli), [emulator commands](https://developer.android.com/tools/agents/android-cli/commands/emulator), [layout](https://developer.android.com/tools/agents/android-cli/commands/layout), [install](https://developer.android.com/tools/agents/android-cli/commands/install), [screen](https://developer.android.com/tools/agents/android-cli/commands/screen).

Evaluate it behind the Provider for preparation/observation reuse. The reviewed reference does not establish a private ADB server endpoint, owned-process recovery, all resource/port controls, or noninterference with owner devices. `--device` is target selection, not control-service isolation. Therefore the first executable spike can use a pinned low-level SDK emulator launch/client for those controls, with migration to CLI only after equivalent acceptance. Both routes use the official engine. Do not expose CLI argv, `android init`, update/auth/Studio/skill management or shell-string execution to device workers. Disable CLI metrics if used; prevent owner `.androidrc` injection.

## ADB identity and control isolation

Use one private ADB server per Provider control domain initially, with explicit enrolled transport per operation and one emulator owner lease. Host endpoints need owner-only access; prefer a qualified Unix smart socket or otherwise combine loopback authentication/isolation with network policy. Never bind all interfaces. Separate ADB server ports alone are not sufficient isolation from guest/sibling access.

AOSP source inspected at commit `1cf2f017d312f73b3dc53bda85ef2610e35a80e9` shows `ADB_USB=0` suppressing USB initialization and `ADB_EMU=0` suppressing emulator scanning. Use these as candidates for a dedicated server that explicitly connects only its enrolled emulator endpoint. They are source findings, not verified behavior of any installed platform-tools binary. Disable wireless/mDNS discovery separately and qualify connection routing, emulator startup registration and private socket support against the pinned binary. [ADB initialization source](https://android.googlesource.com/platform/packages/modules/adb/+/1cf2f017d312f73b3dc53bda85ef2610e35a80e9/client/main.cpp).

ADB supports selecting serial/transport and a separate server endpoint. Its `--one-device` option is documented for USB and must not be assumed to isolate one emulator. Forward/reverse handles need explicit scope tracking/removal. Never invoke the owner's global `kill-server`, root/remount/security controls or a generic `adb shell` tool. [ADB man page](https://android.googlesource.com/platform/packages/modules/adb/+/1cf2f017d312f73b3dc53bda85ef2610e35a80e9/docs/user/adb.1.md).

Allocate/reserve console/ADB ports with collision recovery, launch only the enrolled AVD, bind the resulting transport to the current Manager epoch, and verify a Provider-installed guest identity plus expected profile. A reused serial/port is not proof of the same guest. Detect unexpected transports and reject routing; cleanup must not disconnect unrelated devices. If startup cannot avoid owner-server/USB interference, keep the integration unqualified rather than silently sharing the global server.

## Lifecycle and readiness

Preparation creates private AVD data and installs the controlled automation package if required. Start owns/reconciles the long-lived emulator process, including child processes; a launcher returning or exiting is not proof the VM is gone. Poll under one deadline: owned process alive -> expected transport connected -> guest boot complete -> package manager responsive -> automation health/identity -> requested capabilities ready. Test actual readiness rather than only reading `sys.boot_completed` once.

Ordinary stop drains scopes/control, requests emulator shutdown, waits for owned emulator exit, then uses declared forced termination if needed. Delete requires stopped state, no unresolved operation and no artifact export in progress; only private AVD state is removed. Restore increments epoch and invalidates display/transport/automation sessions. Initially prefer cold boot and fresh private user data for disposable tests; version-qualified Quick Boot is an optimization. Wiping user data is not the same as deleting all optional state; clean tests use a full enrolled clean template. [Launch/storage](https://developer.android.com/studio/run/emulator-commandline), [snapshots](https://developer.android.com/studio/run/emulator-snapshots).

## Minimum device API

| Pot action | Implementation direction | Scope/limits |
|---|---|---|
| Screenshot | Fixed ADB binary screenshot operation | Bounded PNG; capture ID/epoch/dimensions/orientation |
| UI tree | Qualified UI Automator agent or isolated CLI layout | Bound node count/text; inaccessible/custom surfaces may be absent |
| Tap/swipe/button | Fixed input operations / instrumentation | Coordinates refer to current observation; serialized controller |
| Set text | UI Automator node text operation | Separate Unicode/set-text from simulated typing; qualify IME/password/custom widgets |
| Install APK | Package-manager operation using artifact staging | Granted package/signature/ABI; no raw Host file argument |
| Launch/stop app | Qualified explicit component/package operation | Enrolled app identity; no arbitrary intent extras or shell strings |
| Logs/test reports | Bounded package/test adapter | Handle app restarts/UIDs; do not return global device logs by default |

The initial package format is one signed debug APK. Native libraries must match ARM64. AAB conversion and split APK sets are later or explicitly qualified extensions; an AAB is not directly installable as an APK. Install/update must check signature continuity, declared permissions and app state policy. Do not auto-grant all permissions or clear app data through a hidden install flag.

Use typed argument validation even when ADB launches guest shell primitives: Host argv escaping alone does not prevent guest-shell injection. The optional automation agent accepts only bounded typed actions, not arbitrary shell/method names. Authenticate its session on a private ADB-forwarded endpoint and remove mappings on scope close. Agent credentials remain broker/adapter-private where possible; guest compromise cannot gain Manager authority. UI Automator supplies cross-app UI controls, but tree completeness and text/IME behavior require app-specific acceptance. [UI Automator](https://developer.android.com/training/testing/other-components/ui-automator), [UiDevice](https://developer.android.com/reference/androidx/test/uiautomator/UiDevice).

## Network, human control and cancellation

Default emulator networking exposes Host loopback via `10.0.2.2`. Qualify a Host-enforced egress boundary for control endpoints, private networks and siblings. A proxy or guest firewall alone does not prove isolation. Private Unix control sockets reduce exposure but do not restrict all Host services. A development backend is an explicit endpoint grant. Until a concrete filtering implementation passes direct TCP/UDP bypass tests, classify this as a development device environment, not qualified untrusted-app isolation. [Network address space](https://developer.android.com/studio/run/emulator-networking-address).

Start headless for Agent automation. A raw interactive emulator window would bypass Manager input arbitration; enable it only with exclusive human takeover that pauses Agent input. Screenshots and structured input are sufficient for the initial App panel; streaming/gRPC embedding follows later. Never expose raw console/ADB/gRPC endpoints to the UI/browser.

Cancel stops queued actions and settles instrumentation/ADB work; killing the Host ADB client alone does not prove a guest command or install stopped. If outcome is unconfirmed, record unknown and quarantine, or stop the exclusively held emulator to establish execution termination. Completed actions and app state are not undone. Normal scope close drains automation/input; persistent foreground app state is declared device state. Receipts/artifacts stay outside snapshots; never auto-replay install/input after disconnect/restore.

## Executable spike and acceptance

First prove private server routing/owner-device noninterference and one cold-boot AVD. Then install a known APK artifact, capture a screen/tree, perform one ordered input, verify foreground app and export logs. Add Manager scope/epoch/cancellation and restore tests before automatic setup/catalogs/live streaming.

Measure cold/quick boot, memory/disk/GPU load and concurrent behavior alongside Linux/macOS VMs. Check ADB process/endpoint access, USB/mDNS denial, port collisions, signature/ABI failures, Unicode/rotation/stale input, app restart logs, crashes/unknown outcomes and artifact survival. A working emulator does not establish hardware, Play Integrity or production-account parity. This document establishes a concrete plan and source evidence, not delivered or measured isolation.
