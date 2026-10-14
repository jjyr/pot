# Pot UI specification

Updated: 2026-10-13. Current MVP UI specification; see [Development plan](development-plan.md) for scope.

The native App presents a unified assistant list and groups Machines by Space.
[Multi-Space UI](multi-space-ui.md) defines ownership, combined Tasks/Usage and
configuration location selection. Assistant messaging stays in the background.

For the approved target appearance, components, motion and navigation, see [Design language](design-language.md). Shared page colors, layouts, the integrated titlebar and parent navigation, Chat, the composer and Pot activity/unread icons now follow that direction. The upstream browser-authorization/provider integration and Terminal removal are retained; the new AI panels share these visual tokens and responsive forms.

## About

The first item in the native Pot menu is About Pot (关于 Pot). Use AppKit’s standard
About panel with version, developer, copyright, MIT license and project link.
The bundled Rust dependency license report is available through its own link.
This panel does not add a sidebar destination. Menu and credit labels follow
the application language.

## Shared layout

Tasks, settings, and Machines use **gpui-kit 0.7.3** with the **gpui-fast** backend, configured in the workspace dependency shared by native and Web UI. Shared layout lives in `crates/app/src/ui.rs`; new pages reuse it rather than maintain separate colors/sizes.

| Element | Specification |
| --- | --- |
| Sidebar | 56 px, 36 px icon hit areas; Pot is Chat, Settings at bottom; 220 ms moving selection and 140 ms hover feedback |
| Titlebar | One 44 px row integrated with native window controls, matching the sidebar background in light and dark modes without a divider; contextual title, parent chevron on child pages and conversation Usage action |
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

The Chat navigation icon uses the selected [Pot Companion reference](design/pot-brand/klee-app-menu-concept.png): an ochre asymmetric vessel with a tilted open rim, one eye, a curling smile and two short feet. The compact 28 px character is drawn natively rather than downsampling the concept board. Opening/revealing the window gives a 360 ms appearance and one blink; hovering raises the character by less than 1 px and changes its smile over 180 ms; pressing compresses it by up to 5% over 280 ms. Actual execution displays gently drifting steam on a three-second cycle, fading across state changes. A newly completed assistant reply gives an 850 ms contented expression once; initial history, failures and cancellation do not trigger it. The navigation hit area and selection highlight stay stable. Animation stops when the window is hidden/occluded, remains static while idle, and respects Reduce Motion. The menu bar uses the same Companion geometry in monochrome, with negative-space eye, simplified smile and open rim. Execution adds steam; waiting narrows the eye. Error and unread indicators remain independent.

## Parent navigation

The single left chevron and ⌘[ return to the current page's parent, not the last
visited page. Task details return to Tasks, schedule details to Schedules,
conversation Usage to its conversation, and allowance configuration to overall
Usage. Root pages show no chevron; there is no Forward action or ⌘] binding.
Tooltips identify the destination. Direct links also have a deterministic parent.

Window-local route state is scoped to Runtime/assistant and client mode. Returning
restores filters, pagination, task-ID scroll anchors, Usage ranges and scroll offsets.
Tasks and Schedules retain separate list state. Conversation drafts and entity-backed
unsaved forms remain intact. Navigation does not send messages, save settings,
execute tasks or reclaim desktop control. Embedded WebView focus and active
marked-text composition retain their shortcut handling. Missing task destinations
show an inline recovery action.

## Pages and interactions

