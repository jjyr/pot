# Pot dependency patch

Source: crates.io `gpui-fast` 0.1.7, SHA-256
`35cb4c01c92462871c34d560b4ef21b7287a355036afa1a1cafede9c13ce55a0`.
The published Cargo manifest, build script, source, resources, README and Apache
license are retained. Cargo registry metadata and the dependency's lockfile are
excluded.

`src/fast/layers/hover_freeze.rs` imports `scheduler::Instant` instead of
`std::time::Instant`. The scheduler clock supports native and WASM targets;
the standard clock panics on `wasm32-unknown-unknown` during wheel dispatch,
leaving the browser application unresponsive. Hover suppression timing and
pointer movement behavior are preserved.

Two trailing-whitespace lines in `src/_accessibility.rs` documentation examples
are normalized to satisfy the repository whitespace check.

Remove this patch and the Cargo override when adopting an upstream release that
uses a browser-compatible clock here. Web asset fingerprints include this tree.
