# UI / UX redesign validation

> Historical record: versions, allowances, UI reflect section dates. Current scope/contracts: [Development plan](../development-plan.md), [Documentation index](../README.md).

Date: 2026-10-03
Basis: [UI design](../ui-ux-design.md). Current Pi MVP slice passes below, not multiple drivers/full cold Setup/web isolation.

## Implemented

- Native56px icon navigation chat/Sandbox/settings, tooltips, no central avatar.
- GPUI blue-gray bubbles/light-dark/basic CommonMark/copy/collapsed tools/successful write-edit file entries. Resolve artifacts only in workspace, show Finder without execution.
- NSTextView multiline/Chinese paste/Enter-send/Shift-Enter/stop; navigation preserves drafts/history position/latest entry.
- Full-page noVNC/minimal toolbar/takeover-handback/reconnect/clipboard/browser/workspace/environment stop-restart. Remove debug forms, Pot browser welcome.
- Real OCI reference/compressed size/uptime/average CPU/memory/system disk/work logical sizes; files refresh30s.
- System/light/dark, Pi-DeepSeek key/allowances, CPU-memory-disk. Validate/atomic save/normal restart; no apply during execution/unknown Runtime. Blank key retains, mode0600.
- Missing Pi config reuses Agent Setup form; only validated Pi selectable, fixed DeepSeek Flash.
- Original U-shaped pot/floating lid shared App-Dock-sidebar-tray. Working lid movement/static raised with reduced motion, tilted auth state. Tray no text, unread number, Open/Hide menu only.
- Unread deduplicates completed assistant IDs/persists read state; Sandbox/history viewing preserves, bottom chat clears.

## Validation

| Item | Result |
|---|---|
| Build | macOS App/Swift Helper/ARM64 Runtime pass; ad hoc deep-strict bundle |
| Rust automation | App6/protocol1/Runtime8 pass; prior no-paid Pi integration not rerun |
| Swift unread | Historical baseline/duplicates/hidden-history/read/reopen pass |
| Native UI | Sidebar/settings themes/bubbles/Chinese paste/Shift-Enter/multiline draft navigation pass |
| Agent settings | Invalid tokens rejected; restored config confirmed/saved/restarted/reconnected, history/usage retained, key0600 |
| Sandbox | Actual noVNC/real metrics; browser close retains connection, empty desktop handback/browser reopen pass |
| Real send | Native short test, Pi acknowledgment, no tools |

One additional **DeepSeek request**: 2350 input (2176 cached), 2 output; ledger5->6, no reset.

Test entry points:

```sh
CARGO_TARGET_DIR=.local/poc/cargo-target cargo test --locked -p pot-app -p pot-runtime -p pot-protocol
swiftc -module-cache-path .local/d1/swift-modules app/swift/UnreadTracker.swift app/tests/unread.swift -o .local/d1/unread-tests
.local/d1/unread-tests
scripts/pot prepare
scripts/pot build
scripts/pot bundle
scripts/pot open
```

Ignored `.local/d1/ui-*.log`; Machines holds deployment/resources.

## Fixes and boundaries

- Old Helper set only cgroups while VM defaulted1GiB/4vCPU. Pass VMResources plus recommended128MiB kernel reserve. Config3GiB yields ~2.98GiB usable, CPU2.
- Fix black background from transparent native settings/Metal, explicit native dark layers.
- Browser-closed handback still RFB release/Fence without nonexistent CDP page.
- Config checks format/ranges, no automatic paid key probe; Saved is not key validation.
- Other drivers/migration/task lists/attachments/full artifact previews future; no fake entries. Basic Markdown lacks full tables/inline images.
- No generic review-wait event; tilted lid uses NeedsAuth, extend for future waits. UI not D4 isolation.
- Cold downloads/resume/distribution undelivered; reused configuration flow only.

## 2026-10-04 refinement

App/bundle signature pass. Real sidebar/selection/General-Agent alignment/limit expand/copy hover/paste checked; drafts cleared, no key save/change/models. Native settings then, Kit Clipboard. Task centered cards/header New task.

