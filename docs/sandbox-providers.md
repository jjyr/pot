# Sandbox providers

Environment references distinguish Host execution from managed Sandbox instances;
remote is a relationship to a Runtime. [Architecture](architecture.md) defines
ownership, exact configuration and fixed Task targets.

## Implemented contracts

Sandbox Manager owns definitions, default selection, explicit Provider registration,
lifecycle serialization, scoped execution, leases, receipts, cancellation and
recovery. Runtime supplies owner approval and task bindings; Providers supply
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
assigned typed Environment and cannot manage lifecycle or switch targets.

Start completes only after authenticated execution readiness. Runtime may prepare
an already bound environment; it cannot allocate a different one as fallback.
Stop/delete respect active scopes and uncertain outcomes. Deleted IDs are retired;
workspace artifacts remain. Cooperative tasks can share a Sandbox but separate
directories do not provide mutual isolation.

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
