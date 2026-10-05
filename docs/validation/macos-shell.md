# macOS shell: P0 / P1 minimum slice

> Old `poc/` sources are removed; commands apply only to the linked historical commit.

> Historical record: versions, allowances, UI reflect section dates. Current scope/contracts: [Development plan](../development-plan.md), [Documentation index](../README.md).

Date: 2026-10-03. **Limited pass**. Working prototype; user-confirmed real tray reopen, observed keys/pinyin/focus/scaling/explicit exit. Full P1 50-cycle/multiscreen/sleep-wake not complete. Written before P2 (read-only node check) and P4 Helper/VM implementation.

> Later: user ended GPUI/tray validation as sufficient; untested items are evidence limits, not prerequisites. Authorized llama-server stop resolved devpc memory shortage. P2-P4: [index](../poc-plan.md#10-execution-records), plus addendum below.

## Reproduction

Run in normal macOS Terminal at `~/Workspace/pot`:

```sh
./poc/macos-shell/poc doctor
./poc/macos-shell/poc build
./poc/macos-shell/poc run
./poc/macos-shell/poc status
./poc/macos-shell/poc stop
```

Artifacts/App/PIDs/logs default to `.local/poc`; use the same `POT_POC_ROOT=/any/writable/path` for all commands, including paths with spaces. Do not change HOME/CODEX_HOME. `run` creates ID/waits for `web.ready`; matching processes/concurrency locks reject duplicates. `stop` verifies full executable path before SIGTERM. Window close hides/preserves window-WebView; explicit exit releases handlers/observers/monitors/status item. No Helper/VM/HTTP server in this slice.

`doctor` diagnoses missing Rust/Cargo/Xcode. First build needs registry/network/cache. Restricted agent sandbox blocked LaunchServices/ps; approved normal desktop execution worked without security-setting changes.

Manual acceptance:

1. Click top-right **pot POC -> Open pot**, then left GPUI/right WebView inputs.
2. Select system pinyin, type `nihao` key by key, Space chooses the first candidate `你好`; English/digits, then `ni`/Escape must preserve committed text. Switch between inputs.
3. Enter the standard test sentence `你好，Pot！中文 English 123` on the right and click Verify standard text. Record paste/IME separately; paste does not prove pinyin.
4. Test Cmd-A/C/V/arrows/backspace. Right `abc123 -> Left -> Backspace` yields `abc13`.
5. Green window zoom then restore: check reflow/click/input alignment.
6. Red close / Cmd-W then tray reopen preserves text. **Quit pot POC** / Cmd-Q gives `status=null`; `run` restarts.

POC retains Dock for Cmd-Tab, not a tray-only product policy. Input logs locally to JSONL; use test text only.

## Pinned versions and build boundaries

| Item | Value / handling |
|---|---|
| GPUI | `=0.2.2`, `runtime_shaders`, full Cargo.lock |
| Unicode segmentation | `=1.13.3`; input adapted from Apache-2.0 GPUI example, NOTICE/license retained |
| Rust / Cargo | Observed 1.98.1; locked dependencies, no automatic toolchain install/upgrade |
| Swift / SDK | Swift 6.4, Swift language mode 5, macOS SDK 27 |
| Host | arm64 macOS 27.0.1, one display, backing scale 2; Machines authoritative |
| Swift bridge | build.rs static library, system frameworks/Swift runtime only, no temporary dylib rpath |
| Input page | Embedded `include_str!`, no HTTP port, WKWebsiteDataStore.nonPersistent |
| Development App | Atomic executable replacement/ad hoc signing; no Helper/VM/kernel-init/release signing |
| noVNC / Containerization / image | Not used/downloaded here; baseline-reproduction.md pins historical versions, P2/P4 pin separately |

Historical Cargo cache reused initially, then default empty `.local/poc/cargo-target` built successfully (~61 seconds, registry cache retained). Default startup and relocated first binary worked; `otool -L` found no old dylib dependency. **No clean-machine/fresh-registry acceptance**, so P0 remains limited. Upstream `block 0.1.6`/`proc-macro-error2 2.0.1` future-incompatibility warnings, current build passes.

## Observed records

Evidence: [`results.json`](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/macos-shell/evidence/results.json), [`native-events.jsonl`](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/macos-shell/evidence/native-events.jsonl), [`ime-focus.png`](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/macos-shell/evidence/ime-focus.png), [`resources.json`](https://github.com/jjyr/pot/tree/5ed4f40277bceb2f1a62f90c2fdf789f54f3bdb2/poc/macos-shell/evidence/resources.json). Actual events, not fixtures; raw ignored logs hashed in results.json. Times UTC, document date Asia/Shanghai.

| Scenario | Status | Method / observation / limits |
|---|---|---|
| Build/start/stop | Pass | Locked build/default root/moved binary/readiness/SIGTERM status=null/restart |
| Real tray click/reopen | Limited pass | User confirmed; 06: 00: 21Z open, 06: 00: 37Z hide, 06: 00: 40Z open, window_number=461. Tool did not expose status menu or fake clicks. No 50 cycles; before later focus fix |
| GPUI / WKWebView focus | Pass | Native GPUI-123/Web-456, screenshots/events no cross-input; final English switching retested |
| WebView navigation | Pass | Actual abc123/Left/Backspace -> abc13; Escape no new chars; prior failure retained |
| Minimum pinyin | Pass | Native nihao/Space in both, yielding `你好 English 123` in WebView and `你好 GPUI 123` in GPUI; ni/Escape preserved text. Web compositionstart/update/end, GPUI marked/commit; not paste |
| Full IME matrix | Untested | Alternate candidates/pages/punctuation/other IMEs/long selections pending. Later English-source nihao not counted as IME |
| Unicode clipboard | Limited pass | Web Cmd-A/native paste standard sentence exact; full bidirectional GPUI/Web matrix untested |
| Resize / Retina | Limited pass | Real zoom 1000x650 -> 1470x891 points, Web 660x650 -> 1130x891, scale=2; restored text and post-zoom ! input; cross-display untested |
| Red close | Pass | Native automation close, window.hide/visible=false/process alive; not recreate/state copy |
| Explicit exit | Pass | Real menu Quit, 06: 06: 50Z quit.request/bridge.cleaned, independent status=null; tray Quit/Cmd-Q not separately tested |
| Script stop cleanup | Pass | Latest SIGTERM same cleanup, run 11f2a0b5 bridge.cleaned, restart |
| Multiscreen / sleep-wake / Cmd-Tab matrix | Untested | One display, no deliberate user-machine sleep; key/resign-key not full switching |
| Resource stability | Limited pass | Single RSS 117936 KiB/8 threads/12 numeric FDs/0 listeners excluding WebKit totals; no 50-cycle comparison/leak claim |
| Imported baseline regression | Pass | Runtime 22/adversarial 4/OAuth 3 =29 fixture tests, not native interaction evidence |

Screenshot is actual pinyin/focus testing. Old Exact match text from earlier comparison remained below, not a current assertion; fixed to clear on input/reset. Decisions use corresponding time/events/exact values.

## Findings and fixes

1. **GPUI stole nonprinting WebView keys**: Left inserted U+001C, Escape U+001B as performKeyEquivalent reached IME before WebKit. Use sibling views in GPUI-owned NSWindow, HostView routes by firstResponder; real-key retest passes, no upstream changes.
2. **contentView was not GPUIView**: container makeFirstResponder swallowed left keys. Locate internal GPUIView in pinned 0.2.2, restore native firstResponder/FocusHandle on click. Revalidate class dependency on upgrades.
3. **WebView Cmd-A/V lacked Edit menu**: standard Cut/Copy/Paste/Select All enables exact Unicode paste.
4. **GPUI example IME offsets**: fix UTF-16 relative replacement/selection conversion without adding old replacement end; POC input, not full editor.
5. **Repeated signing cache**: overwriting Mach-O caused immediate silent exit/resources mismatch. Atomic temp replacement + whole-bundle ad hoc signing; run-stop-run passes.
6. **Stale comparison**: clear prior comparison after input.

All AppKit/WebKit mutations main-thread; copy borrowed C strings before async boundaries, no reverse raw Rust pointer callback. Preserve GPUI delegate/main loop. Hidden objects intentionally retained; exit removes handlers to break WKUserContentController cycle.

## P2 node check

Read canonical inventory and selected development `machines/devpc/machine.yaml.local` for read-only SSH. Docker available, free resources inadequate: **candidate does not mean deployment ready**. Conditions recorded in:

`~/Workspace/Machines/machines/devpc/pot-p2-readiness-20261003.md`

Do not copy addresses/accounts here. No pulls/containers/networks/volumes/ports/service stops. Before P2 recheck ~3-4 GiB free memory/disk or choose dedicated node, then pin desktop digest/loopback viewer/SSH. Local VM is not remote Docker evidence.

## Next decision

Keep GPUI/Swift/WKWebView for P2/P3: basic input feasible, real bridge defects fixed. Full P1 still needs final 50-cycle/resources/IME-shortcuts/multiscreen/sleep-wake. Standalone Helper IPC/VM/distribution/Gatekeeper/cold install are P4; development App is no substitute.

## Later status update

User ended minimum GPUI/tray slice; full P1 checklist above no longer this round's backlog.

User authorized devpc llama-server stop; matching user service stopped/no residual process, Docker available, enough memory. Autostart/model/other services unchanged, no desktop container yet. Operation/recovery: `~/Workspace/Machines/machines/devpc/pot-p2-readiness-20261003.md`. Next P2: image/volume disk budget and real integration.

## P3/P4 integration addendum

Owned NSMenu auto-validation reentered GPUI validate_menu_item lock, freezing native menus/queries. Sampling located it; main/Edit/status autoenablesItems=false restores final native click/input/Quit. Not full tray-cycle acceptance. See [P3](integrated-desktop.md), [P4](native-helper-bundle.md).
