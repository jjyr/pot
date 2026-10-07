# Virtual iPhone provider research

Current priority: iOS is deferred beyond phase one, which now targets Linux and macOS. The integration recommendations below are future research, not required delivery gates.

Reviewed: 2026-10-07. Upstream: [Lakr233/vphone-cli](https://github.com/Lakr233/vphone-cli), commit `f7556452e5443d7af6bb0da11e930da074974f9d`. Reviewed documentation and source from a shallow checkout; no binaries were built/run, no firmware was downloaded and no host configuration was changed. Upstream results below are reported observations, not Pot qualification. The proposed product contract is [Sandbox providers](../sandbox-providers.md).

## Technical fit

Upstream implements an iOS research VM on a physical Apple silicon Mac. It uses Virtualization.framework plus private PV=3/PCC hardware configuration, research entitlements, paired iPhone/cloudOS firmware and patching. This is neither Xcode Simulator nor a normal publicly supported iOS guest API. The source has useful device automation already; Pot need not invent its own firmware patch pipeline for the first adapter.

The upstream README requires macOS 15+, online restore signing and owner-adjusted host security policy. Research guests do not run inside a nested macOS VM. Pot's current macOS 26 baseline is higher, but that alone does not satisfy private-entitlement admission. [Host prerequisites](https://github.com/Lakr233/vphone-cli/blob/f7556452e5443d7af6bb0da11e930da074974f9d/Documents/Guides/host-setup.md), [hardware model implementation](https://github.com/Lakr233/vphone-cli/blob/f7556452e5443d7af6bb0da11e930da074974f9d/VPhoneExecutable/VPhoneVirtualization/UI/VirtualMachine/VPhoneVirtualMachineHardwareModel.swift).

## Integration surface

`VPhone.bundle` contains an unentitled CLI, a separately entitled VM/window process, admission tooling and signed guest resources. Upstream explicitly recommends launching the intact bundle as a process rather than treating it as a dynamic plugin. Updates change the VM binary's cdhash and can require renewed AMFI admission. Its runtime needs no Xcode/Python/Homebrew; building the guest daemon requires the iPhoneOS SDK. [Bundle integration](https://github.com/Lakr233/vphone-cli/blob/f7556452e5443d7af6bb0da11e930da074974f9d/Documents/Guides/bundle-integration.md).

Launch supports headless mode and an optional loopback HTTP/WebSocket proxy with an OS-selected port. The guest `vphoned` uses HTTP over VSOCK 1339. The proxy generates a per-launch bearer and authenticates before forwarding. Pot should retain this credential inside the provider adapter and redact serial/CLI logs, which can otherwise print it. A health daemon-instance UUID changes when the daemon restarts; this is not a complete VM boot/restore epoch.

The daemon exposes screenshot, accessibility tree, touch/gesture/text, app launch/terminate/install, file transfer and device state. It also exposes far more privileged functions: launchd, keychain, security changes and guest reboot. Pot needs a typed allowlist, not a generic RPC passthrough. No equivalent to Pot's scoped pipe executor, cgroup cleanup or durable replay-safe execution is established by the reviewed API catalog. [API contract and methods](https://github.com/Lakr233/vphone-cli/blob/f7556452e5443d7af6bb0da11e930da074974f9d/Research/vphoned_http_api.md).

## State, firmware and storage

Pin the iPhone build, cloudOS build, patch preset/receipt, bundle version and VM schema together. Upstream reports that its usable cloudOS pairing contains a board absent from newer cloudOS releases; “latest firmware” is not a compatibility policy. Existing VM formats may be rejected rather than migrated. [Upstream compatibility matrix](https://github.com/Lakr233/vphone-cli/blob/f7556452e5443d7af6bb0da11e930da074974f9d/Documents/Guides/compatibility.md).

Upstream stopped-VM snapshots jointly save disk, SEP storage and NVRAM; restoring only a disk can break secure-state consistency. APFS is required for its snapshot implementation. Clones can retain machine/network identity unless a new identity is requested. Pot must increment its own durable epoch after restore/recreate and reconcile unknown operations outside the VM. [Snapshot contract](https://github.com/Lakr233/vphone-cli/blob/f7556452e5443d7af6bb0da11e930da074974f9d/Documents/Guides/snapshots.md).

The bundle integration document describes host output permissions of 0777. Do not put Pot control credentials or product state under that library. A Pot-owned private parent directory, library ownership checks and restrictive adapter secret storage need separate testing. Owner-imported VM definitions must reference only enrolled library entries, never model-supplied host paths.

## Network boundary

Upstream supports NAT, bridged, tunnel and no-network modes. Its tunnel forwards guest gateway connections to Mac loopback; this can expose Pot's integration services to the guest. Use `none` for initial qualification because control uses VSOCK independently of guest internet. Online operation requires a separately qualified egress path blocking Host control endpoints, sibling guests and private destinations according to policy. Do not label NAT alone as an egress sandbox. [Upstream networking](https://github.com/Lakr233/vphone-cli/blob/f7556452e5443d7af6bb0da11e930da074974f9d/Documents/Guides/networking.md).

## Packaging and isolation status

The top-level project is MIT, but vendored restore/recovery components contain LGPL notices. A top-level license does not describe the entire bundle. Start with an owner-installed external provider and imported guest; redistribution and dependency inventory remain separate work under [Pot's license policy](../licenses-and-about.md). Firmware licensing is also independent of source licensing. [Recovery component license](https://github.com/Lakr233/vphone-cli/blob/f7556452e5443d7af6bb0da11e930da074974f9d/VPhoneExecutable/VPhoneCommand/VPhoneRestore/MobileRecoveryCore/COPYING).

Private APIs, relaxed host debug restrictions and a patched guest mean Pot cannot inherit the Linux provider's isolation claims. Represent this as `research` assurance in the provider descriptor, with explicit host preparation status. Do not copy private entitlements into Pot's main executable or normal Linux/macOS helper. Host preparation is owner-controlled and absent from Agent tools.

## Future qualification slice (deferred)

Enroll an already prepared guest. Prove preflight/status, start/stop identity, authenticated health, screenshot and UI tree, one ordered touch/text action, app launch/foreground verification, screenshot artifact export, disconnect/timeout behavior and per-device exclusivity. Test proxy credential redaction and denial of raw lifecycle/keychain/launchd RPC. Only then expand app/file capabilities or provisioning. Virtual device functionality does not establish real-device parity for hardware, protected services or app authentication.
