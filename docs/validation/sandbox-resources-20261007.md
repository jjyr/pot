# Sandbox resource delivery acceptance — 2026-10-07

## Implementation under test

`pot-resource` and `pot-sandbox-images`, embedded catalog SHA-256 verification, Runtime-owned approval/persistent bundle pins, native Chat/Sandbox progress cards and cache controls, Provider resource binding, and Linux Helper startup without bundled kernel/OCI layouts. macOS enrollment remains owner-provided.

## Automated results

| Check | Result |
|---|---|
| `mise run test` | 274 passed, 1 previously ignored live-model test; no failures |
| `mise run test:app` | 16 passed |
| `mise run test:ui` | 8 passed |
| `mise run clippy` | All workspace targets passed with warnings denied |
| `cargo check --workspace` | Passed |
| `mise run check:swift` | Both Helper products compiled |
| `mise run lint` | Rust/Swift/Python/Shell and generated web translation checks passed |
| `mise run test:i18n` | 732 keys and native/web locale contracts passed |
| `mise run test:release` | 3 passed, including refusal to package old embedded resources |
| `mise run licenses` | Dependency notices generated successfully |
| `scripts/export-sandbox-resources` | Deterministic exports matched embedded digest/size/format entries |
| `git diff --check` | Passed |

Resource tests use an actual local TLS server with a trusted test certificate and streamed bytes. They verify digest mismatch rejection, one shared transfer with independent cancellation, last-waiter cancellation and partial-file removal. Offline tests reject modified cache content even if the completion marker is changed, symlinked cache files, and archive links/special entries. Catalog tests reject incompatible formats/architecture/schema and unsafe filenames.

Runtime tests verify that model-supplied download/authorize arguments do not start a transfer, preparation waits before receipt dispatch, user decline releases the original live call, references protect cache pruning, and restart marks unfinished preparation interrupted without automatically authorizing or replaying it. Paired-access tests confirm the resource route remains owner-only.

The full workspace has an existing `block` dependency future-incompatibility notice. Tool cache write warnings from the restricted environment did not affect the successful checks.

## Real Linux VM regression

Command:

```sh
mise exec -- python scripts/tests/sandbox-runtime.py \
  --helper .local/d1/helper-build/release/PotHelper \
  --resources .local/validation/image-delivery-resources \
  --resource-assets .local/releases/resources \
  --output .local/validation/image-delivery-linux.json
```

The client resource directory contained Host executables, Host dependencies, skills and web assets, **no `vmlinux`, `init-image`, `sandbox-image` or `sandbox-image.json`**. The fixture used the embedded-catalog importer to verify and provision three Release assets into a private temporary shared cache before starting Helper. This exercises cached provisioning and the new Provider binding/Helper import contract; it does not bypass hashes or substitute bundled paths.

All 18 regression flags passed: scoped settings without starting, lazy boot, two Linux environments, readonly skill mappings/helpers, skill management, sessions retaining assignment across default changes, fixed worker assignment, Host Codex without a Sandbox, Codex resume/loadouts, cgroup cancellation and busy-stop protection, Host/worker restart, independent stop and persistent artifacts, managed desktop RFB/input controls with persistent VM state, and optional desktop/shutdown. Local model fixtures handled 47 requests and 4 summaries; no paid model calls were made.

## Scope and publication limits

- Native UI compiled and presentation/state/localization tests passed. No manual native visual acceptance or online GitHub download through the UI is claimed.
- The initial catalog's exact resources were exported locally (about 531 MiB total download, excluding guest instance storage). `sandbox-resources-v1` assets have **not been published**. Online first-use downloads require that publication; the separate publication workflow verifies bytes and refuses overwriting a release.
- The TLS tests cover network transfer/integrity/cancellation; VM tests use owner/developer offline provisioning. Together they test the two boundaries, not a live GitHub end-to-end first-use download.
- No actual macOS VM was supplied or newly qualified in this delivery task. Existing macOS Provider/helper/bootstrap support is retained; OS images are not distributed.
- Range resume, independent remote catalog updates and in-place instance bundle upgrades are outside this phase. Pins retain existing bundle identities; future exports derive new identities from the entire bundle descriptor, preserving previous entries and unchanged kernel/boot release tags.

## Later publication on the same date

The previously unpublished resource placeholder tag was changed to `sandbox-resources-v0.0.1`, preserving all resource digests and the bundle ID. The exact assets were published and GitHub digests checked; see [publication acceptance](sandbox-resource-release-20261007.md). The repository remains private and anonymous client download is still unavailable.
