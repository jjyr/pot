# GPUI Kit suitability evaluation

> Historical research: versions/current refer to original review date. [Development plan](../development-plan.md) defines implementation, not recommendations/old state here.

Reviewed: 2026-10-04. Adopt Kit components/text/interaction in stages and fix Pot updates/window integration. Only tray click changed this round, no dependency upgrade or comparative FPS measurement.

## Version facts

| Item | Query result |
|---|---|
| Pot dependency | `gpui = "=0.2.2"`, consistent Cargo.lock |
| Latest stable crates.io gpui | 0.2.2, 2025-10-22 |
| Latest stable gpui-kit | 0.7.0, 2026-09-28 |
| Published0.7.0 dependencies | `gpui-pre = "=0.3.7"`, matched platform/web, base/component0.7 |
| Latest gpui-pre | 0.3.7, 2026-09-28; snapshot package, not official Zed stable gpui0.3.7 |
| Researched Kit main | `3a39e9dfc97d8b12cf3c116708ada36bee02220b`, updated2026-10-03 |

Latest GPUI depends on package: Pot latest same-named stable, not Kit newer snapshot. Main fixes/features not necessarily0.7.0; main includes input horizontal scroll/Chinese soft-wrap fixes.

## Factors explaining Pot experience

Source facts/diagnostic hypotheses, not GPU measurements:

- Rust Shell200ms consumes Swift/unconditionally notify; bridge actions may wait tick, redraw unchanged.
- Client500ms task/main/current snapshots sets streaming visibility latency, not fixed by framework upgrade.
- Render clones/messages/reparses Markdown, no virtualization; cache/visible rendering needed.
- Swift100ms tray/sidebar redraw ~10animation updates/second.
- Swift rehosts GPUI contentView/offset56px; separate layout/focus/mouse/menu need coordinate/tracking/scroll/focus validation. Actual native validation/mutex conflict observed.
- Handwritten div-click buttons lack shared states; Markdown lacks full mouse selection.
- Debug build/dev only disables info, no optimization; compare optimized before performance claims.
- GPUI0.2.2 macOS has DisplayLink/event rendering; 200ms business tick **does not mean5FPS GPUI**.

## Why Kit fits

Still GPUI GPU, not AppKit substitute; benefits component behavior/state:

| Area | Kit candidate | Benefit |
|---|---|---|
| Tasks/settings | Buttons/forms/popovers/themes/focus | Unified feedback, less manual state |
| Chat body | TextView/State/selectable Markdown/incremental streaming | Selection/copy/render/stream state |
| Long chat/tasks | Variable-height virtual lists | Avoid full renders |
| Validation | test-support/interactions/accessibility | Focus/input/click/post-redraw checks |
| Diagnostics | profiler/gpui-fps | Measurement tools, not guarantees |

README120FPS claim is not Pot evidence; test AccessKit per control/current macOS bridge.

## Migration boundaries/order

1. **Updates first**: immediate native main-thread notification/incremental messages/state-only redraw; Markdown cache then virtualization. Measure idleCPU/click latency/scroll frame time/stream delay.
2. **One GPUI version/thin slice**: matched Kit0.7.0 types/entry replacing0.2.2; never mix Entity/Window/App generations. Single window/cards/selectable body first.
3. **Mixed-window boundary**: Kit owns Root/layout/interaction; Swift tray/lifecycle/WebView. Keep proven NSTextView IME until Kit pinyin/candidates/newlines/focus accepted.
4. **Keep noVNC/WKWebView**: experimental gpui-wry overlays GPUI rectangle; cannot assume fixes layers/focus/performance.
5. Same machine/data/scaling/window/optimized build; long chat/hover-click/inertial scroll/selection/Chinese/Sandbox/Hide-reopen/unread.60Hz16.7ms/120Hz8.3ms, p50-p95, not subjective/timer-based claims.

No JS extension runtime/full IDE/dock needed; keep Rust API/Pi scheduling/storage.

## Tray change this round

Remove Open/Hide menu; left release calls showWindow/activates, preserves page/drafts/unread. Hide close/Cmd-W, Quit Cmd-Q.

## Sources

- [GPUI metadata](https://crates.io/api/v1/crates/gpui)
- [Kit metadata](https://crates.io/api/v1/crates/gpui-kit)
- [Published0.7.0 dependencies](https://crates.io/api/v1/crates/gpui-kit/0.7.0/dependencies)
- [GPUI snapshot metadata](https://crates.io/api/v1/crates/gpui-pre)
- [Pinned Kit README](https://github.com/longbridge/gpui-kit/blob/3a39e9dfc97d8b12cf3c116708ada36bee02220b/README.md)
- [Workspace versions/optimization](https://github.com/longbridge/gpui-kit/blob/3a39e9dfc97d8b12cf3c116708ada36bee02220b/Cargo.toml)
- [Streaming Markdown example](https://github.com/longbridge/gpui-kit/blob/3a39e9dfc97d8b12cf3c116708ada36bee02220b/examples/stream-markdown/src/main.rs)
- [WebView constraints](https://github.com/longbridge/gpui-kit/blob/3a39e9dfc97d8b12cf3c116708ada36bee02220b/crates/webview/README.md)
- Local `app/src/main.rs`, client.rs, markdown.rs, Bridge.swift, scripts/pot, GPUI0.2.2 macOS sources.

## Validation this round

App/build deep-strict/update/start passes; window hide operated. CUA exposes windows, not status item, noWindowsAvailable after hide, so real tray click not automated-pass.0 models/no frame benchmark.

## 2026-10-04: Published component research / migration

Use actual crates.io0.7.0, not main-only APIs. Pin Kit0.7.0/lock gpui-pre0.3.7, remove0.2.2.

| Component | Adoption / boundaries |
|---|---|
| application/init/open_window/Assets | Kit platform/Root, one type set |
| Button | New/back/stop/tools/copy/task/latest, ghost/shared state |
| ListItem | Task cards/status/callback |
| TextView (base) | Replaces hand pulldown, stable IDs reuse parse/layout, selection/copy/tables/code, http-https links; base explicit white blue-bubble text |
| Theme | Sync native appearance |
| Sidebar | Checked Icon48px; keep56px AppKit to avoid simultaneous WebView changes |
| Input/Textarea | Independent states checked; keep NSTextView, pinyin separately |
| MessageScroller/virtual_list | Variable-height/tail/splice-reset checked, history virtualization pending |
| WebView | Existing WKWebView, no experimental Wry |

GPUI full-window coordinates/Rust56px inset; bounded native channel immediate Shell wake; 200ms notify only View/page/theme/input-height changes.500ms polling retained. Edit focus routes Kit Copy/SelectAll vs native NSTextView. Placeholder no longer intercepts clicks.

### Validation this round

- App build locked/ad hoc deep-strict pass.
- App6/protocol1/Runtime17 pass, 2 Pi mocks not run; removed handwritten parser test reduces App by1.
- Real restored task cards/selection-Cmd-C-paste completion/dark/Sandbox connected. Fix New button height-padding clipping.
-0 models/no FPS benchmark, dev build, not120FPS/native-performance claim.

Native navigation/settings-Setup/input/tray/Sandbox retained; GPUI rendering/component migration, not all AppKit rewrite.

Final Chinese multiline paste/Shift-Enter/Cmd-A-delete/placeholder click/New pass, drafts cleared/system appearance. No dedicated pinyin candidates. Wait old process exit before scripts/pot open to avoid LaunchServices exiting-instance reuse.
