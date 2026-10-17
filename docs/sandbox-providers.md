# Sandbox providers

Sandbox Manager owns definitions, selection, lifecycle serialization, scopes,
leases, receipts and recovery. Control supplies policy/approval; Providers own
workspace paths, lifecycle, image catalogs, guest bootstrap and authenticated transport. Installed packages are discovered through the [Plugin packages](plugins.md).
Discovery handshakes with host processes but starts no guest or download. [Architecture](architecture.md) defines execution ownership.

| Provider | Host | Execution |
|---|---|---|
| `podman` | macOS or Linux | Linux containers using the same architecture-specific Debian OCI image |
| `macos.apple-virtualization` | macOS | Owner-prepared native macOS VM using the signed Swift Helper |

Linux uses local rootless Podman with cgroup v2/systemd. macOS uses a dedicated
Podman machine per Runtime execution root, selected explicitly instead of the
user's default connection. Containers share its Linux kernel/storage; Tasks in
a shared container are not mutually untrusted security boundaries. Swift owns
only native macOS VMs; see [enrollment](macos-sandbox.md).

On the dedicated macOS FCOS engine, container SELinux type `container_engine_t`
permits the broker's scope mounts while retaining MCS labels and machine-wide
SELinux enforcement.

## Machine and mount boundaries

The macOS machine shares `podman-shared`, exact trusted Skill roots and explicit
mapping roots fixed at first initialization. Product databases, credentials, the
complete execution root and the owner's home directory stay outside it.
Containers receive their workspace, role-specific Skill mounts and explicit
owner mappings. A later mapping outside pinned machine shares fails; Pot never
restarts sibling containers to expand those shares. Machine capacity, private
paths and runtime prerequisites are documented in
[Deployment](deployment.md#linux-sandbox-runtime).

## Lifecycle and cleanup

Start waits for authenticated readiness of the bound Sandbox, with no replacement
or Host fallback. Descriptors validate capabilities without hiding model tools;
unsupported calls fail explicitly. Workers cannot manage lifecycle. Same-machine
Host exceptions return through Control for authorization.

Task completion/cancellation drains only that execution's exact process scope,
never sibling Tasks or the shared desktop. Unknown cleanup retains quarantine.
Owner stop/rebuild/delete require admission checks for active leases, unsettled
receipts and retained references. Stop keeps the writable container; rebuild
resets installed software/shared toolchains while retaining Task files/records.
Deleted IDs are retired. See [execution lifecycle](executor.md#shared-machine-execution-environment).

Per-Sandbox stop/delete targets only its container. Orderly Runtime shutdown
stops owned containers, proves that the dedicated engine has no running
containers, then stops the macOS machine. Uncertain cleanup or a remaining
foreign container returns an error and leaves the machine running.

## Desktop boundary

Manager owns expiring desktop tickets, boot checks, revocation and exclusive
viewer input; endpoints and credentials stay private. Server gates input
independently of noVNC viewOnly. Closing a viewer does not close Task scopes or
stop the environment. Viewer arbitration does not fence all guest automation.
Desktop viewing is local to the exact Runtime; remote streaming through Control
remains outside the current transport contract. Layout, fonts, theme and
interaction rules are shared across hosts; see [image contents](sandbox-image.md).

Linux image preparation uses [verified resource bundles](sandbox-resources.md).
Platform expansion and automatic template cloning are covered by
[product scope](development-plan.md), separately from these Provider contracts.