### Gray message copy contrast

Assistant hover-copy gets background/border/shadow: white-gray light, brighter gray/light border dark; hover stronger. Blue user unchanged. Build/signature pass.

### Sandbox connection indicator

Sandbox title retained; green connected/amber connecting/red disconnect-error dot. Status in hover title, keyboard/aria retained; RFB/API refresh/errors. JS/signature pass.

WKWebView cached old viewer; LocalRuntime readiness/page requests use reloadIgnoringLocalCacheData for upgrades. Swift/Rust pass.

### Avoid repeated conversation loading

Client:: select cleared snapshots/waited500ms every click; same-session now unchanged, live main_snapshot/up to16 task memory snapshots. First access loads correct target. Selection wakes refresh/GPUI; no disk cache, boot_id clears tasks.

App8 tests including reopen messages-errors/cache isolation pass; build/signature, no models. Cold VM/first task/long history virtualization outside.

## 2026-10-04: Unified tasks / settings / Sandbox

- `app/src/ui.rs` shared page/title/hint/cards/Kit forms; same task borders.
- AppKit settings -> Kit Form/Field/Input/Button; Swift validates/atomic save/restarts; Setup reuses.
- Sandbox title/status/actions -> Kit; WebView below, web environment/More retained, hidden web header/shared radii/themes.
- `cargo build --locked -p pot-app`, `cargo test --locked -p pot-app` pass8; existing target, resigned/reopened.
- Real three-page navigation/cards/forms/limits/Cmd-A/paste/vCPU0 error; restored draft, no save.
- Real green dot/image-CPU-memory-disk/More reconnect-browser-clipboard-management; dark viewer sync, restored system theme.
- No models/auth/resources/task changes; no full save-restart/takeover/minimum-size regression.
- Rewrote concise `docs/ui-ux-design.md`; reuse Kit/ui.rs, no separate standard AppKit settings.

## 2026-10-04: Everyday configuration ranges

Remove General background/unread text. Defaults10000 lifetime/8192 output/100 per turn, Sandbox4vCPU/8GiB/32GiB. UI/Swift/Runtime/Helper/import ranges aligned; old explicit values preserved. Local dev config explicitly updated after clean exit, ledger11.

App/Linux Runtime/Helper pass; 26 tests, 2 external Pi ignored. Reopened API Ready/new budget/4-8-32, UI fields confirmed, no models.

## 2026-10-04: Sandbox menu positioning

Web menu offset by title. App NSMenu anchored toolbar-right in window coordinates across GPUI/WebView; standalone unchanged. Real alignment/disabled/separators/management-to-settings/scaling menu pass; App build, no models.

## 2026-10-04: Chat caret blinking

200ms sync resetting textColor/frame disrupted caret. Update colors on theme change/geometry only actual resize. Build/bundle/reopen; bright/dark caret/paste-select-delete observed, draft cleared, no send.

## 2026-10-04: Native keyboard routing and networking

Composer is NSTextView, not Kit. GPUI conflict inserted control characters. firstResponder composer routes through local key monitor; unmarked arrows/Home-End map AppKit editing with Shift/Option/Command; marked IME native.

Real abcd/Left2/X -> abXcd, Right/Shift-Right replacement -> abXcY, Command-Left/Z -> ZabXcY, multiline movement correct. Draft cleared, 25 tests/2 ignored, built/bundled/reopened.

Pi bash/curl/outbound network available; earlier no-network reply used no tools. Append prompt tries authoritative current sources, distinguishes site denial/network, invents no tools. Real curl https://abc.xyz/investor/ returns HTTP/2 403/cf-mitigated challenge, accurately reported.2 requests/no retries; no dedicated search/browser tools.

## 2026-10-04: Custom output tokens / K-M

Remove32768 cap; UI/import decimal case-insensitive K-M/whitespace/fractions -> integer. Runtime/protocol u64 retains JS exact2^53-1. Output stays8192; server validates, context/output differ.

