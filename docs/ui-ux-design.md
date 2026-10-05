# Pot UI specification

Updated: 2026-10-04. Current MVP UI specification; see [UI validation](validation/ui-ux.md) for historical acceptance and [Development plan](development-plan.md) for scope.

## Shared layout

Tasks, settings, and Sandbox use **gpui-kit 0.7.0** (gpui-pre 0.3.7). Shared layout lives in `app/src/ui.rs`; new pages reuse it rather than maintain separate colors/sizes.

| Element | Specification |
| --- | --- |
| Sidebar | 56 px, icons only; name tooltips, selected/hover backgrounds |
| Page | `ui::page(cx)`: reserves sidebar, uses Kit theme background/text |
| Standard content | `ui::content()`: centered, max 820 px, 32 px padding, 20 px section spacing |
| Title / hint | `ui::title()`: 24 px semibold; `ui::hint()`: 13 px secondary text |
| Card | `ui::card(cx)`: 20 px padding, 8 px radius, 1 px themed border |
| Form | Kit `Form::horizontal().label_width(px(160.))` + `Field` + `Input` |
| Actions | Kit `Button`: `primary()` for primary, `ghost()` for secondary |
| Icon actions | Kit `IconName` with tooltip and accessibility label |
| List | Kit `ListItem` with shared card border/radius/20 px padding |

Prefer `cx.theme()` background, foreground, muted_foreground, border, secondary; reserve green/amber/red for status. Selection backgrounds must not depend solely on hover. General settings switch light/dark/system appearance.

Content scrolls while titles/main navigation remain. Minimum window 800 x 700; long forms must scroll to action buttons.

## Pages and interactions

- **Chat**: Pot icon opens chat; no central avatar. Blue user/neutral assistant bubbles. Small action bar below messages: Copy left, linked View task right, both with tooltips. Latest assistant actions show when idle; historical/user actions show on hover. Reserve height to avoid movement. Latest messages shares native input width/centering, sits above it, is fully clickable, and hides immediately on click. Preserve native IME/edit shortcuts. Opened conversations show cache before background refresh.
- **Tasks**: fixed title/three state groups/search above, scrollable task list only, fixed pagination below. Main Agent creates tasks without manual creation. Default Ongoing (queued/running), Needs attention (failed/interrupted), Archived (completed/cancelled). Groups show global counts; title/ID search stays within group; 20 items/page, latest state change first. Cards retain stage/pending guidance count; guidance in archived task conversations requeues them. Group/search changes reset page. Distinct error/no-match/loading text. Use Kit Input/Button/ListItem; active-task checks use global counts, not visible page. Agent settings concurrency defaults to 5.
- **Task conversation header**: separate back row; single-line title with `...` overflow and wrapping Kit Tooltip (max 480 px). Preserve full titles; restore old 48-character truncation from original task first lines. One row below shows stage/archive/cumulative tokens using small icons/secondary text; tokens open details. Header stays fixed, max 760 px aligned with messages. Task list titles share truncation/full tooltips.
- **Schedules**: reuse Tasks / Schedules navigation, Kit Button/Form/Field/Input/cards. Fixed bottom pagination matches tasks: total/page left, previous/next right. Details show next run/state/paginated history linking to tasks. Main Agent creates schedules; no manual creation. Existing edit forms scroll with fixed save/cancel and include zone/frequency/notification/missed policy. Pause/delete preserves current runs; second click confirms deletion. See [Scheduled tasks](scheduled-tasks.md).
- **Usage**: overall statistics/charts only, last 7/30/90 days/year. No granularity selector; year automatically aggregates 7-day groups. Task/main usage belongs in each conversation and survives archive. Single-page details: totals, horizontal model stacks (conversation/compaction, real tokens/proportions/hover), tool rankings (estimated tokens/calls, shared scale, top 5 expandable). Hide zero compaction/empty tools; no tabs/per-request lists/Skills stats. Never add tool output to model consumption. Hide allowance cards without periodic limits. See [Token usage](token-usage.md).
- **Settings**: General / Assistant / Agent / Sandbox share forms. Assistant explicitly selects Codex/Claude/AGY/Pi-API; Agent checks read-only status/edits launch commands. API keys belong to model services; show supported options only. Expand limits on demand. Reuse normal Assistant settings after environment/tool preparation. General contains appearance/shortcuts without Window and background/Unread messages explanations.
- **Sandbox**: Kit title/toolbar 112 px; desktop fills remaining space without the 820 px cap. Green connection dot with hover text. Take over is primary; environment/reconnect/settings/more are icons. Environment displays image/state/CPU/memory/system disk/work-file sizes.
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

Token input is decimal: 1K=1, 000, 1M=1, 000, 000, stored as integers. Remove the product 32K cap; retain exact Pi/JSON integer bound (2^53-1), rejecting zero/negative/fractional results/overflow. Model context and output limits differ; servers may reject unsupported ranges.

## Sandbox desktop

Embedded noVNC uses a warm-white minimal Xfce desktop matching Pot. Persistent bottom Browser/Files/Terminal launchers/window switching and matching context menu. CJK fonts cover Chinese/Japanese/Korean. Software is preinstalled; see [Sandbox image](sandbox-image.md) for build/theme entries.

Settings navigation: General / Assistant / Agent / Sandbox. Agent shows CLI installation/official login status, refresh, current profile executable/argv editing. Command saves use profile revisions without model tests.
Terminal and Sandbox are independent sidebar entries. Sandbox contains desktop/environment controls; Terminal
connects to the same Runtime-owned PTY independently of noVNC/main Agent/first setup. Navigation/reconnect injects no commands;
terminal close is separate. Agent settings offers read-only installation/auth, refresh, ordinary Open Terminal, and launch command editing,
without clickable login status/login-command fields/dedicated auth PTYs/wizards. Users operate native CLIs in Terminal themselves.

First launch prepares environment/tools, then normal Assistant settings explicitly choose a main Agent or configure API.
Set up later still permits Terminal/Sandbox/settings; without a main profile no chat/tasks execute.
Known logged-out/check-failed CLIs are unavailable; unknown does not mean logged in, and accepted CLIs require explicit selection.
Refresh neither selects backends automatically nor sends paid tests/creates native main sessions; the first real message creates one.
Tool selection/completion persist per data directory, not entered keys. Install other Agents reuses preparation.

Routing settings list saved profiles for model edit/copy, background defaults, Agent-selectable
profile allowlists, and profile/provider background concurrency limits. API providers configure daily/weekly token
allowances alongside global limits; CLI subscriptions show post-execution metering only. Change Agent in task chat opens
the same settings with migration target; save migrates only that task, while normal settings target main chat.


AGY supports main/background/schedules/summaries with user-entered models. Users accept original
CLI permissions; per-turn prompts constrain roles, with no enforced read-only summary claim. Pot gives AGY no task creation,
schedule management, or other orchestration tools. Selecting AGY sets main/background default profiles together.
AGY has no validated official read-only login query; show Unknown without model probes.


Global/session usage both show Agent / model usage grouped by Pi/Codex/Claude/AGY and model,
including input/output/input-cache/incomplete records; missing legacy attribution shows Unknown Agent.
Global categories follow selected ranges; details use session totals, 20/page with previous/next.
Category pages do not change totals/trends/allowances; range/session/zone changes reset pages.
