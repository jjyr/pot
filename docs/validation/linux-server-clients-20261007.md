# Linux Server and multiple clients acceptance

Date: 2026-10-07. Scope: standalone Linux Server with embedded Web UI, rootless
Podman execution, explicit host/engine Provider names, and client regressions.

## Implementation

Provider IDs are `linux.podman`, `macos.apple-containerization`, and
`macos.apple-virtualization`. Crates/directories/types identify their host OS and
engine; descriptors report `host_os`, `engine`, and guest `os`. Linux registers
Podman; macOS registers the Apple engines. No aliases or legacy Provider-ID
compatibility code remain. Checked local instance directories had no saved old-ID
inventories requiring migration; task/environment identities are unchanged.

The Podman Provider uses the local rootless CLI without an API daemon, pins an
already-installed image ID, publishes the authenticated executor on loopback,
and mounts environment workspace plus skill sources. It requires cgroup v2 and
systemd delegation. CPU/memory limits are applied; disk quotas are unavailable
(`disk_gib=0`). Desktop is disabled. Container identity and boot generation are
checked before reuse; shutdown removes containers while retaining workspace.

A shared Dockerfile caches package installation separately from executor/assets.
The Apple OCI export build reuses the same installation script. Portable Web icon
assets replace the `sips` build dependency. CLI archives include builtin skills.
The full x86_64 Dockerfile build succeeded on Linux, and a subsequent assets
change reused the package installation layer. The OCI image includes pinned
tini 0.19.0-r3 to reap descendants; Podman's `--init` cannot be combined with the
required `--systemd=always`, so the Provider invokes the image's init explicitly.

## Automated verification

- Portable backend suite: 295 passed, one existing paid-model test ignored.
- `mise run test:clients`: four regression tests passed locally and on Linux.
- Backend Clippy with warnings denied passed; final affected-crate Clippy passed.
- Script/source lint passed; executor lifecycle tests: five passed; skill mount
  tests: ten passed, including retention of rootless locked mount flags.
- Web/WASM build and macOS native release CLI/archive passed. Linux x86_64 release
  CLI with embedded Web assets compiled and ran on the validation host.

`server/tests/multi_clients.rs` exercises production Runtime, Access, durable
storage and real HTTP/SSE listeners with independent signing keys/cookies and a
local mock model. It covers independent pairing, login/logout/revocation and
Server restart; concurrent same-ID deduplication, conflicting payload rejection,
busy/retry and persistent receipts; multiple subscribers, unread subscribers,
disconnect/reconnect, selective revocation and orderly shutdown; and cancellation
of main/task execution from another client with shared outcome visibility.

These tests run in the normal backend test task and Ubuntu CI without Podman.
Task detail/cancel checks exposed an outer Axum wildcard parameter contaminating
internal Runtime path extraction (HTTP 500). Access now clears outer request
extensions before dispatching to the authenticated Runtime router.

## Manual Linux qualification

Host: Arch Linux x86_64, Podman 6.1.3, pasta/passt, systemd user manager and cgroup
v2, existing subordinate UID/GID ranges. Installed Podman dependencies with owner
authorization. Used isolated temporary storage and instance state; existing
Docker/inference services were not changed.

Two separate browser profiles paired as Linux Client A and Linux Client B and
loaded the embedded GPUI Web canvas. After Server restart, both automatically
signed in using their saved client keys. Revoking A made A unauthenticated while
B remained authenticated. B's persisted P-256 private key reported
`extractable=false`. Browser screenshots are local ignored acceptance artifacts.

Rootless execution qualification uses the actual Sandbox Manager and Provider,
without a model service. The initial run exposed a readonly skill bind-remount
failure: inherited `nosuid/nodev` restrictions are locked by the user namespace.
The shared remount code now preserves inherited restriction/atime flags, with a
portable regression test; it does not relax the mount or disable seccomp.

The final qualification checks successful commands and receipt deduplication,
`apply_patch`, live PTY input after normal turn completion, cancellation of
detached `setsid` descendants, workspace persistence after stop/restart, separate
environment workspaces, and removal on Manager shutdown. It also checks that
execution users cannot read broker credentials/ledgers or raw skill sources,
and task skill views reject writes to both user and builtin skills. A stricter
process check found lingering orphan zombies; the pinned init fixes their
reaping in addition to cgroup cancellation.

## Limits

No Podman integration suite was added to CI, as requested. This is a single
Arch/x86_64 manual qualification, not a distribution/kernel/SELinux matrix.
Physical mobile browsers, external HTTPS deployment, push delivery, native
appearance and real-model inference were not exercised here. macOS VM/Xcode
qualification remains pending; existing Apple VM import/export paths were
retained. Podman desktop and disk quotas remain unavailable.