Swift/CLI128K/1M/1.5M/>u32 and fractional-negative-overflow rejection pass. Rust1M/2^32/2^53-1 roundtrip/zero-overflow rejection; 26 regressions/2 ignored. App/Linux/Helper bundle/reopen, no models. Final UI interrupted by simultaneous user window operation; parsing/config tested.

## 2026-10-04: Latest-message button

Deferred scroll_to_bottom left old render/no unchanged-state notification. Jump state hides same frame; separately observe bottom for redraw. Center above input. Build/reopen; scroll-up appears/click removes AX without mouse move/repeat works; no models.

## 2026-10-04: Task states / archive / pagination

Kit groups/search/fixed pagination; Ongoing/Needs attention/Archived global counts,
20/page, independent selected metadata. Reopen: 4 old completed archived,
ongoing empty; Chinese multitask search2, unmatched explicit; archived detail
Archived/Completed, history/guidance retained, back preserves filters, search cleared.
API limit2 returns2/4, next_offset2. Thousand-task automated pagination without
bulk user data injection; ledger27, 0 paid calls.

## 2026-10-04 schedules

- Built/reopened `.local/d1/Pot.app`, updated guest Runtime.
- CUA Tasks/Schedules/form/paused creation/edit refill/future one-off/resume-zone/pause checked.
- Test schedule removed via API, no enabled schedules/active tasks; ledger27, 0 new paid.
- App9/Protocol1/Runtime31 and4 real Pi mock tests pass, including creation/independent runs/unchanged silence. [Contract](../scheduled-tasks.md).
- Light UI only; no extra minimum-size/dark acceptance.

### 2026-10-04: Token usage / Pot allowance

- Global7/30/90/365, 53 year buckets; session/archived totals/requests/tools/Skills/compaction then. Default no periodic card.
- SQLite dedupe/attribution/cache/import/date/page/DST-Monday/reserve/manual resume/unknown/wait persistence; task slots/restart/guidance/cancel.
- App10/Protocol1/Runtime39 plus5 real Pi mock pass. Real append tool then local allowance exhaustion/storage reopen/same-session resume, one file append, 2 requests30tokens.
- Build/lint/diff; real sidebar/ranges/forms/archive/page; historical27 retained/unlimited, 0 paid.
- Missing old times not invented. Skills/automatic compact then off, subscriptions absent; no dark/minimum-size. Existing UI data, positive buckets/dedupe mock-tested.

## 2026-10-04: Task conversation header

- Font-measured single-line `...`, Kit full tooltip/list-chat shared grapheme-aware logic.
- Stage/archive/tokens one gray icon row aligned; old48-character titles restored from initial task, no models.
- 51 tests(App10/Protocol1/Runtime40) title creation/persistence/repair; 5 Pi not rerun. lint/build/image/bundle pass.
- Real long CJK-English ellipsis/restored titles/icons/tooltips; then fixed-width wrapping built, Mac locked before final visual/minimum-dark check. No models.

## 2026-10-04: Enable compaction, remove Skills stats

-54 regular/6 real Pi mock pass: threshold/output-trigger retry/budget recovery/cancel/permissions/migration/multiple-summary aggregation-dedupe; cancelled no-usage unknown.
- lint/workspace/App/Linux/image/bundle pass; reopened summary compaction=true, Skills capability/UI removed.
- Real then-three tabs/model-tools-compaction/Enabled-empty. No forced paid compaction; nonempty mock-tested, no extra real card screenshot, 0 paid.

## 2026-10-04: Simplified usage details

- Remove tabs/detail pagination; totals/conversation-compaction actual stacks/tool rankings, descending/top5-expand, estimates excluded from totals.
- Workspace/lint/54 regular pass, 6 Pi not rerun; App/Linux/bundle, 0 paid.
- Real main lifetime/single stack/empty-tools hidden/no tabs-page. No fresh tools/compaction so nonempty ranks/expand/two stacks untested; no dark/minimum-size rerun.
