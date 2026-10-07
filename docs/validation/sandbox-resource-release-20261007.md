# Sandbox resource 0.0.1 publication — 2026-10-07

Published [Sandbox resources 0.0.1](https://github.com/jjyr/pot/releases/tag/sandbox-resources-v0.0.1) at 11:33:50 UTC. Resource Release tags are separate from client versions; this tag does not trigger the client `v*.*.*` workflow and was not marked latest.

The embedded catalog, export script default, publication workflow and documentation now use `sandbox-resources-v0.0.1`. The old placeholder was never published; changing its locator preserved all digests, filenames and the existing bundle ID. Client binaries were rebuilt after the embedded catalog change.

| Runtime resource | Bytes | SHA-256 |
|---|---:|---|
| Kernel | 30,423,552 | `fb2cfb79eb1ae19447a85d75682d7fa5cfec97e24beb2609a492b806e8072c8d` |
| Boot/vminit OCI archive | 68,582,381 | `37408b7c74d619f6f443cc257661fcb4bbfd2a04f950d2fe7215a166f1ddae0f` |
| Linux Sandbox OCI archive | 457,403,044 | `c83a5caebe3b9895d59d41bcbf93ecdbed2a971d52e591e6286a960c714bed4d` |

Total runtime download: 556,408,977 bytes, about 531 MiB. Additional small assets are `catalog.toml`, `SHA256SUMS` and `provenance.json`.

Assets were freshly exported deterministically, checked locally against the embedded catalog, uploaded as a draft, and all six GitHub asset SHA-256 values and byte counts matched the local files before publication. The final release is not a draft and the Git tag resolves to `2d72194484674562b1a585f17d95fe645f5a4f72`, the existing remote main commit. The release notes explicitly state that this Git ref is not a complete source snapshot of the locally validated working-tree build. `provenance.json` records the local base commit, image source fingerprint `026f1eea024aed72883e`, and OCI manifest digest.

Validation: image catalog/template tests passed; Server/App binaries rebuilt successfully; exporter formatting and `git diff --check` passed. Resource bytes are unchanged from the earlier [Linux VM acceptance](sandbox-resources-20261007.md), so no new VM regression was needed for the locator-only change.

The repository remains private. Authenticated GitHub API access verified publication and asset metadata; anonymous end-user downloads were not qualified and the current client downloader has no private GitHub authentication. A public resource hosting location is required for external clients. No repository visibility, client-release publication, source push or unrelated local changes were performed.
