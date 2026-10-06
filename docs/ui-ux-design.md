# Pot UI specification

Updated: 2026-10-05. Current MVP UI specification; see [UI validation](validation/ui-ux.md) for historical acceptance and [Development plan](development-plan.md) for scope.

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
- **Tasks**: fixed title/three state groups/search above, scrollable task list only, fixed pagination below. Main Agent creates tasks without manual creation. Default Ongoing (queued/running), Needs attention (failed/interrupted), Archived (completed/cancelled). Groups show global counts; title/ID search stays within group; 20 items/page, latest state change first. Cards retain stage/pending guidance count; guidance in archived task conversations requeues them. Group/search changes reset page. Distinct error/no-match/loading text. Use Kit Input/Button/ListItem; active-task checks use global counts, not visible page. Task Configuration concurrency defaults to 5.
- **Task conversation header**: separate back row; single-line title with `...` overflow and wrapping Kit Tooltip (max 480 px). Preserve full titles; restore old 48-character truncation from original task first lines. One row below shows stage/archive/cumulative tokens using small icons/secondary text; tokens open details. Header stays fixed, max 760 px aligned with messages. Task list titles share truncation/full tooltips.
- **Schedules**: reuse Tasks / Schedules navigation, Kit Button/Form/Field/Input/cards. Fixed bottom pagination matches tasks: total/page left, previous/next right. Details show next run/state/paginated history linking to tasks. Main Agent creates schedules; no manual creation. Existing edit forms scroll with fixed save/cancel and include zone/frequency/notification/missed policy. Pause/delete preserves current runs; second click confirms deletion. See [Scheduled tasks](scheduled-tasks.md).
- **Usage**: overall statistics/charts only, last 7/30/90 days/year. No granularity selector; year automatically aggregates 7-day groups. Task/main usage belongs in each conversation and survives archive. Single-page details: totals, horizontal model stacks (conversation/compaction, real tokens/proportions/hover), tool rankings (estimated tokens/calls, shared scale, top 5 expandable). Hide zero compaction/empty tools; no tabs/per-request lists/Skills stats. Never add tool output to model consumption. Hide allowance cards without periodic limits. See [Token usage](token-usage.md).
- **Settings**: General / AI Configuration / Task Configuration / Work Environment. AI Configuration contains one unified Agent list and the main Chat Agent/model selection; no API vs. CLI sections. Changes remain drafts until explicit Save. Chat shows informational Agent/model text without switching dropdowns. Task Configuration keeps background defaults, concurrency and allowances; Work Environment keeps Sandbox resources.
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

## AI Configuration (#7)

Unconfigured Chat links to AI Configuration; first-time setup and Settings use the same page. The Pot Terminal page, side-rail button, xterm assets and native renderer have been removed. Sandbox remains independently accessible. API setup never requires a Terminal.

AI Configuration contains a compact Main Agent summary, Integrated Agents list and one shared connection panel. The catalog uses colored icons: Codex, Grok, Claude API, Gemini API, DeepSeek, OpenCode Go, OpenCode Zen and Custom API. Existing API connections remain editable. Removed native CLI configurations are unsupported and require fresh configuration; no compatibility migration is provided. Main-Agent changes stay in AI Configuration, not the Chat header.

Preset API forms show only key/model and a Get API Key link. Endpoint/protocol are supplied by Pot. Claude has no account/API mode selector. Custom API shows endpoint and four explicit formats: OpenAI Chat Completions, OpenAI Responses, Claude Messages and Gemini. Model-list results appear in a picker alongside the editable model field.

Check and Save are separate adjacent buttons. Check performs supported model-list GETs without inference or saving. Save validates configuration through the existing Runtime API; it does not force a separate Check click or claim successful inference. Unsupported model discovery permits manual input. Provider errors preserve drafts; secrets are masked and excluded from public state. Switching connection forms clears secret fields.

OpenCode uses a maintained per-plan, per-model route table in `app/swift/APIIntegration.swift`, checked against official documentation on 2026-10-06. Go and Zen may serve the same model through different formats. Unknown formats/models are excluded from discovered options and require a catalog update or explicit Custom API configuration; names alone do not determine the adapter.

For an empty main configuration, the final connection action also sets the Main Agent through the existing migration boundary. Entry from Chat uses Save and start chatting and restores its draft without sending. Settings explains Save and set as Main Agent. Existing main configurations are not replaced by ordinary additions. Save retains a stable draft connection identity and reconciles partially completed saves before retrying.

Account authorization uses official Codex app-server and Grok ACP interfaces. The user explicitly approved native authentication endpoints and a temporary Mac-to-VM callback relay on 2026-10-06. The shared panel opens the official browser URL, waits for the official completion result and account verification, then reads model options. Callback receipt alone never establishes authentication. Cancel, failure, expiry, app shutdown and environment loss close the Mac listener. Only native bearer-authenticated requests are accepted; browser Origins and worker tokens are rejected. Callback targets are pinned to the attempt's official guest loopback URL and state. No authorization codes, tokens or passwords enter product records or logs. Codex falls back to the official device-code flow if the Mac callback port is unavailable. Grok currently requires browser loopback authorization.

Grok 1.0.46 is pinned from the official npm distribution. Its initial ACP integration supports main-chat session creation/loading, model selection, streaming and cancellation. It does not claim tool-policy enforcement, MCP orchestration, background-task support, native usage reporting or checkpoint replay. Client permission requests fail closed pending a permission-review UI. Real-provider end-to-end verification remains outstanding; this machine's connection to auth.x.ai fails during TLS negotiation.

The existing Runtime PTY subsystem is retained; removal of this separate subsystem was also rejected by automatic review. This is separate from the deleted product Terminal UI and from Agent shell/tool execution.

Main Save reuses Runtime migration. Pending operations disable duplicate submission and explain current-turn/paused-session waits; only a confirmed active binding is displayed as effective. Existing task bindings remain pinned. New tasks following Chat use its active snapshot rather than an unapplied profile edit.

Task Configuration preserves existing explicit worker defaults/routing and allows choosing Follow Chat. Advanced profile editing/copy, allowlists, profile/provider caps and allowances remain available. Task-specific Change Agent targets only that task and reuses AI connection management; completion returns to the task. Credential-independent product limits use private `execution-limits.json`, without implicitly selecting Pi or requiring a DeepSeek key.

Initial setup opens ordinary AI Configuration directly and can be deferred. Status refresh never selects an Agent or makes paid model probes. Real-account/engine matrices are recorded separately in dated acceptance reports.

Global/session usage both show Agent / model usage grouped by Pi/Codex/Grok and model (historical rows retain their original labels),
including input/output/input-cache/incomplete records; missing legacy attribution shows Unknown Agent.
Global categories follow selected ranges; details use session totals, 20/page with previous/next.
Category pages do not change totals/trends/allowances; range/session/zone changes reset pages.
