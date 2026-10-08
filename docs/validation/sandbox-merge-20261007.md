# Sandbox Provider / executor merge acceptance — 2026-10-07

Merged main `2d72194484674562b1a585f17d95fe645f5a4f72` into PR #22. The Provider/resource split and persistent macOS implementation coexist with main's PTY, cross-turn sessions and shell snapshots. Runtime supplies the Agent epoch; `pot-sandbox` owns scope gates, inspection, parking, idle reconciliation and closure. Providers transport authenticated inspection; macOS retains its explicit session-closure policy. macOS bootstrap is now `pot-macos-executor-v3`; older manifests are rejected before execution and require reprovisioning with the current bootstrap.

## Verification

- 291 backend tests passed; one existing live-model test ignored. Runtime doubles cover cross-turn original-target routing, lease parking/reactivation, generation/epoch invalidation and malformed inspection quarantine.
- 16 App and 8 UI tests passed. Workspace check/Clippy (all targets, warnings denied), formatting/script checks, localization (732 keys), and Swift Helper compilation passed.
- Five Linux broker lifecycle tests passed. Local macOS TLS/broker tests passed, including authenticated inspection before/after closure and the desktop tunnel; no real macOS VM was available.

## Image compatibility

The default catalog bundle is `linux-arm64-a8a713299829b6e8`, with executor `pot-executor-v2`, OCI reference `pot.local/sandbox:e191d651a1fef59d4e8e` and manifest digest `sha256:6a47c7610f537063f754a73aa5dae4af48978b22d717ab57ad32488c58b843b2`. Resource locators target `sandbox-resources-v0.1.0`; the unchanged kernel retains its original 0.0.1 locator. The boot OCI content/reference is unchanged; serialization order in exported index metadata changed its archive digest. Older resources and bundles remain in the catalog.

| New asset | Bytes | SHA-256 |
|---|---:|---|
| Boot archive | 68,582,373 | `84ff38f4c43b3aae39fdbf833daaf48e5266213270b0fa000b51872ebd5cfb07` |
| Sandbox OCI archive | 457,472,781 | `d2872eedc59d938db7a9273ff6df206b4afaca694edd871ea99cca400dc79063` |

Exports were checked against the embedded catalog before building the Server/importer used by the VM harness. A legacy v1 pin is rejected before download/dispatch with an instruction to create a new Sandbox; its existing image and workspace remain preserved. A regression confirms no download, Helper startup or silent pin replacement. In-place bundle upgrade remains outside this phase. Resource download authorization and progress are unchanged.

The repository remains private; anonymous external resource download and real macOS/Xcode/VNC qualification remain untested. The previous acceptance reports are historical observations, not rerun claims.
