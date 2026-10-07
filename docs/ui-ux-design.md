# Pot UI specification

Updated: 2026-10-07. Current MVP UI specification; see [Development plan](development-plan.md) for scope.

For the approved target appearance, components, motion and navigation, see [Design language](design-language.md). Shared page colors, layouts, the contextual toolbar and navigation history, Chat, the composer and Pot activity/unread icons now follow that direction. The upstream browser-authorization/provider integration and Terminal removal are retained; the new AI panels share these visual tokens and responsive forms.

## About

The first item in the native Pot menu is About Pot (关于 Pot). Use AppKit’s standard
About panel with version, developer, copyright, MIT license and project link.
The bundled Rust dependency license report is available through its own link.
This panel does not add a sidebar destination. Menu and credit labels follow
the application language.

## Shared layout

Tasks, settings, and Sandbox use **gpui-kit 0.7.1** (gpui-pre 0.3.8). Shared layout lives in `app/src/ui.rs`; new pages reuse it rather than maintain separate colors/sizes.

| Element | Specification |
| --- | --- |
| Sidebar | 56 px, 36 px icon hit areas; Pot is Chat, Settings at bottom; 220 ms moving selection and 140 ms hover feedback |
| Toolbar | Shared 56 px row with Back/Forward, contextual title and conversation Usage action |
| Page | `ui::page(cx)`: reserves sidebar, uses Kit theme background/text |
| Standard content | `ui::content()`: centered, max 820 px, 32 px padding, 16 px section spacing |
| Title / hint | `ui::title()`: 24 px semibold; `ui::hint()`: 13 px secondary text |
| Card | `ui::card(cx)`: 20 px padding, 10 px radius, 1 px themed border |
| Form | Shared Kit Form: 144 px horizontal labels; vertical below 900 px window width; compact controls |
| Actions | Kit `Button`: `primary()` for primary, `ghost()` for secondary |
| Icon actions | Kit `IconName` with tooltip and accessibility label |
| List | Kit `ListItem`: 80 px task rows inside one grouped surface, fine row dividers, 16 px padding |

Prefer `cx.theme()` background, foreground, muted_foreground, border, secondary; reserve green/amber/red for status. Selection backgrounds must not depend solely on hover. General settings switch light/dark/system appearance.

Content scrolls while titles/main navigation remain. Minimum window 800 x 700; forms scroll while Settings and schedule Save/Cancel actions remain outside the scrolling region. Page entry uses a 180 ms opacity transition; Reduce Motion disables it.

The Chat navigation icon uses the selected [Pot Companion reference](design/pot-brand/d-pot-companion-concept.png): a terracotta pot with small handles, eyes and a restrained smile. The compact 28 px character is drawn natively rather than downsampling the concept board. Opening/revealing the window gives a 360 ms appearance and one blink; hovering raises the character by less than 1 px and changes its smile over 180 ms; pressing compresses it by up to 5% over 280 ms. Actual execution displays gently drifting steam on a three-second cycle, fading across state changes. A newly completed assistant reply gives an 850 ms contented expression once; initial history, failures and cancellation do not trigger it. The navigation hit area and selection highlight stay stable. Animation stops when the window is hidden/occluded, remains static while idle, and respects Reduce Motion. The tray retains its separate monochrome execution/unread rendering.

## Navigation history

History is window-local. Chat, task lists/details, schedule details, overall/conversation Usage, allowance configuration, Settings and Sandbox are destinations. Back/Forward buttons and ⌘[ / ⌘] restore task filters/page/scroll anchor, schedule selection/page, Usage range/ranking page, Settings section and scroll offsets. Search/group/tab changes checkpoint the current destination without adding entries; navigating to a new destination after Back discards the forward branch.

Native conversation drafts and entity-backed unsaved forms remain intact during navigation. History does not send messages, save settings, execute tasks or reclaim desktop control. Returning to Settings changes its section without reopening or resetting an in-progress AI connection panel. Embedded WebView focus and active marked-text composition retain their shortcut handling. Task scroll restoration uses the visible task ID plus inset to tolerate reordering. Missing task destinations show an inline recovery action.

