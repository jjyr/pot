# Sandbox resource delivery

Linux Sandboxes on macOS and Linux consume separately distributed OCI images for
their guest architecture. Podman installation/machine OS preparation is a
[separate prerequisite](deployment.md#linux-sandbox-runtime). Native macOS VMs use
[owner enrollment](macos-sandbox.md), not distributed installation media/disks.

## Trust and ownership

`internal/resource` downloads over HTTPS, checks exact bytes/SHA-256 and safely
extracts bounded OCI archives. It refuses links, special/duplicate files and
unsafe paths; Unix free-space checks precede download. Completed cache entries
publish atomically and are revalidated against the archive, not an editable
marker. Transfers share by digest: waiters cancel independently, and the last
cancellation stops the transfer.

`internal/sandboxresources` consumes embedded `resources/sandbox/catalog.json`.
It pins immutable filenames, sizes, hashes, architecture, OCI identity and
executor bootstrap. Asset URLs derive from the release tag/filename under
`https://github.com/jjyr/pot`. No mutable remote catalog/latest image is consulted.
Hashes protect bytes only while the client/catalog itself remains trusted.

Control owns consent, requests, bundle pins and cache references. Manager passes
verified image paths to Podman, which imports and pins their immutable identity.
Resource binding alone starts no engine, machine or container. Resource readiness
is distinct from Provider lifecycle/readiness.

## Consent, progress and cache

Start/Rebuild requests missing pinned resources. Native owner confirmation shows
the bundle/missing byte count; download starts only after Download and continue.
A prepared cache needs no new consent. After machine Sandbox authorization,
Agent operations wait on the same download confirmation before dispatch. Model
arguments, Worker tokens and published browser sessions cannot authorize it.
`GET/POST /api/v1/sandbox-resources` is a native/private owner interface.

Chat/Sandbox share progress cards with bytes, rate/ETA when meaningful and
cancel/retry/continue. Leaving a page/dialog does not cancel an accepted transfer.
Approval continues the original live call, without replacement execution;
declining ends its wait. Another approved transfer can satisfy that wait.

Requests/pins live in `sandbox-resources.sqlite`; verified files live in
`resource-cache/<sha256>/`. Approval/completion/cancellation checkpoints persist;
byte progress is transient. Restart marks unfinished requests interrupted and
requires explicit continue. It never treats partial files as usable or replays
execution. HTTP range resume is not implemented.

Native cache controls expose bytes/references via `cache.items` and
`cache.preparing`. `POST` with `{"action":"prune","resource_id":"ID"}` removes
only unreferenced entries. Preparation/import blocks pruning; corrupt
unreferenced entries can be removed. Cancellation retains the pin; only confirmed
Sandbox deletion releases it. Failed deletion retains references.

## Pins and versions

The image-only catalog has `schema_version: 2`, per-architecture defaults and
independent resource tags `sandbox-go-resources-vMAJOR.MINOR.PATCH` (currently
`v0.3.0`). Published descriptors/files are immutable. New images get new
identities; client defaults affect newly pinned instances. Existing instances
keep their bundle, including on rebuild. Resource updates do not implicitly
rewrite existing definitions or workspaces.

## Building and releasing

Image build, reviewed export and immutable publication commands are documented in
[Development](development.md#sandbox-image-resources).
