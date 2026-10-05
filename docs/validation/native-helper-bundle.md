# P4: Swift Helper, local VM, standalone App

> Old `poc/` sources are removed; commands apply only to the linked historical commit.

> Historical acceptance record: versions, allowances, UI reflect section dates. See [Development plan](../development-plan.md) / [Documentation index](../README.md) for current capabilities/contracts.

Date: 2026-10-03. **Limited pass**. Owned Swift Helper links Containerization and starts/operates/stops local desktop from relocated App, without user Docker/host Python/cctl. This is a local ad hoc prototype, not public distribution acceptance.

## Reproduction, versions, resources

Full entry: [README](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/native-helper/README.md):

```sh
poc/native-helper/poc prepare
poc/native-helper/poc build
poc/native-helper/poc bundle
poc/native-helper/poc open
```

Default `.local/poc/pot Local POC.app`. Move entire App, open, click **Start local desktop**, wait for viewer. First pull/install observed minutes, warm APK tens of seconds, not guarantees. Stop ends VM; window close retains environment; Quit cleans Helper/VM. Default data `~/Library/Application Support/pot-poc`, persistent workspace/profile. Machine record: `~/Workspace/Machines/machines/local-mac-m4/pot-macos-shell-20261003.md`.

Pinned Containerization 0.47.0 commit `bc994b88df46207fad7775b0eabc51947e315881`, transitive Package.resolved; entry verifies Kata 3.32.0/noVNC 1.6.0 SHA256; Alpine/init digests in Swift; 294 exact APK versions in packages.lock. Chromium 142.0.7444.59/TigerVNC 1.15/aiohttp 3.11.15. App-relative resources; initial OCI/APK needs network, not offline bundle.

Old experiment caches reused to save downloads, without hardcoded source/default dependency. Fresh Mac cold build/long-term old APK availability untested.

## Observed evidence

| Scenario | Status | Evidence / scope |
|---|---|---|
| Owned Helper IPC + real VM | Pass | initial-helper.jsonl; mode-0600 Unix socket, request IDs, async start/status/stop without cctl |
| Relocated standalone App | Pass | `.local/poc/relocated/pot Local POC.app` opened by LaunchServices; native Start shows VM; deep/strict signing, no Helper development-path libraries |
| Shared viewer/driver/takeover/handback | Pass | native-local-returned.json exact `P4 本地桌面 中文 123`; final-native-returned.json exact `P4 最终恢复 中文 456`; native Clipboard/canvas keys/save |
| Profile across restart | Pass | Native viewer shows same session/saved text |
| Repeated start / stop during startup | Pass | lifecycle.json real minimal probe VM, same generation; not GUI workload substitute |
| guest exit42 / Helper SIGKILL / restart | Pass | lifecycle.json; explicit failed, stale socket recovery, successful restart |
| Missing generated initfs / kernel | Pass | Initfs rebuilt from pinned cached OCI; kernel gives explicit error; registry interruption untested |
| Invalid data root | Pass | invalid-root.json: ordinary file as directory, clean exit 1; permission-denial matrix untested |
| GUI SIGKILL / parent watchdog | Pass | gui-crash.json active desktop crash; Helper exits/socket removed, about 2.54 seconds cleanup, old desktop no HTTP |
| Native Quit cleanup | Pass | app-quit.json; Helper/socket/actual HTTP checked, not TCP connect alone |
| Distribution / full failure matrix | Untested | Developer ID/notarization/clean Gatekeeper/download interruption/read-only root/sleep-wake/long-term resources |

[evidence](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/native-helper/evidence). `verify_lifecycle.py` uses isolated fault-runtime/probe.sh in real Alpine VM; native viewer tested separately. Screenshots in tool record, no full video.

## Fixes and limitations

Helper flocks runtime root, cleans only fixed POC container, fences startup generations, waits on cancellation cleanup. Parent death/SIGTERM stops with 15-second final fallback; SIGINT forwards cleanup. VM startup is not viewer readiness; App polls actual HTTP before load.

Forced Chromium exit caused Restore pages. Changed main process to exec Chromium, TERM + 2 seconds before VM stop. Final Cmd+Q guest exitCode 0 (final-app-quit-helper.jsonl), Helper/VM cleaned, but profile.exit_type Crashed (app-quit.json): **Chromium clean exit failed**. TERM grace did not solve it; browser-close acknowledgment needed. Only saved-marker recovery confirmed.

Initial socket.create_connection success wrongly classified guest listener alive; HTTP later timed out with zero bytes and old Helper gone. Evidence retains this; application-level requests determine service availability, without attributing TCP connect to an unproven network mechanism.

P3/P4 found/fixed native menu deadlock and viewer Ctrl/Fence defects; see P3. Desktop uses P2 test page still labeled remote Docker; **authoritative backend label is local-swift-vm above it**.

Local Chromium runs as normal user without --no-sandbox, but final chrome://sandbox untested; P2 results cannot transfer. VNC/CDP/websockify guest loopback only; shared broker on private vmnet needs random token, not complete hostile guest/shell isolation.

## Technical decision

Keep user-process Swift Helper/pinned Containerization; no evidence requiring privileged Helper. GPUI/Swift small lifecycle IPC and shared viewer/control can enter product skeleton. Next: distribution supply chain/first-download failure feedback/full input isolation/native IME. Identity/memory/tasks remain existing research, outside this round.