- **Chat**: Type `/` at the caret to complete machine, label or Skill references; arrows/Enter insert, Escape dismisses, and click/tap also inserts. References remain editable text and constrain only newly delegated tasks from that message. See [reference scope](agent-context.md#message-references-and-machine-labels). Pot icon opens chat; no central avatar. Blue user/neutral assistant bubbles. Small action bar below messages: Copy left, linked View task right, both with tooltips. Latest assistant actions show when idle; historical/user actions show on hover. Reserve height to avoid movement. Latest messages shares native input width/centering, sits above it, is fully clickable, and hides immediately on click. Preserve native IME/edit shortcuts. Opened conversations show cache before background refresh.
- **Tasks**: executor brand icons appear in Chat running-task summaries and task-list rows, without additional Agent/model/reason labels; actual model and switching remain in task details. Icons follow Thread configuration snapshots and explicit configuration changes. Use the assistant list to open/create an assistant and its Chat edit action to save AGENTS.md rules; see [Agent context](agent-context.md). Fixed title/three state groups/search above, scrollable task list only, fixed pagination below. Main Agent creates tasks without manual creation. Default Ongoing (queued/running/waiting), Needs attention (failed/interrupted), Archived (completed/cancelled). Lifecycle details: [Task coordination](task-coordination.md). Groups show global counts; title/ID search stays within group; 20 items/page, latest state change first. Within each state tab, Tasks are grouped by assistant; assistants with no matching tasks are hidden. Grouped rows retain stage/pending guidance count; guidance in archived task conversations requeues them. Group/search changes reset page. Distinct error/no-match/loading text. Use Kit Input/Button/ListItem; active-task checks use global counts, not visible page. Task Configuration concurrency defaults to 8.
- **Task conversation header**: A parent chevron, title and Usage live in the integrated titlebar; single-line title with `...` overflow and wrapping Kit Tooltip (max 480 px). Preserve full titles; restore full titles from original task first lines. One row below shows stage/archive and task actions using small icons/secondary text. The status/action row stays fixed, max 760 px aligned with messages. Task list titles share truncation/full tooltips.
- **Schedules**: reuse Tasks / Schedules navigation, Kit Button/Form/Field/Input/cards. Fixed bottom pagination matches tasks: total/page left, previous/next right. Details show next run/state/paginated history linking to tasks. Main Agent creates schedules; no manual creation. Existing edit forms scroll with fixed save/cancel and include zone/frequency/notification/missed policy. Pause/delete preserves current runs; second click confirms deletion. See [Scheduled tasks](scheduled-tasks.md).
- **Usage**: overall statistics/charts only, last 7/30/90 days/year. No granularity selector; year automatically aggregates 7-day groups. Task/main usage belongs in each conversation and survives archive. Single-page details: totals, horizontal model stacks (conversation/compaction, real tokens/proportions/hover), tool rankings (estimated tokens/calls, shared scale, top 5 expandable). Hide zero compaction/empty tools; no tabs/per-request lists/Skills stats. Never add tool output to model consumption. Hide allowance cards without periodic limits. See [Token usage](token-usage.md).
- **Settings**: General starts with Language (English / 简体中文), above Appearance and keyboard shortcuts. First launch matches macOS preferred languages, falling back to English; the resolved choice is saved. Changes apply immediately without restarting or resetting drafts. See [Internationalization](internationalization.md). General / AI Configuration / Task Configuration. AI Configuration contains one unified Agent list and the main Chat Agent/model selection; no API vs. CLI sections. The provider chooser uses compact branded controls; connection sheets use a 12 px radius, responsive shared labels, compact model/authorization actions, and a 200 ms fade respecting Reduce Motion. Changes remain drafts until explicit Save. Chat shows informational Agent/model text without switching dropdowns. Task Configuration keeps background defaults, concurrency and allowances.
- **Machines**: lightweight machine cards group Host and Sandbox rows. Click a Host or Sandbox to expand its status, View Tasks, and an available desktop entry. View Tasks filters by the exact Runtime and target, with a clear-filter action. Desktop is shown only when the client has an implemented transport. Connect machine remains available. Technical management, plugin/session/work-directory inspection and file transfers belong in ordinary Chat; no management shortcut, artifact list or download buttons. Resources are created through the API. Selecting a Host or Sandbox never changes task routing. Selecting an assistant selects its conversation owner; machine groups do not change that selection.
- **Chat file downloads**: completion summaries report the task result; file names/download links are optional and appear only when the task has files to deliver. Clicking downloads the task file from its execution machine through Control to the current client. App asks for a save location; Web uses the browser's normal download flow. Control is a relay, not the save destination. The link binds a specific file and its task/assistant; it cannot expose arbitrary Host paths. Offline machines or missing files produce an explicit error. Machines and Task pages have no artifact lists or download buttons. Links use `pot-file://TASK_UUID/encoded/relative/path` in ordinary Markdown; existing tools and their arguments are unchanged. Downloads are limited to regular files within the task workspace (1 GiB maximum); symlinks, traversal and detected changes are rejected. App writes a private temporary file and replaces the chosen destination only after completion.
- **Remote Task execution**: start a Task Agent on the same machine as its Host/Sandbox; Task details show status, machine and Host/Sandbox location. Reuse existing guidance, cancel and approval interactions. No Runner management page, Agent migration controls, separate Agent placement selector or global plugin/skill management panel. Global resources are managed by Control through API/Chat; new model-visible tool contracts require explicit agreement.
- **Tray**: one pot-shaped status icon; click opens window, no menu. Unread count beside icon. Window close keeps background work; Quit stops the local Runtime normally.

## Implementation boundaries

- Rust / Kit: chat, tasks, settings, Sandbox title/toolbar, unready states.
- Swift: window/sidebar/tray/native chat input/configuration/lifecycle. `SettingsPanel.swift` is configuration service only, not settings rendering. Restart confirmation uses native sheets; Desktop viewer menus remain confined to the viewer.
- WKWebView / noVNC: desktop canvas/Sandbox details/clipboard/scaling. Standalone viewer retains its web More menu; shared web implementations use neutral colors, 8 px radii, 13-14 px text, synchronized themes. Hide web title bar in App to avoid duplicate toolbars.

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

## Agent approval cards

Native/Web conversations show approval cards separately from model messages, with
assistant, machine and Host/Sandbox capability. Actions are Always allow, Allow this work,
Deny, in that order; saved grants can be revoked. See [Agent permissions](permissions.md).

## Machines and desktop

Machines is a top-level destination combining machine and Host/Sandbox status.
Cards show name, connectivity and running Tasks. Expanding shows Host/Sandbox
rows; selecting a Host or Sandbox shows the Tasks bound to that exact target.
Offline data is marked cached and new operations are disabled. Local supported
Sandbox desktops reuse the existing viewer entry. Remote desktop transport is
deferred until its connection design is agreed; no remote desktop entry is added
in the current #39 scope. Viewing never changes a task target or starts a stopped instance.

Technical management stays in ordinary Chat/API. There are no resource creation,
plugin configuration, session, artifact/download or “ask assistant to manage”
controls on Machines. Resource creation UI is deferred. Task details keep necessary
machine and Host/Sandbox status and existing guidance/cancel/approval interactions.
File links appear in Chat only for tasks with deliverables.

## AI Configuration (#7)

Unconfigured native Chat offers AI Configuration and Connect server. AI Configuration reuses the Settings page; Connect server opens the existing Machines connection form, which offers Open server chat once a Control is configured. The Pot Terminal page, side-rail button, xterm assets and native renderer have been removed. Sandbox remains independently accessible. API setup never requires a Terminal.

AI Configuration contains a compact Main Agent summary, Integrated Agents list and one shared connection panel. The catalog uses colored icons: OpenAI API, Claude API, Gemini API, DeepSeek, OpenCode Go, OpenCode Zen and Custom API. Existing API connections remain editable. Removed native CLI configurations are unsupported and require fresh configuration; no compatibility migration is provided. Main-Agent changes stay in AI Configuration, not the Chat header.

Preset API forms show only key/model and a Get API Key link. Endpoint/protocol are supplied by Pot. Claude has no account/API mode selector. Custom API shows endpoint and four explicit formats: OpenAI Chat Completions, OpenAI Responses, Claude Messages and Gemini. Model-list results appear in a picker alongside the editable model field.

Check and Save are separate adjacent buttons. Check performs supported model-list GETs without inference or saving. Save validates configuration through the existing Runtime API; it does not force a separate Check click or claim successful inference. Unsupported model discovery permits manual input. Provider errors preserve drafts; secrets are masked and excluded from public state. Switching connection forms clears secret fields.

OpenCode uses a maintained per-plan, per-model route table in `crates/app/swift/APIIntegration.swift`, checked against official documentation on 2026-10-06. Go and Zen may serve the same model through different formats. Unknown formats/models are excluded from discovered options and require a catalog update or explicit Custom API configuration; names alone do not determine the adapter.

For an empty main configuration, the final connection action also sets the Main Agent at a safe turn boundary. Entry from Chat uses Save and start chatting and restores its draft without sending. Settings explains Save and set as Main Agent. Existing main configurations are not replaced by ordinary additions. Save retains a stable draft connection identity and reconciles partially completed saves before retrying.

The existing Runtime PTY subsystem is retained; removal of this separate subsystem was also rejected by automatic review. This is separate from the deleted product Terminal UI and from Agent shell/tool execution.

Main Save queues a Thread configuration update. Pending operations disable duplicate submission and explain current-turn/paused-request waits; only the applied Thread configuration is displayed as effective. Context is preserved across provider/model changes. Existing task configurations remain pinned. New tasks following Chat use its active snapshot rather than an unapplied profile edit.

Task Configuration preserves existing explicit worker defaults/routing and allows choosing Follow Chat. Advanced profile editing/copy, allowlists, profile/provider caps and allowances remain available. Task-specific Change Agent targets only that task and reuses AI connection management; completion returns to the task. Credential-independent product limits use private `execution-limits.json`, without implicitly selecting Pot or requiring a DeepSeek key.

Unconfigured Chat lets the user keep browsing without configuring an Agent. There is no Sandbox/tool installation wizard.

Native assistant Chat adds a compact card beneath a settled, unsuccessful task
report when its diagnostic identifies a missing usable desktop. It says
“The task Agent failed because no usable desktop was found” with two actions:
Create desktop Sandbox and Connect desktop machine. These reuse the existing
Sandbox creation sheet and Machines connection form. Creation from this card does
not select an execution target, start a desktop, send a message, retry a task or show an
extra success confirmation. Users ask the assistant to try again themselves.
Creation errors and required resource-download authorization remain visible.
Detection uses the product's unsuccessful report wrapper and recognized English
and Chinese missing-desktop diagnostics; arbitrary model paraphrases are not a
structured failure signal. Successful/cancelled reports, user text, transport,
permission and unqualified task-capability errors do not trigger the card.

Status refresh never selects an Agent or makes paid model probes. Real-account/engine matrices are recorded separately in dated acceptance reports.

Global/session usage both show Agent / model usage grouped by model,
including input/output/input-cache/incomplete records; missing attribution shows Unknown Agent.
Global categories follow selected ranges; details use session totals, 20/page with previous/next.
Category pages do not change totals/trends/allowances; range/session/zone changes reset pages.

## App Spaces and Web hosting

App's local Runtime includes an embedded Control and starts/exits with App; it
cannot open the standalone Control listener. Users may also connect an independently
deployed remote Control; additional execution machines join that Control.
Assistants from both Spaces appear together with local/cloud ownership icons.
Drafts, caches and cursors are scoped by Control/assistant. Remote failure never
selects local execution. Assistant message relay operates independently of the
selected assistant; see [Assistant messages](assistant-messages.md).

Web UI is explicitly enabled by `pot serve --ui --control URL` and remains fixed
to that Control. It has no local/remote switch. App has no Web UI/Tailscale hosting
settings. Browser Token controls belong to the standalone host; see
[deployment](deployment.md).
