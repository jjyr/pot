# Embedded Web assets

Server's optional `web-ui` feature is enabled by default. Cargo builds the WASM
client and static resources, then invokes the external Brotli CLI to compress
and embed them. Generated assets are not tracked in Git. Developers use mise's
`conda:brotli-bin` 1.2.0 tool; standalone Cargo users install the CLI themselves.
There is no separate compression feature.

The build cache under `<target>/pot-web/` records source/lockfile and payload
hashes. Valid raw assets skip WASM generation; compression independently reuses
unchanged files. Missing/corrupt files and changed Brotli versions/parameters
invalidate the corresponding cache. Nested Cargo uses an isolated target and a
cache lock serializes concurrent builders. `--no-default-features` does not
invoke frontend or compression tools.

## Compression comparison

Measured on the existing October 9 Web build using gzip level 9, Brotli 1.2.0
quality 11, Terser 5.44.0 (`--compress --mangle --module`) and Binaryen 130
(`wasm-opt -Oz --all-features`). Sizes below are bytes, not binary-file deltas.

| WASM | Original | gzip | Brotli |
|---|---:|---:|---:|
| wasm-bindgen output | 18,420,575 | 5,924,023 | 3,802,289 |
| wasm-opt -Oz output | 15,459,284 | 5,898,958 | 3,964,941 |

Generated JS shrank from 176,528 to 107,402 bytes with Terser, but gzip only
changed from 25,372 to 22,608 bytes. Optimization of the uncompressed WASM saved
little after gzip and made Brotli larger. The selected build therefore uses
Brotli alone; it preserves original JS and license comments and does not add
Terser or Binaryen to the required build tools.

Before the build-cache revision, the October 12 assets totaled **4,749,294 bytes (4.53 MiB)** in Brotli
form. The generated build-cache manifest now records each asset's original/compressed size and
SHA-256. On that same build, the previous raw-plus-gzip layout would
embed 26,691,463 bytes (25.45 MiB), and gzip-only would use 6,936,329 bytes
(6.62 MiB). Brotli cuts asset storage by **82.2%** versus the previous layout.
Native binary growth also includes decoder code and alignment. Measured Apple
Silicon release installs from the same Git snapshot:

| Build | Binary bytes | MiB |
|---|---:|---:|
| Default embedded Web | 38,008,432 | 36.25 |
| `--no-default-features` | 33,020,624 | 31.49 |
| Increment including decoder | **4,987,808** | **4.76** |

Both installations used Rust 1.98.1, the same lockfile and release profile. Other
platforms/toolchains can produce different native-code sizes.

## Behavior

Brotli-capable clients receive the embedded bytes directly. Clients requiring
identity get decoded bytes, cached on first use. Encoding quality values,
explicit exclusions and `Vary: Accept-Encoding` are honored. Clients excluding
both supported representations receive 406. The existing authenticated desktop
asset boundary is retained. `--no-default-features` omits embedded resources and
retains external-directory hosting.

The generated cache fingerprint includes the Web client, shared UI/core/protocol,
localization, desktop templates, icons, Cargo dependency lock and build scripts.
Changing these inputs regenerates the assets on the next build.

## Earlier validation (before build-cache revision)

- Fresh Web/WASM build succeeded; Node's `WebAssembly.Module` validated the generated binary.
- Every asset passed a Brotli compression/decompression round trip.
- Source/payload checks passed; deliberate source and compressed-payload changes in a disposable copy were rejected by the former checked-in-payload validator.
- Server access unit tests: 8 passed, 1 existing Tailscale-library test ignored.
- Public access integration tests: 6 passed, including HTML, JS, WASM, fonts, locales and icons served without an external asset directory.
- No-default-features encoding test passed.
- Server Clippy with all targets and warnings denied passed.
- CLI tests (3) and TLS integration test (1) passed.
- Release `cargo install --git` from a disposable local Git snapshot succeeded with `--locked --bin pot pot-server`; the snapshot had no ignored `dist` directory. The installed binary ran from `/tmp` and reported `web_embedded: true` with no asset directory configured. A second installation with `--no-default-features` succeeded and reported `web_embedded: false`.
- Four license-policy tests passed; the added decoder uses approved BSD/MIT terms.

These checks cover packaging, encoding and the HTTP asset boundary. They do not
claim new browser rendering or remote desktop qualification.

## Build-cache revision validation

- Default `web-ui` Cargo build passed, generating WASM/static assets and invoking the external Brotli CLI.
- A repeated frontend build skipped WASM generation and reused all 101 compressed files (0 recompressed). A subsequent unchanged Cargo check completed in 0.26 seconds.
- `--no-default-features` Cargo check passed without invoking the frontend build.
- Four cache tests passed: unchanged reuse, changed/deleted/corrupt input recovery, compressor-profile invalidation, missing CLI diagnostics and nested-Cargo environment isolation.
- Server access unit tests: 8 passed, 1 existing Tailscale test ignored; access integration tests: 6 passed; CLI tests: 3 passed.
- Release Cargo Git installation from a source-only temporary repository passed, reusing the valid target cache; the installed CLI reported `web_embedded: true` outside the checkout. No `.br` or generated WASM was present in that repository.
- Mise resolves Brotli to its managed `conda-brotli-bin/1.2.0/.mise-bins/brotli`, which reports version 1.2.0 and passes the compression tests. The `conda:brotli` metapackage does not expose the dependency executable in mise, so the configuration uses `conda:brotli-bin` explicitly.

The source-only installation check reused WASM/tool caches; it does not claim
a new empty-machine toolchain download test or browser rendering qualification.
