# Sandbox providers

Execution target references distinguish Host execution from managed Sandbox instances;
remote is a relationship to a Runtime. [Architecture](architecture.md) defines
ownership, machine policy and execution records.

## Implemented contracts

Sandbox Manager owns definitions, default selection, explicit Provider registration,
lifecycle serialization, scoped execution, leases, receipts, cancellation and
recovery. Control supplies policy and approval; Runtime supplies execution scopes. Providers supply
platform configuration, workspace paths, lifecycle and authenticated guest transport.
Resource creation registers installed Providers without starting an instance.

| Provider | Host | Execution | Qualification |
|---|---|---|---|
| macos.apple-containerization | macOS | Linux container guest | Local support; see resource/executor acceptance |
| linux.podman | Linux | Rootless Podman guest | Linux-host support; actual remote plugin qualification pending |
| macos.apple-virtualization | macOS | Prepared macOS VM | Implementation present; real VM qualification pending |

Provider descriptors report shell/files/desktop/shared-execution support. These
are execution validation, not model tool-catalog selection. Control's configured
tools stay visible; unsupported calls return explicit errors. No configuration,
model, target or plugin replacement occurs. Worker calls are fixed to their
assigned execution context and cannot manage lifecycle. Same-machine Host exceptions return through Control for authorization.

Start completes only after authenticated execution readiness. Runtime may prepare
the already bound Sandbox; it cannot allocate a different one as fallback.
Stop/delete respect active scopes and uncertain outcomes. Deleted IDs are retired;
workspace artifacts remain. Automatic Tasks reuse the machine-owned shared Linux development environment.
Stop preserves its writable layer; explicit rebuild/delete removes it. Prepared
macOS VM bundles retain their existing operator-managed lifecycle.

## Desktop boundary

Manager owns opaque expiring desktop tickets, boot checks, revocation and exclusive
viewer input. Provider endpoints and credentials stay private. Closing a viewer
does not close task scopes or stop the VM. Server gates input independently of
noVNC viewOnly. Viewer arbitration does not fence all guest-local automation.
Supported local desktop transport is implemented; outward remote streaming through
Control and broader takeover qualification remain in #39.

## Platform and resource details

See [macOS setup](macos-sandbox.md), [resource delivery](sandbox-resources.md),
[image recipes](sandbox-image.md) and [command execution](executor.md).
MacOS VM preparation requires an enrolled compatible instance and qualified toolchain;
IPSW installation media is not a ready development VM. Android, iOS and Windows
Providers, allocation catalogs and automatic template cloning are outside current
implementation. They add no unused public interfaces or tool schemas.

Owner rebuild uses the existing native Sandbox API with `action: "rebuild"`.
It rejects occupied/quarantined environments, resets the writable layer and
shared development tools, and retains Task files and records. Container Providers
implement explicit destruction separately from stop. Stopped retained environments
remain discoverable and can be restarted with their installed software.

Control initialization registers installed Providers without creating instances.
On execution nodes, an authenticated command for the shared environment lazily
registers Providers; no separate Sandbox creation call is needed. Persisted shared
definitions restore registration before lease reconciliation on Runtime restart.
Registration itself neither starts a VM nor downloads assets.
