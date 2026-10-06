# Pot design language

Approved design direction: 2026-10-06. Tracking: [issue #9](https://github.com/jjyr/pot/issues/9).

This document defines the target visual and interaction language for Pot. It is a design baseline, not a record of shipped behavior. The [UI specification](ui-ux-design.md) describes the existing implementation; update it as changes are delivered. Authentication, execution, accounting and desktop-control capabilities still depend on their respective implementation contracts.

## Principles

Pot should feel like a refined Mac application: clear typography, compact controls, predictable navigation and responsive feedback. Use a bright, almost white window matching the chat canvas, a quiet light-gray sidebar and toolbar, and soft blue user messages. Distinguish surfaces with small luminance differences, fine dividers and restrained shadows.

Keep color sparse. Blue identifies primary actions and focus; green, amber and red communicate status alongside words or icons. Small touches of playful motion provide liveliness without changing the native foundation. Avoid large contrasting cards, decorative gradients, extensive glass effects and repeated bouncing. Mac styling does not require a darker gray window or saturated message bubbles.

Implement semantic tokens consistently across GPUI, AppKit and embedded web surfaces. Prefer native macOS text rendering, icons, menus, sheets and system behavior where supported. A native component must still fit the application's surface hierarchy; do not mix unrelated control styles between pages.

## Color and surfaces

The following values are reference tokens. Validate actual rendered contrast in light and dark modes; native controls may follow the user's system accent. The light palette is the primary visual reference, while System / Light / Dark appearance remain supported.

| Token | Light | Dark | Use |
| --- | --- | --- | --- |
| Canvas | `#FDFDFD` | `#202020` | Unified window and content background, including Chat |
| Surface | `#FFFFFF` | `#2B2B2B` | Composer, forms and grouped content |
| Navigation | `#F4F4F4` | `#282828` | Quiet sidebar and toolbar |
| Primary text | `#262626` | `#F2F2F2` | Titles and body |
| Secondary text | `#646464` | `#B7B7B7` | Descriptions and metadata |
| Divider | `#E8E8E8` | `#414141` | Quiet separators and borders |
| Accent | `#0068DA` | `#0A84FF` | Primary actions, links and focus |
| Selection | `#ECEFF3` | `#414141` | Subtle selected navigation/control surfaces |
| User message | `#E7F2FF` | `#243A52` | Soft blue message fill |
| User message text | `#25435F` | `#E3EFFF` | Text on user message fill |
| Success | `#247844` | `#78BE8A` | Completed or connected |
| Attention | `#885B13` | `#D7AF66` | Needs attention |
| Error | `#BD342D` | `#FF6961` | Errors and destructive actions |

Toolbar/sidebar light surfaces may vary between `#F5F5F5` and `#F6F6F6`. The light-mode send control may use `#3478F6`; navigation highlights may use `#E8EEF7`. These are contextual variants, not additional page-specific palettes.

Keep assistant responses mostly unboxed. Reserve surface fills for the composer, interactive groups and relevant details. Use a fine composer border and a soft shadow; avoid giving every row a floating-card treatment. Separate control boundaries from decorative dividers: a control must remain identifiable even when a faint divider is unnecessary.

Target WCAG AA text contrast. Secondary text must remain readable against its actual surface. Color never carries status alone, and an outline must not be the only indication of keyboard focus.

## Typography and geometry

Use the system font (`.AppleSystemUIFont` / AppKit system font). Preserve native Chinese input and text rendering. Use monospaced text for commands, code, IDs and aligned numeric data only.

| Role | Size / line height | Weight |
| --- | --- | --- |
| Page title | 24 / 30 px | Semibold |
| Section title | 16 / 22 px | Semibold |
| Body | 14–15 / 22 px | Regular |
| Description / metadata | 13 / 18 px | Regular |
| Toolbar / compact control | 12–13 px, native line height | Regular or medium |
| Dense form section label | 14 px, native line height | Semibold |

Use a spacing scale of 4, 8, 12, 16, 24 and 32 px. Standard pages have 32 px outer padding and centered content up to 820 px; conversations use up to 760 px. Sandbox uses the full available canvas. Preserve usability at 800 × 700.

| Element | Corner radius / size |
| --- | --- |
| Compact buttons and fields | 5–6 px radius; native-style controls may be 28 px tall |
| Selected sidebar control | 10 px radius; 36 px hit area |
| Grouped form/list surface | 9–10 px radius |
| Composer | 16 px radius |
| Menu | Approximately 7 px radius; prefer native rendering |
| Sheet | Approximately 12 px radius; prefer native rendering |
| Standard icon | 18–20 px, with optical alignment |

Avoid uniformly applying one large radius to every element. Touch-oriented surfaces need larger hit areas separately; compact Mac controls are not a mobile sizing specification.

## Application shell and navigation

Keep the compact 56 px icon-only sidebar: Chat, Tasks, Sandbox, Usage and Settings. The Pot icon is the Chat entry; there is no separate brand block or chat-bubble entry. Show names in hover/focus tooltips and accessibility labels. Place Settings at the bottom. Tasks contains Tasks / Schedules rather than another top-level navigation entry.

The target design removes the user-facing Terminal page, consistent with [AI configuration #7](https://github.com/jjyr/pot/issues/7). This does not itself remove shell tools or background agent execution.

Use one shared 56 px toolbar below the native titlebar: Back, Forward, destination title and contextual actions. Keep native window controls. Task details show the task title with a Tasks context, truncation and a full-title tooltip. A task conversation belongs to Tasks for sidebar selection. Do not duplicate a second back row inside the page.

### History

Maintain window-scoped browser-style navigation history. Start on main Chat with Back and Forward disabled. Store route identity separately from route-local view state.

- Primary destination changes, task/schedule details, conversation Usage and deliberate jumps from linked results add entries.
- Opening the currently active route is a no-op. Going Back and choosing another destination discards the forward branch.
- Filters, search, pagination, local tabs and expanded groups update local state without adding history entries.
- Returning restores filters, page, scroll anchor, per-conversation draft and unsaved settings. Prefer a task ID plus offset to a raw list offset when live sorting changes.
- Settings remembers its selected section; changing sections does not consume history.
- Navigation never implicitly sends a draft, saves configuration, stops execution or changes desktop-control ownership.
- History lasts for the current window lifetime; cross-restart history is outside this design.

Provide Back / Forward buttons, disabled states and tooltips with `⌘[` / `⌘]`. Route shortcuts through the native menu/responder chain. Do not override IME composition or embedded desktop shortcuts while that content owns focus.

Open cached content promptly and refresh in the background. Ignore stale responses for destinations that are no longer active. Missing destinations show an unavailable state and a route to the relevant list, without silently substituting another task.

## Components and feedback

### Actions and selection

Use one blue primary action per action group, neutral secondary buttons and quiet message actions. Destructive actions have explicit labels; use red sparingly. Hover, press, selection, focus and disabled states remain distinct and preserve dimensions. Essential actions must be available to keyboard users and cannot depend solely on hover.

Use compact neutral segmented controls for local choices. Their selected item has a subtle raised or inset treatment rather than a large colored pill. Icon actions have tooltips, accessibility names and disabled states. Use native pop-up controls where appropriate.

### Forms

Group fields by intent using section headings and quiet row dividers. At desktop widths, align labels to the right of a consistent label column; stack labels above fields when space is limited. Keep descriptions and errors beside the field they explain. Show units for numeric resources.

Focus the first invalid field on submission. Preserve unsaved values during navigation and background refresh. Optional advanced fields use a disclosure; ordinary setup must not require an extra Advanced page just to finish.

Keep Save / Cancel actions stable and reachable when a form scrolls. During a slow save, prevent duplicate submission and reserve the button's width. Show success only after acknowledgment. A saved confirmation must not substitute for a persistent validation or recovery message.

### Menus, sheets and destructive actions

Anchor menus to their trigger. Escape closes the menu and restores focus. Prefer native menus to avoid embedded WebView clipping. Sheets name the action and its concrete impact; default focus goes to Cancel when the action interrupts work. A resource-restart sheet states which running tasks are affected and which data is retained.

Keep the existing second-click confirmation for deleting a schedule. Pausing/deleting a schedule prevents future triggers without cancelling an existing run.

### Loading, empty and error states

Distinguish no data, no search matches, initial loading, stale-content refresh failure and unavailable destinations. Empty Tasks points users to Chat; it does not introduce manual task creation. No-match results offer clearing the search. Initial loading reserves list structure without fabricated progress.

On refresh failure, keep usable content and place Retry beside the error. Persistent failures have persistent inline recovery information. Small transient feedback is suitable for Copy or successful Save; it must not carry the only explanation of an unresolved error.

### Content and linked results

Use consistent Markdown headings, body text, quotes and code blocks. Reserve message-action space so Copy / View task appearing does not move content. Result rows include a file icon, title, concise context and a clear viewing action; no additional top-level destination is required.

Guidance states are truthful: Received is not Applied until the Agent consumes it. Status and selection colors are always paired with visible text or recognizable symbols.

## Page patterns

### Chat and task conversation

Use the bright unified canvas, soft blue user bubbles and mostly unboxed assistant responses. Keep a native IME composer with a white light-mode surface, fine border, restrained shadow and stable send/stop control. Do not animate every streamed token.

Auto-scroll only while the reader remains near the bottom; otherwise expose the latest-message action. Keep task stage, archive state, pending guidance and conversation Usage available. Navigating away does not stop a task. Empty-chat examples may insert a draft but must not send it automatically.

#### Running-task summary below the latest assistant message

In main Chat, place a compact live summary directly below the latest assistant message. This replaces the proposed task-status entry in the Chat header; do not add a duplicate header summary or a hover interaction. The summary is transient UI, not generated assistant prose or a persisted part of message history. Move it with the latest assistant message rather than copying it below earlier messages.

Each row contains a small activity dot, task title and concise current progress, for example:

```text
· Organize design references    Comparing sources
· Summarize the weekly report   Preparing the summary
```

Use 12–13 px system text in the secondary text color. Keep the summary subordinate to the assistant reply, with compact row spacing, no card border, no filled container and no primary action styling. Truncate long titles/progress without obscuring the row's accessible name. Clicking a row opens that task's conversation through normal navigation history; keyboard activation does the same.

Show only background tasks actually executing. Queued, completed, cancelled, interrupted, failed and waiting-for-user tasks do not belong in this summary. Use authoritative execution state rather than a stale progress sentence; when execution ends, remove its row and do not retain a completion receipt here. Hide the whole region when no background tasks are executing. Show up to three rows, with an “N more running” entry linking to task inspection when needed.

To indicate ongoing activity, the small dot slowly changes brightness or shifts between secondary gray and a muted accent tone over approximately 2.4–3 seconds. Keep title/progress text stable and readable; do not flash, animate every word or synchronize multiple rows into a conspicuous effect. Activity motion runs only while execution is active and visible. Progress replacements may fade in briefly; ending rows collapse softly without forcing the reader to scroll or moving keyboard focus unexpectedly. Under Reduce Motion, use a static activity dot and immediate state updates. Expose a textual Running state to assistive technology, independent of the dot's color or motion.

### Tasks and schedules

Keep the title, Tasks / Schedules switch, state groups and search outside the scrolling list; pagination stays at the bottom. Use grouped rows rather than separate cards for every item. Each task row shows title, useful context, textual status and change time.

Retain global Ongoing / Needs attention / Archived counts, 20 items per page and no manual creation. Search/group changes reset pagination. Schedules show next run, time zone, notification policy, missed-run policy and history linked to tasks. Editing groups frequency, time, zone and policy with stable Save / Cancel actions. See [Scheduled tasks](scheduled-tasks.md).

### Settings and first connection

Use General / AI Configuration / Task Configuration / Work Environment, aligned with #7. General contains appearance and shortcuts. AI Configuration shows the main Agent/model first and saved connections below. Task Configuration groups concurrency and allowances. Work Environment contains Sandbox resources and lifecycle.

Use one compact provider grid and shared connection panel. Browser authorization progresses from Not connected to Waiting for authorization to verified account/model selection in the same panel. Keep Reopen browser / Cancel available while waiting. Unknown status is not Connected.

Provider API forms keep Check / Save adjacent. Custom API exposes endpoint, key, format and model. First setup ends with Save and start chatting, restores the Chat draft and does not automatically send it. Ordinary additions do not silently replace the main Agent. See #7 for authentication and activation behavior; previews do not establish backend support.

Persistent task-executor preferences follow [#3](https://github.com/jjyr/pot/issues/3) and remain managed through Chat in its first version; do not add a routing-rule editor as a design prerequisite.

### Usage

Use 7 / 30 / 90 days / year, compact totals, one trend region and readable Agent/model and tool rankings. Keep category colors consistent and numeric labels neutral. Model consumption and estimated tool tokens remain distinct. Show allowance cards only when configured. Conversation Usage is its own history destination. Retain existing aggregation and incomplete-record semantics; see [Token usage](token-usage.md).

### Sandbox

Keep the desktop usable and full-width. The toolbar shows navigation/title, textual connection state, Take over / Hand back, environment information, reconnect, settings and More. At minimum width, move low-priority actions into More.

Connection and control ownership reflect actual acknowledgment; viewing or navigating does not reclaim control. Show connected/observing, human control, starting, disconnected/retry and unavailable states. Environment information uses a compact resource panel. Mobile pairing/access remains separate under [#8](https://github.com/jjyr/pot/issues/8).

### Menu-bar Pot activity and unread indicator

The confirmed design combines a pot-shaped execution indicator with a compact numeric unread badge. These represent independent facts: the pot shows whether work is executing, and the badge shows unread main-chat replies. Replace the existing bare number presentation without changing unread accounting or the click-to-open-window behavior. Do not add a tray menu or rely on hovering to explain the state.

| Execution state | Pot appearance |
| --- | --- |
| Neither main Chat nor a background task is executing | The existing empty pot |
| Main Chat is processing or at least one background task is executing | A pot visibly simmering, with a few simple contents/boiling curves and restrained steam |

Keep the small icon legible and predominantly monochrome. Depict cooking rather than detailed individual ingredients: simplify the contents to short boiling curves and, where legible, a small steam mark. The change from empty to simmering must be visible even without animation. A queued or waiting task alone does not make the pot cook. Return to the empty pot only after both main Chat and executing background work are idle.

While actively cooking, boiling/steam may change gently over a slow cycle. Avoid a continuously bouncing pot, flashing whole icon or exaggerated lid movement. Stop the activity animation when idle; under Reduce Motion, show a static simmering pot. Preserve configuration/error distinctions without interpreting them as unread messages or active execution.

Use the selected **compact numeric badge** next to the pot: fixed count area, tabular digits, a soft accent fill and `9+` above nine unread replies. Hide the badge at zero, retaining fixed status-item width so other menu-bar items do not move. Do not also show an unread dot or a second trailing number. An empty pot can have unread messages, and a simmering pot can have none.

Set the exact unread count and actual execution state in the accessibility label; no hover tooltip is required for this design. Clicking opens Pot and does not itself mark all messages read. Retain the current main-chat completion/read-at-latest rules. Badge arrival may use a single 180 ms opacity/scale transition from zero to positive, without repeatedly pulsing on refresh or count changes. Validate real menu-bar backgrounds, selected-button appearance, Retina scale and VoiceOver before implementation acceptance.

## Motion

Motion supplies liveliness through continuity and tactile feedback. Use gentle settling, typically `cubic-bezier(.22,1,.36,1)`, with no repeated bounce or overshoot. Every effect follows a user action or a meaningful state transition. Do not delay input or block controls for animation.

| Interaction | Target motion | Constraint |
| --- | --- | --- |
| Hover / press | 120–160 ms color/opacity; press scale 0.97 with 180 ms settling | Stable layout and hit area |
| Sidebar selection | One subtle highlight moves over 220 ms | Immediate initial positioning; focus remains independent |
| Page change | 180 ms fade with 3 px translation | Toolbar stays fixed |
| Disclosure | 180–220 ms height/opacity | Preserve scroll and focus |
| Composer focus | 150 ms border / 200 ms shadow | No size change |
| Menu entry | 160 ms; 3 px translation / 0.985 scale | Trigger-relative origin; immediately interactive |
| Sheet entry | 200 ms; 6 px translation / 0.985 scale | Backdrop fades once; correct initial focus |
| Copy / Save acknowledgment | Inline text/check; 180 ms check settling from 0.85 scale | Reserve width; acknowledge actual success |
| Running-task dot | Gentle brightness/muted-color cycle, approximately 2.4–3 seconds | Only for executing, visible tasks; stable text; static under Reduce Motion |
| Running-task progress / removal | Brief progress fade; 180–220 ms row collapse | No forced scrolling, stale completed rows or lost focus |
| Pot execution icon | Gentle simmering/steam cycle while work executes | Static empty/simmering shapes remain distinct; no whole-icon bounce |
| Loading | Small indicator with reserved space | No fabricated percentages or decorative looping |

Activity cycles are semantic execution indicators, not decorative loops: run them only while work is executing and the relevant surface is visible. Respect Reduce Motion: remove animated transforms, activity cycles and disclosure transitions, while preserving necessary selection positioning. Use static execution indicators and immediate state changes or restrained opacity feedback. Announce meaningful results accessibly and do not announce every hover, activity cycle or streamed token.

## Implementation and validation

2026-10-06 implementation: Chat light/dark colors, pale user bubbles, unboxed assistant messages, the native composer, running-task summary and Pot cooking/unread icons are implemented. Task progress uses runtime tool/reply states rather than invented semantic progress descriptions. Summary removal hides finished content immediately and briefly collapses the released space. Shared light/dark tokens, the 56 px contextual toolbar and Back/Forward history, grouped task rows, four Settings categories with compact AI connection choices, responsive forms and stable action footers, Usage styling, and native/embedded Sandbox toolbar styling are also implemented. Existing saved execution settings remain available under a collapsed advanced disclosure. After rebasing onto #10, the upstream browser authorization, provider integration and Terminal removal are preserved. Its provider grid, connection panels, model pickers and task configuration now use the shared design tokens, compact controls and responsive labels. The connection sheet enters with a restrained 200 ms fade, disabled under Reduce Motion. See [UI validation](validation/ui-ux.md) for verified scope and remaining native checks.

Implement shared tokens and shell/history first, then conversation/task patterns, settings/usage and embedded toolbar consistency. Update the current UI specification as behavior ships. Do not change authentication, task bindings, accounting or desktop-control semantics through styling alone.

Design previews contain illustrative counts, charts and files, a placeholder desktop and simulated login/save/control feedback. Their approval establishes visual and interaction direction, not real-provider or native-rendering acceptance. Full authorization failures, complete usage details and mobile access still require their own designs and implementation verification.

Validate light/dark, 800 × 700, long Chinese titles, keyboard focus, IME input, per-conversation drafts, unsaved settings, scroll/filter restoration, history branching, missing routes, live updates during navigation, loading/errors, reduced motion and embedded desktop focus. Verify pot activity across main-only/background-only/concurrent execution and queue/wait/finish transitions; unread badges remain independent. Verify summary placement below only the latest assistant message, running-only membership, overflow, progress changes, row removal and navigation without forced scroll/focus changes. Check rendered text/control contrast. UI-only verification requires no paid model calls. Compilation and a real App smoke test are necessary before claiming the design is delivered.