## Pages and interactions

- **Chat**: Pot icon opens chat; no central avatar. Blue user/neutral assistant bubbles. Small action bar below messages: Copy left, linked View task right, both with tooltips. Latest assistant actions show when idle; historical/user actions show on hover. Reserve height to avoid movement. Latest messages shares native input width/centering, sits above it, is fully clickable, and hides immediately on click. Preserve native IME/edit shortcuts. Opened conversations show cache before background refresh.
- **Tasks**: executor brand icons appear in Chat running-task summaries and task-list rows, without additional Agent/model/reason labels; actual model and switching remain in task details. Icons follow actual binding snapshots and explicit migrations. Persistent execution preferences are managed/read through Chat; see [Execution preferences](execution-preferences.md). Fixed title/three state groups/search above, scrollable task list only, fixed pagination below. Main Agent creates tasks without manual creation. Default Ongoing (queued/running), Needs attention (failed/interrupted), Archived (completed/cancelled). Groups show global counts; title/ID search stays within group; 20 items/page, latest state change first. Grouped rows retain stage/pending guidance count; guidance in archived task conversations requeues them. Group/search changes reset page. Distinct error/no-match/loading text. Use Kit Input/Button/ListItem; active-task checks use global counts, not visible page. Task Configuration concurrency defaults to 5.
- **Task conversation header**: Back/Forward, title and Usage live in the shared toolbar; single-line title with `...` overflow and wrapping Kit Tooltip (max 480 px). Preserve full titles; restore old 48-character truncation from original task first lines. One row below shows stage/archive and task actions using small icons/secondary text. Header stays fixed, max 760 px aligned with messages. Task list titles share truncation/full tooltips.
- **Schedules**: reuse Tasks / Schedules navigation, Kit Button/Form/Field/Input/cards. Fixed bottom pagination matches tasks: total/page left, previous/next right. Details show next run/state/paginated history linking to tasks. Main Agent creates schedules; no manual creation. Existing edit forms scroll with fixed save/cancel and include zone/frequency/notification/missed policy. Pause/delete preserves current runs; second click confirms deletion. See [Scheduled tasks](scheduled-tasks.md).
- **Usage**: overall statistics/charts only, last 7/30/90 days/year. No granularity selector; year automatically aggregates 7-day groups. Task/main usage belongs in each conversation and survives archive. Single-page details: totals, horizontal model stacks (conversation/compaction, real tokens/proportions/hover), tool rankings (estimated tokens/calls, shared scale, top 5 expandable). Hide zero compaction/empty tools; no tabs/per-request lists/Skills stats. Never add tool output to model consumption. Hide allowance cards without periodic limits. See [Token usage](token-usage.md).
- **Settings**: General starts with Language (English / 简体中文), above Appearance and keyboard shortcuts. First launch matches macOS preferred languages, falling back to English; the resolved choice is saved. Changes apply immediately without restarting or resetting drafts. See [Internationalization](internationalization.md). General / AI Configuration / Task Configuration / Work Environment. AI Configuration contains one unified Agent list and the main Chat Agent/model selection; no API vs. CLI sections. The provider chooser uses compact branded controls; connection sheets use a 12 px radius, responsive shared labels, compact model/authorization actions, and a 200 ms fade respecting Reduce Motion. Changes remain drafts until explicit Save. Chat shows informational Agent/model text without switching dropdowns. Task Configuration keeps background defaults, concurrency and allowances; Work Environment keeps Sandbox resources.
- **Sandbox**: shared 56 px toolbar plus 56 px desktop controls; desktop fills remaining space without the 820 px cap. Textual connection state with semantic color. Take over is primary; environment/reconnect/settings/more are icons. Environment displays image/state/CPU/memory/system disk/work-file sizes.
- **Tray**: one pot-shaped status icon; click opens window, no menu. Unread count beside icon. Window close keeps background work; Quit stops the environment normally.

## Implementation boundaries

