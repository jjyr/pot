# Sandbox resource delivery

Linux resources are separate from the client package. macOS uses owner-prepared VM enrollment; Pot does not distribute macOS installation media or VM disks.

## Boundaries and trust

`internal/resource` downloads over HTTPS, verifies exact byte count and SHA-256, safely extracts OCI archives, publishes a completed cache atomically, and shares transfers by digest. Every waiter receives progress and can cancel independently; a transfer stops when its last waiter leaves. The private cache revalidates the downloaded blob and extracted bytes against the archive, rather than trusting an editable completion marker. Archive links, special files, duplicate files and unsafe paths are refused; extraction is bounded by the declared size. On Unix, free space is checked before downloading.

`internal/sandboxresources` owns typed bundle descriptors; `resources/sandbox/` owns Linux/macOS recipes and the embedded `catalog.json`. The catalog pins schema, resource tags, immutable filenames, sizes, SHA-256 and compatible bundles (OS, architecture, executor/bootstrap version and OCI references). The repository is `https://github.com/jjyr/pot`; asset URLs are derived from tag and filename. No remote catalog or mutable latest image tag is consulted. A changed asset fails verification even if its download was redirected.

Control resource adapters own approval, resource requests, persistent instance bundle pins and cache references. They call the image service and pass `sandbox.PreparedResources` to `internal/sandbox`, which delegates binding to the Provider. Manager consumes verified resource paths through its Provider interface. The Linux Provider writes a private manifest outside the guest mount. Helper imports only the supplied local OCI layouts and boots the supplied kernel; it does not download resources or fall back to bundled images.

The embedded hash is a trust root only while the client/catalog itself remains trusted. It protects downloaded bytes from replacement; it does not authenticate a malicious client update.

## Interaction

Creating a Linux Sandbox registers a stopped definition without starting a guest or downloading resources. Start/Rebuild requests any missing pinned resources and waits for preparation. A manual native operation shows an OS-native confirmation with the bundle and missing byte count. Downloading happens only after the user clicks Download and continue. Cancelling leaves the Sandbox stopped so preparation can be requested later. A prepared Sandbox uses the shared verified cache without downloading again.

After [machine Sandbox authorization](permissions.md), Agent start/execute operations with missing image resources show a download confirmation and wait before execution dispatch. This confirms the missing bundle download; it is not another command or sandbox access grant. Prepared resources require no download confirmation. Model arguments cannot grant download permission. Native owner controls are the only HTTP authorization boundary: `GET/POST /api/v1/sandbox-resources`. Worker tokens and Token-authorized Web access cannot authorize or prune resources. The original live call continues after preparation, without issuing a replacement tool call; declining/cancelling ends that wait. A different authorized download can satisfy an existing wait through the shared cache.

The Chat and Sandbox pages share permission/progress cards. They show current resource, bytes, rate and ETA when meaningful, with cancel/retry/continue controls. Sandbox list rows show preparation stages. Manual Start/Rebuild also uses an OS-native confirmation dialog. Leaving a page or closing a dialog does not cancel an accepted transfer. Native Settings and Sandbox expose resource cache size and sandbox references; the backend rejects deleting referenced resources or pruning during preparation.

Requests and bundle pins live in `sandbox-resources.sqlite`; shared resources live in `resource-cache/<sha256>/`. Download byte updates are transient; approval/completion/cancellation checkpoints are durable. On restart, unfinished preparation becomes interrupted and requires an explicit continue action. Fully verified resources are retained; unfinished files are not treated as usable. HTTP range resume is not implemented. Prior execution is not automatically replayed after restart.

VM lifecycle progress is still supplied by the Provider (`starting`, `running`, etc.); resource readiness means the kernel and OCI layouts are usable, not that a VM is already running. Persistent workspaces and macOS VM state keep their existing lifecycle.

