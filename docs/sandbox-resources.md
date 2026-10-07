# Sandbox resource delivery

Updated: 2026-10-07. Linux resources are separate from the client package. macOS uses owner-prepared VM enrollment; Pot does not distribute macOS installation media or VM disks.

## Boundaries and trust

`pot-resource` (`resource/`) downloads over HTTPS, verifies exact byte count and SHA-256, safely extracts OCI archives, publishes a completed cache atomically, and shares transfers by digest. Every waiter receives progress and can cancel independently; a transfer stops when its last waiter leaves. The private cache revalidates the downloaded blob and extracted bytes against the archive, rather than trusting an editable completion marker. Archive links, special files, duplicate files and unsafe paths are refused; extraction is bounded by the declared size. On Unix, free space is checked before downloading.

`pot-sandbox-images` (`sandbox-images/`) owns template descriptions, Linux/macOS recipes and the embedded `catalog.toml`. The catalog pins schema, resource tags, immutable filenames, sizes, SHA-256 and compatible bundles (OS, architecture, executor/bootstrap version and OCI references). The repository is `https://github.com/jjyr/pot`; asset URLs are derived from tag and filename. No remote catalog or mutable latest image tag is consulted. A changed asset fails verification even if its download was redirected.

Runtime owns approval, resource requests, persistent instance bundle pins and cache references. Runtime calls the image service and passes `PreparedResources` to `pot-sandbox`, which delegates binding to the Provider. `pot-sandbox` has no dependency on either new crate. The Linux Provider writes a private manifest outside the guest mount. Helper imports only the supplied local OCI layouts and boots the supplied kernel; it does not download resources or fall back to bundled images.

The embedded hash is a trust root only while the client/catalog itself remains trusted. It protects downloaded bytes from replacement; it does not authenticate a malicious client update.

## Interaction

Manual Linux creation first records an environment awaiting preparation, then shows an explicit download confirmation with bundle and missing byte count. Downloading happens only after the user clicks Download and continue. Cancelling leaves the environment stopped so preparation can be requested later. A prepared environment uses the shared verified cache without downloading again.

Agent create/start/execute operations create a product-generated permission card and wait before execution dispatch. Model arguments cannot grant download permission. Native owner controls are the only HTTP authorization boundary: `GET/POST /api/v1/sandbox-resources`. Worker tokens and paired Mobile access cannot authorize or prune resources. The original live call continues after preparation, without issuing a replacement tool call; declining/cancelling ends that wait. A different authorized download can satisfy an existing wait through the shared cache.

The Chat and Sandbox pages share permission/progress cards. They show current resource, bytes, rate and ETA when meaningful, with cancel/retry/continue controls. Sandbox list rows show preparation stages. Manual creation/start also uses a native confirmation dialog. Leaving a page or closing a dialog does not cancel an accepted transfer. Native Settings and Sandbox expose resource cache size and environment references; the backend rejects deleting referenced resources or pruning during preparation.

Requests and bundle pins live in `product/sandbox-resources.json`; shared resources live in `resource-cache/<sha256>/`. Download byte updates are transient; approval/completion/cancellation checkpoints are durable. On restart, unfinished preparation becomes interrupted and requires an explicit continue action. Fully verified resources are retained; unfinished files are not treated as usable. HTTP range resume is not implemented. Prior execution is not automatically replayed after restart.

VM lifecycle progress is still supplied by the Provider (`starting`, `running`, etc.); resource readiness means the kernel and OCI layouts are usable, not that a VM is already running. Persistent workspaces and macOS VM state keep their existing lifecycle.

## Resource versioning

Release tags use `sandbox-resources-vMAJOR.MINOR.PATCH`, independently of the client release. The first resource release is `sandbox-resources-v0.0.1`. Patch versions cover compatible resource updates; minor versions group new functionality while keeping the declared Provider/bootstrap contract compatible; major versions indicate incompatible preparation/executor contracts. Compatibility is still checked through explicit bundle fields, not inferred from the tag. `schema_version = 1` describes the catalog format and is not a resource release number.

A Release tag locates immutable files; full SHA-256 identifies their contents. Updating only the image creates a new bundle/image resource while kernel and boot can keep their previous Release tags. Published resources and bundle descriptors must not be edited in place. Before publishing an initial, previously unpublished resource, its placeholder release tag may be changed while retaining its digest and bundle identity.

## Building and releasing

1. `mise run prepare` and `mise run check:swift` prepare developer prerequisites.
2. `mise run build:sandbox-image` builds the guest executor and Linux OCI layout independently of client packaging.
3. `mise exec -- python scripts/export-sandbox-resources --tag sandbox-resources-v0.0.1 --update-catalog` exports deterministic gzip/tar assets and explicitly updates the source catalog. Retain older bundle/resource entries for pinned instances. Existing resource digests retain their original release tag, so unchanged kernel/boot resources need no new release.
4. Review/commit the catalog before building clients. `mise run export:sandbox-resources` verifies a regenerated export against it; it fails if bytes, format or sizes differ. Filenames include the digest prefix and must not be reused for different contents.
5. `.github/workflows/sandbox-resources.yml` is an explicit separate resource publication workflow. It verifies assets before publication and never overwrites an existing resource release. Client releases no longer build or embed the Linux kernel or OCI layouts. Release packaging rejects obsolete embedded resources.

The initial assets were published as [sandbox-resources-v0.0.1](https://github.com/jjyr/pot/releases/tag/sandbox-resources-v0.0.1) on 2026-10-07. All three runtime sizes and full SHA-256 values were checked against GitHub asset metadata before publication. The repository is currently private, so these URLs require GitHub access; the current anonymous client downloader still needs a public resource distribution location for external use. Repository visibility was not changed. See [publication acceptance](validation/sandbox-resource-release-20261007.md). Resource catalogs must never be changed after their client binaries are built.

For offline owner/developer provisioning, build `pot-resource-import` from `pot-sandbox-images`, then run `pot-resource-import CACHE_ROOT RELEASE_ASSETS_DIR`. It uses the same embedded sizes, hashes and extraction validation; it is not an Agent tool or an unverified path import endpoint. Real VM tests use this route with isolated temporary caches.

Automatic independent Sandbox upgrades, remote catalog discovery, signatures, macOS media delivery, Android and iOS remain outside this phase. Existing instances stay on their pinned compatible bundle; changing a client's default affects newly pinned environments.