- Rust / Kit: chat, tasks, settings, Sandbox title/toolbar, unready states.
- Swift: window/sidebar/tray/native chat input/configuration/lifecycle. `SettingsPanel.swift` is configuration service only, not settings rendering. Restart confirmation uses native sheets; Sandbox More uses NSMenu anchored to toolbar right in window coordinates, avoiding WebView popup clipping.
- WKWebView / noVNC: desktop canvas/environment/clipboard/scaling. Standalone viewer retains its web More menu; shared web implementations use neutral colors, 8 px radii, 13-14 px text, synchronized themes. Hide web title bar in App to avoid duplicate toolbars.

Rust reads nonsensitive state through `pot_bridge_ui` and sends allowlisted operations via `pot_bridge_command`. Saved keys never return to UI; masked Input accepts new keys, blank preserves existing. Sync forms only on configuration revision changes; background refresh must preserve unsaved input. Validate ranges/active tasks/connection before save; after confirmation, stop normally, write atomically, restart.

## Adding UI

1. Start with `ui::page(cx).child(ui::content()...)` and reuse title/hint/card.
2. Prefer Kit Button/Form/Field/Input/ListItem over new AppKit/HTML standard settings widgets.
3. Store input state in Entity; render reads state, background updates notify redraw.
4. Icon buttons have names/tooltips/disabled states; slow actions show progress and recoverable failures.
5. Validate light/dark/minimum window/scroll/keyboard/paste/drafts after navigation/errors; UI-only changes need no model calls.

```rust
ui::page(cx).child(
    ui::content()
        .child(ui::title("Page name"))
        .child(ui::hint("One-sentence description", cx))
        .child(ui::card(cx).child(form))
)
```

## Allowance configuration

Defaults suit everyday tasks; preserve explicit old values. Settings changes apply after restart.

| Setting | Default | Range |
| --- | --- | --- |
| Concurrent background tasks | 5 | 1-1, 000; excess queues, main chat separate |
| Lifetime model requests | 10, 000 | 0-1, 000, 000; 0 disables requests, ledger retained |
| Output tokens per request | 8, 192 | Positive integer, K/M supported including decimals such as 1.5M |
| Model requests per turn | 100 | 1-1, 000 |
| Sandbox vCPU | 4 | 1–32 |
| Sandbox memory | 8 GiB | 1-128 GiB |
| Sandbox system disk | 32 GiB | 4-512 GiB |

Resource maxima remain constrained by hardware/virtualization. Update UI, Swift configuration, Helper, Runtime, and import-script ranges together.

Token input is decimal: 1K=1, 000, 1M=1, 000, 000, stored as integers. Remove the product 32K cap; retain exact JSON integer bound (2^53-1), rejecting zero/negative/fractional results/overflow. Model context and output limits differ; servers may reject unsupported ranges.

## Sandbox desktop

The Host chat/backend stay available with every environment stopped. Sandbox uses the existing 56 pt global rail, a 224 pt environment sidebar, and a desktop area with two 56 pt toolbar rows. Selecting an environment shows its noVNC desktop directly. A stopped environment stays stopped until Start Desktop is pressed. Selecting a running execution environment requests its desktop component automatically. View selection never changes the execution default; Set Execution Default is an explicit menu action affecting future unspecified assignments. Each task retains its immutable Sandbox ID.

The list shows environment name, phase, running task count and the execution-default marker. New creates a stopped Linux environment. The desktop toolbar provides Take Over / Give Back, selected-environment Settings and More. More contains reconnect, browser, clipboard, workspace, associated tasks, execution-default selection, stop and delete. Busy leases and unsettled operations explain disabled stop/delete actions; the Runtime rechecks restrictions. The default environment and environments referenced by durable tasks or schedules cannot be deleted. Files are retained after deletion.

Settings edits the selected environment's name and CPU/memory/system-disk values through Runtime. The environment must be stopped or failed, with no active leases or unsettled operations. Resources persist in `sandboxes/<id>/sandbox.json` and apply on the next start; global resource files are no longer read. Worker lifecycle/target changes are rejected by Runtime. Async status, desktop probes and management requests capture their target; switching views invalidates prior view completions.