The native Resource cache page reports stored file bytes and Sandbox references.
`GET /api/v1/sandbox-resources` includes `cache.items` and `cache.preparing`;
explicit `POST` with `{"action":"prune","resource_id":"ID"}` removes only an
unreferenced entry. Preparing transfers/imports block pruning. Cancelling a
request preserves its bundle pin; confirmed Sandbox deletion releases that pin.
Failed deletion retains it. Cache verification errors remain visible while
unreferenced corrupt entries can still be removed. Published browser clients
cannot access resource administration.

## Resource versioning

Go resource release tags use `sandbox-go-resources-vMAJOR.MINOR.PATCH`, independently of the client release. The Go catalog currently targets `sandbox-go-resources-v0.2.0`; retained older Go resource releases remain available for pinned instances. Patch versions cover compatible resource updates; minor versions group new functionality while keeping the declared Provider/bootstrap contract compatible; major versions indicate incompatible preparation/executor contracts. Compatibility is still checked through explicit bundle fields, not inferred from the tag. `schema_version = 1` describes the catalog format and is not a resource release number.

A Release tag locates immutable files; full SHA-256 identifies their contents. Updating only the image creates a new bundle/image resource while kernel and boot can keep their previous Release tags. Published resources and bundle descriptors must not be edited in place. Before publishing an initial, previously unpublished resource, its placeholder release tag may be changed while retaining its digest and bundle identity.

## Building and releasing

1. `mise run prepare` and `mise run check:swift` prepare developer prerequisites.
2. `mise run build:sandbox-image` builds the guest executor and Linux OCI layout independently of client packaging.
3. `mise exec -- python scripts/export-go-sandbox-resources --tag sandbox-go-resources-v0.2.0 --update-catalog` exports gzip/tar assets and explicitly updates the source catalog. Retain these exact reviewed export files and older bundle/resource entries for pinned instances. Existing resource digests retain their original release tag, so unchanged kernel/boot resources need no new release.
4. Review/commit the catalog before building clients. `mise run export:sandbox-resources` can verify another archive export from the same reviewed OCI layout; it fails if bytes, format or sizes differ. Archive export is deterministic for identical OCI inputs. A clean image rebuild is not guaranteed to reproduce those inputs: Go VCS metadata and installation timestamps can change image bytes. Filenames include the digest prefix and must not be reused for different contents.
5. Place the exact reviewed export files in a directory on the self-hosted release runner. `.github/workflows/sandbox-resources.yml` requires its absolute path as `prepared_assets` and the committed resource release tag. It never builds an image or changes the catalog. `scripts/verify-go-sandbox-resources --source ABSOLUTE_EXPORT_DIR --output ABSOLUTE_FRESH_DIR --tag sandbox-go-resources-v0.2.0` selects only catalog assets owned by that tag, verifies size and SHA-256 while copying into fresh private staging, and rejects symlinks and unsafe names. Missing or changed files fail before publication. Older tags select their own retained files; unrelated files are not uploaded.
6. The workflow publishes only the verifier's explicit file list and never overwrites an existing resource release. Client releases no longer build or embed the Linux kernel or OCI layouts. Release packaging rejects obsolete embedded resources.

The repository may require GitHub access for Release downloads. Anonymous client
resource delivery requires an accessible distribution location; publishing a
catalog does not change repository visibility. The Go catalog pins the guest Go
executor image and must be finalized before building client binaries. Resource
publication is an explicit release operation and does not follow from building
or locally verifying an export.

For offline owner/developer provisioning, stop the Runtime using that root and
run `pot resources import -root ROOT -directory RELEASE_ASSETS_DIR`. The command
uses the embedded size, hash and safe extraction checks before binding verified
resources. It provides no model tool or unverified import endpoint.

Automatic independent Sandbox upgrades, remote catalog discovery, signatures, macOS media delivery, Android and iOS remain outside this phase. Existing instances stay on their pinned bundle; changing a client's default affects newly pinned sandboxes. The merged PTY/session executor uses `pot-executor-v2`; a pinned v1 bundle is rejected before resource preparation or execution with an explicit request to create a new Sandbox. Its image and workspace are preserved. Silent upgrades and replay are not performed.
