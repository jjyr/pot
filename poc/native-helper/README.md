# Swift helper / independent local App POC

Requires Apple Silicon, macOS26+, Xcode/Swift6.2+ and Rust for **building**. The generated App uses bundled native executables and system frameworks; it does not run host Python, cctl, Docker or a development shell. First VM preparation downloads pinned OCI init/Alpine images and locked APK packages; it is not an offline distribution.

```sh
poc/native-helper/poc prepare
poc/native-helper/poc build
poc/native-helper/poc bundle
poc/native-helper/poc open
```

Default build root: repository `.local/poc` (`POT_POC_ROOT` override). `prepare` verifies the Kata archive/kernel and noVNC SHA256. `POT_KERNEL_PATH` can supply an already downloaded kernel, still checksum verified. Build uses committed Swift Package.resolved. App: `.local/poc/pot Local POC.app`.

Move the whole App anywhere, then open it normally. Click **Start local desktop**. Helper status appears at left; after VM boot and package setup, WKWebView loads the common broker/viewer. Use Take over / Return control and Chinese Clipboard fallback as in P3. Stop terminates the VM; closing the window retains it; Quit terminates the owned helper/VM. Default runtime data: `~/Library/Application Support/pot-poc`; `POT_LOCAL_ROOT` is a test override when launching the executable directly. Do not point it at unrelated data. `workspace/profile` and workspace files persist; generated VM rootfs is disposable. An APK cache avoids re-downloading after the first start.

Helper protocol: mode0600 Unix socket `<runtime-root>/helper.sock`, one newline-delimited JSON request/response per connection, `id` echoed, methods `prepare/start/status/stop/shutdown`. Start is asynchronous and idempotent while starting/running/stopping. Guest failure is visible; helper failure asks the user to reopen App. Parent death watchdog and SIGTERM stop owned resources. The helper holds a root-wide flock, cleans only its fixed `pot-local-poc` container path, and recovers its stale socket after process death.

For diagnostics: `python3 poc/native-helper/client.py '<runtime-root>/helper.sock' status`; logs in `helper.log`, `workspace/desktop.log`, `workspace/logs/`. Python is only a developer diagnostic tool.

`verify_lifecycle.py` runs **real VMs with probe.sh**, not a desktop fixture: duplicate start, cancellation, nonzero guest exit, helper SIGKILL/recovery, missing generated initfs and missing kernel. It uses isolated `.local/poc/fault-runtime` and packaged helper. Run after prepare/build/bundle, from repo root. Desktop and relocated-App acceptance are separately recorded in `docs/validation/native-helper-bundle.md`.

Only ad hoc signing is implemented. Developer ID, notarization, clean-machine Gatekeeper acceptance, Intel/older macOS, complete download interruption matrix and malicious guest isolation are not certified by this POC. Guest broker is exposed only on the private vmnet subnet and requires a random session token; raw VNC/CDP/websockify bind guest loopback. This is a single-owner development prototype.

Known limitation: normal Quit cleans helper/VM but Chromium profile still reports `Crashed`; clean browser shutdown is not yet validated. Saved test-page markers survive restart. See the validation report before treating this as production persistence.