Embedded noVNC uses a warm-white minimal Xfce desktop matching Pot. Persistent bottom Browser/Files/Terminal launchers/window switching and matching context menu. CJK fonts cover Chinese/Japanese/Korean. Software is preinstalled; see Sandbox image for build/theme entries.

## AI Configuration (#7)

Unconfigured Chat links to AI Configuration; first-time setup and Settings use the same page. The Pot Terminal page, side-rail button, xterm assets and native renderer have been removed. Sandbox remains independently accessible. API setup never requires a Terminal.

AI Configuration contains a compact Main Agent summary, Integrated Agents list and one shared connection panel. The catalog uses colored icons: Codex, Claude API, Gemini API, DeepSeek, OpenCode Go, OpenCode Zen and Custom API. Existing API connections remain editable. Removed native CLI configurations are unsupported and require fresh configuration; no compatibility migration is provided. Main-Agent changes stay in AI Configuration, not the Chat header.

Preset API forms show only key/model and a Get API Key link. Endpoint/protocol are supplied by Pot. Claude has no account/API mode selector. Custom API shows endpoint and four explicit formats: OpenAI Chat Completions, OpenAI Responses, Claude Messages and Gemini. Model-list results appear in a picker alongside the editable model field.

Check and Save are separate adjacent buttons. Check performs supported model-list GETs without inference or saving. Save validates configuration through the existing Runtime API; it does not force a separate Check click or claim successful inference. Unsupported model discovery permits manual input. Provider errors preserve drafts; secrets are masked and excluded from public state. Switching connection forms clears secret fields.

OpenCode uses a maintained per-plan, per-model route table in `app/swift/APIIntegration.swift`, checked against official documentation on 2026-10-06. Go and Zen may serve the same model through different formats. Unknown formats/models are excluded from discovered options and require a catalog update or explicit Custom API configuration; names alone do not determine the adapter.

For an empty main configuration, the final connection action also sets the Main Agent through the existing migration boundary. Entry from Chat uses Save and start chatting and restores its draft without sending. Settings explains Save and set as Main Agent. Existing main configurations are not replaced by ordinary additions. Save retains a stable draft connection identity and reconciles partially completed saves before retrying.

Account authorization uses official Host Codex App Server interfaces. The panel opens the official browser URL; the official Codex process owns its Host loopback callback and credentials. Pot runs no callback listener or VM relay. Only the matching official login-completed event followed by account verification establishes success; Pot then reads model options. Native-only start/status/cancel APIs reject browser Origins and worker tokens. Cancellation, timeout and shutdown settle the official login attempt. The backend also supports official device-code authorization. Real-account acceptance must be renewed after this Host change.

The existing Runtime PTY subsystem is retained; removal of this separate subsystem was also rejected by automatic review. This is separate from the deleted product Terminal UI and from Agent shell/tool execution.

Main Save reuses Runtime migration. Pending operations disable duplicate submission and explain current-turn/paused-session waits; only a confirmed active binding is displayed as effective. Existing task bindings remain pinned. New tasks following Chat use its active snapshot rather than an unapplied profile edit.

Task Configuration preserves existing explicit worker defaults/routing and allows choosing Follow Chat. Advanced profile editing/copy, allowlists, profile/provider caps and allowances remain available. Task-specific Change Agent targets only that task and reuses AI connection management; completion returns to the task. Credential-independent product limits use private `execution-limits.json`, without implicitly selecting Pot or requiring a DeepSeek key.

Unconfigured Chat offers AI Configuration; the user can keep browsing without configuring an Agent. There is no environment/tool installation wizard. Status refresh never selects an Agent or makes paid model probes. Real-account/engine matrices are recorded separately in dated acceptance reports.

Global/session usage both show Agent / model usage grouped by Pot/Codex and model (historical rows retain their original labels),
including input/output/input-cache/incomplete records; missing attribution shows Unknown Agent.
Global categories follow selected ranges; details use session totals, 20/page with previous/next.
Category pages do not change totals/trends/allowances; range/session/zone changes reset pages.
