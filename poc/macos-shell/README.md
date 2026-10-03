# pot macOS shell POC

Requires a Mac desktop session, Xcode macOS SDK, Python 3.9+, Rust 1.88+ and network access for the first Cargo fetch. Validated toolchain: Rust 1.98.1, Swift 6.4, SDK 27, arm64 macOS 27.0.1. Older macOS/Intel are untested.

```sh
./poc/macos-shell/poc doctor
./poc/macos-shell/poc build
./poc/macos-shell/poc run
./poc/macos-shell/poc status
./poc/macos-shell/poc stop
```

Run from a normal macOS terminal. A restricted agent sandbox may block AppKit/LaunchServices or process inspection; use approved desktop execution rather than changing system security settings.

`POT_POC_ROOT` defaults to repository `.local/poc`; override it consistently for every command. Cargo target, development `.app`, run ID, PID state and JSONL logs live there. Cargo's normal registry cache is used; `CARGO_HOME` may optionally select an existing cache. No historical temporary directory is required. `build --locked` is built into the entry point. Swift is statically linked, and the HTML is embedded; no HTTP listener or resource download is needed at launch. `runtime_shaders` avoids requiring the separately downloaded Metal Toolchain.

This development `.app` only provides a native identity and launchable GUI; it is not P4 helper/runtime packaging or a notarized distribution. Build logs may warn about upstream `block` and `proc-macro-error2` future compatibility.

The Dock is visible for Cmd-Tab tests. Click **pot POC → Open pot / Hide pot / Quit pot POC** in the status bar. Close or Cmd-W hides the same window and retains its input; Quit and Cmd-Q exit. `stop` targets only the recorded matching GUI process and sends SIGTERM. This slice starts no helper, VM or server. Text is diagnostic test data and is logged locally; use the supplied test phrases.

Left: actual GPUI text input adapted from the pinned upstream example. Right: ephemeral WKWebView with composition/key/clipboard logs and exact-text comparison. Test Pinyin with individual keystrokes and candidate selection; Unicode typing/paste alone does not test IME. See [validation](../../docs/validation/macos-shell.md) for observations and remaining checks.
