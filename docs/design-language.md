# Pot design language

This is the shared App/Web reference for visual language, responsive navigation
and page interactions. Code defines current behavior. Platform responsibilities
belong in [Shared UI](shared-ui.md), ownership in [Multi-Space UI](multi-space-ui.md),
and build and regression commands in [Development](development.md#checks).

## Visual language

Use quiet surfaces, system typography, fine dividers and restrained color. Blue
identifies primary actions and focus; status colors accompany text or icons.
Assistant replies use quiet neutral bubbles; user messages use a soft blue fill. Reserve
cards for related controls or details, rather than wrapping every row.

### Color and surfaces

[`styles.css`](../web/src/styles.css) installs the shared semantic tokens.
System / Light / Dark appearance is available in General settings.

| Token | Light | Dark |
| --- | --- | --- |
| Canvas | `#FDFDFD` | `#202020` |
| Surface | `#FFFFFF` | `#2B2B2B` |
| Navigation / titlebar | `#F4F4F4` | `#282828` |
| Assistant pane | `#E9EEF5` | `#303640` |
| Assistant selection | `#D9E4F3` | `#3B475B` |
| Primary text | `#262626` | `#F2F2F2` |
| Secondary text | `#646464` | `#B7B7B7` |
| Divider | `#E8E8E8` | `#414141` |
| Input border | `#D6D6D6` | `#555555` |
| Primary action / link | `#0068DA` | `#0A84FF` |
| Secondary control | `#ECEFF3` | `#414141` |
| User message fill | `#E7F2FF` | `#243A52` |
| Success | `#247844` | `#78BE8A` |
| Attention | `#885B13` | `#D7AF66` |
| Error / destructive action | `#BD342D` | `#FF6961` |

Keep text readable against its actual surface, targeting WCAG AA contrast.
Selection, focus and status need more than color alone. Default assistant avatars
keep their colors across themes; see [Assistants](#assistants).

### Typography and geometry

The native App uses the system-font stack; browsers retain IBM Plex Sans with
system-font fallbacks. Both use the same type hierarchy and geometry. Use
monospace for code and identifiers. [`primitives.mjs`](../web/src/components/primitives.mjs)
owns page, form, control and editor primitives:

| Primitive | Current geometry |
| --- | --- |
| Content column | Up to 820 px including 28 px inset, reduced below 900 px |
| Conversation / editor column | Up to 760 px |
| Page / section title | 24 / 16 px, semibold |
| Page body | 14 px; 16 px below 720 px |
| Description | 13 px; 14 px below 720 px; regular, 1.5 line height |
| Card | 18 px padding, 12 px below 720 px; 10 px radius |
| Form | 184 px label column; stacked labels below 900 px |
| Control / composer radius | 6 / 16 px |

Shared button/input controls adopt 44 px height and input
text of 16 px on narrow windows or touch-capable browsers. Other custom controls
own their geometry; this is not a global minimum applied to every element.
Desktop mouse controls stay compact. Wide iPad windows retain touch density.
Wrap secondary actions and keep labels readable rather than shrinking buttons.
Keep copy concise; put detailed operating explanations behind disclosures or in
field help.

## Application shell and navigation

Phones, iPads and computers use the same shell, selected by available window width
rather than device names. The phone reference is iPhone 14 Pro at 393 CSS px;
390 px is the portrait release target floor. Smaller screens are not release
targets. iPad split windows follow the same breakpoints.

| Available width | Layout |
| --- | --- |
| Below 720 px | Full-width assistant directory or conversation; global navigation opens as a full-screen menu |
| From 720 px | 56 px feature rail; Chat adds a collapsible 220 px assistant pane |
| Below 900 px | Compact page spacing and stacked form labels, independently of the navigation layout |

The rail contains Chat (Pot icon), Tasks, Machines, Usage and bottom-aligned
Settings. Tasks includes Tasks / Schedules. Other destinations and editors hide
the assistant pane; wide editors retain the rail. A single 44 px titlebar holds
the destination, Back and contextual actions. Native window controls and draggable
space share this row; avoid duplicate title rows.

### Narrow surfaces

A fresh narrow Web visit opens Assistants; an explicit conversation link opens
that conversation. Selecting an assistant opens full-width Chat. Returning keeps
the directory search and scroll, and each conversation retains its draft, cached
history and reading position.

Root pages expose a 44 × 44 px menu button. A rightward swipe beginning within
24 px of the left edge also opens the full-screen menu with Assistants, Tasks,
Machines, Usage and Settings. A destination, Close, Escape or a leftward swipe
anywhere in the menu dismisses it. The menu follows the finger; a quarter-width
swipe, capped at 100 px, commits, while a short, reversed or cancelled swipe
settles back. Reduced Motion removes the settling animation.

Conversation and editor pages show Back instead of the root menu button. Their
left-edge swipe uses the same Back path, including the dirty-editor guard.
Vertical scrolling and horizontal content gestures away from the left edge stay
with the content. Conversation actions are available through the ellipsis menu.
The shell follows the visible viewport as browser chrome or the keyboard changes;
editor input reveal keeps the focused supported input in view.

### Parent navigation

Back and ⌘[ follow page hierarchy: task → Tasks, schedule → Schedules,
conversation Usage → conversation, allowance editor → overall Usage. Narrow Chat
returns to Assistants; wide Chat hides Back. Root lists, Machines, Usage and
Settings have no parent button. There is no forward button or ⌘] binding.
Editors instead restore their captured caller as described below.

Navigation caches filters, pagination, scroll and expansion state. The assistant
directory belongs to the window; conversation and task state is scoped by owner.
Tasks and Schedules keep separate list states. Navigation does not send a draft,
stop execution or choose an execution target. Missing task destinations expose
recovery rather than silently opening another owner.

### Editing pages replacing complex modals

AI connection/model configuration, machine connections, schedules and assistant
settings use editing pages on App and Web so phone forms can use the available
viewport. Small menus and confirmation dialogs remain transient surfaces.

Shell supplies one contextual titlebar with Back. AI, machine and schedule editors
use the shared editor form and bounded scrolling body. On narrow
windows, the action area wraps to the available width. Assistant forms share
the same return and dirty-exit handling. Opening a phone editor does not force
keyboard focus. Input entities retain drafts across resizing and preference changes.

#### Return context and draft lifecycle

Entering an editor captures the caller's route and presentation: conversation,
Settings section/configuration location, filters, pagination, Usage range,
expanded rows/panes and scroll positions. Nested AI steps retain parent input
entities and draft checkpoints. Returning restores this presentation alongside
current live data, without rolling back backend state. Source focus is captured
only at widths of at least 720 px, avoiding keyboard activation on phones.
A changed Control/Runtime identity produces an error instead of restoring an old
context into the new owner.

| Exit | Behavior |
| --- | --- |
| Back / Cancel, unchanged | Restore the caller |
| Back / Cancel, changed | Offer Keep editing or Discard changes; discard restores the caller |
| Other in-app navigation, changed | Use the same guard, then open the selected destination |
| Save acknowledged | Restore the captured caller; Chat AI setup retains its return-to-chat behavior |
| Save fails | Keep the editor and draft available for correction or recovery |

Back, ⌘[, narrow edge Back and browser Back within the active editor session use
the shared leave guard. Pending requests block leaving while their result is
handled. Saving never occurs implicitly on exit. Editor sessions are memory-only;
browser history holds route/session markers, not form values or credentials.
This guard does not provide draft persistence across reloads or browser closure.
Configuration request reconciliation is described in
[Shared UI](shared-ui.md#configuration-and-asynchronous-actions).

## Page patterns

### Assistants

The directory and wide pane share rows containing avatar, name, ownership badge,
one-line preview and unread count. Search filters names without changing the
selected conversation. Registry order remains stable; duplicate names include
the Space name. Initial history does not count as unread. Opening the conversation
clears its indicator. Offline, approval, configuration and activity previews
reflect their own states rather than changing the avatar.

Default avatars compose the canonical Pot Companion with eight foreground and
eight background colors. A stable hash of the creation request identity selects
one of 64 combinations, unchanged by theme or rename. Creation previews, rows
and titles share the image. A new completed live reply triggers one 650 ms whole
image scale-and-tilt rebound. Initial history, duplicate updates and reconnect
catch-up do not replay it; hidden pages do not queue reactions.

First-use Chat offers Connect AI provider when no connection exists. Saving that
connection returns to Chat and automatically creates Pot using the Space's default
Provider/model. An empty Chat with an existing Provider follows the same bootstrap
path. Offline or failed directory loads offer recovery rather than connection setup.
The empty directory caption is No assistants yet.

Every newly created assistant starts with a fixed welcome message in the creation
UI's language, introducing its saved name and inviting the user to name it or
describe how it can help. The welcome is committed to both the durable transcript
and the model checkpoint, so the first model request includes it as an assistant
message. It makes no inference call, generates no unread notification and is never
reinserted or translated on reopening. Existing conversations are not backfilled.
The assistant can save a user-requested name through `rename_assistant`; directory,
title and settings use that same identity. Workers and peer conversations cannot
rename the assistant.

The + action opens creation with name, destination Space, Provider and model
visible. The destination Space's default Provider is preselected, and the model
field contains that Provider's concrete default model ID, never a synthetic
"Default model" choice. Changing Provider selects its default model. Changing
Space loads that Space's Providers and defaults. Saving a Provider within this
form resumes the draft without creating or selecting an assistant; registration
occurs on Create and start chatting, and retries reuse its request identity.

Creation and assistant editing share the same configuration controls. Existing
assistant location is read-only. Provider/model changes are saved on the assistant
without changing the shared Provider or other assistants. Model parameters and
assistant instructions start collapsed; expanding instructions reveals AGENTS.md.
Nested Provider setup preserves the assistant draft and returns to its caller.
Create and start chatting saves the assistant and opens its conversation.
Assistant settings contain no task routing controls: the assistant chooses task
models autonomously, guided by its instructions.

### Chat and task conversation

The composer, send/stop control, draft ownership and reference completion are
shared. During a running turn, a nonempty draft offers Send guidance; an empty
draft offers Stop. The conversation menu exposes Pause/Resume, with pausing
applied at a safe model boundary. All hosts use a browser/WebView-native textarea and IME. `/`
completes machine, label and Skill references at the caret; arrows/Enter, Escape
and click/tap operate the same menu. See [Agent context](agent-context.md).

Cached history appears before refresh. Auto-scroll follows only readers near the
bottom; otherwise a latest-message action appears above the composer. Copy and
View task actions reserve space and remain reachable on touch. Empty-chat examples
insert drafts without sending them. Task conversations add a fixed stage/action
row and expose guidance, cancellation, approvals and Usage. Long task titles have
full-title tooltips.

Main Chat shows up to three executing background tasks below only the latest
assistant message, with a dot, title and overflow entry. Rows open task details;
queued, waiting and finished tasks are excluded. The region is presentation state,
not persisted assistant prose. Queued guidance has a clock with a Received tooltip;
applied instructions do not retain a status caption.

Sending immediately creates one optimistic user message and assistant reply
bubble. Its identity survives acceptance, Thinking, streamed output and completion;
the composer and current reading position remain intact. Waiting dots use a smooth
1.4-second staggered cycle, and streamed output retains a small activity dot.
Completed, paused, stopped and allowance-waiting replies have no activity animation.
Reduced Motion uses static indicators.

All failed replies share one presentation in that same bubble: a soft error fill,
error icon, concise reason and expandable original diagnostic. Partial prose stays
above the error within the bubble. Configuration failures offer one Check AI
settings action. A definitively rejected send retains its draft and offers Retry
with the same request identity; editing the draft disables that retry. Failure is
not repeated in a separate notice below the composer. Persisted failures remain
readable after navigation or restart.

Connection loss during a reply or uncertain send updates the bubble to
Reconnecting, without declaring execution failed or automatically sending again.
Recovery reads authoritative state. A connection notice below the composer is
used only when no pending send or active reply already owns that status.

Task file links download through the owning Control; App requests a save location
and Web uses browser downloads. Delivery constraints belong in
[Deployment](deployment.md). Native Chat can offer desktop recovery actions for
recognized unsuccessful-task diagnostics; these reuse existing connection/resource
forms without automatically retrying the task.

### Tool calls

Chat and Task details share compact inline tool disclosures. The closed row shows
the operation, a deterministic argument preview, explicit status and elapsed time;
active operations also show their resolved machine and workdir.
Failures keep a concise reason visible while collapsed. Expanded details show
arguments, ordered output, final results and a secondary raw call/result disclosure.
Commands, scripts and patches retain monospace formatting; supported image results
render inline. No tool action implicitly repeats an operation.

Ordinary calls default to collapsed. Composition children nest under their original
`exec` call in start order. Exact approval requests open the affected call and its
ancestors, show the full reviewed operation, and provide Allow this operation / Deny.
A compact locator exposes pending approvals outside the current reading position;
requests without a transcript row retain a standalone approval card. A failed
approval submission keeps its diagnostic and controls available. Long-lived machine
policy remains in Machines.

Live output has bounded height and follows only while its reader stays near the
bottom; Jump to latest resumes following. Expanding, copying, changing locale,
completion and streamed updates preserve disclosure and reading position. Narrow
rows wrap status and argument previews without making the page scroll horizontally.
Tool clocks use animation frames only while the corresponding projection is active.
See [UI events](ui-events.md#tool-call-projections) for the event and persistence contract.

### Tasks and schedules

Tasks keeps Ongoing / Needs attention / Archived groups, search and 20-item
pagination. Rows group by assistant and show title, state and change time. Filters
and search remain outside the scrolling list; search/group changes reset paging.
View Tasks from Machines filters by exact Runtime and execution target.

Schedules share the Tasks destination and provide next run, time zone, policies
and history linking to tasks. Editing uses the editing-page contract; deletion
uses second-click confirmation. Pause/delete prevents future triggers without
cancelling a current run. Tasks and schedules are created through Chat rather
than manual creation forms. See [Task coordination](task-coordination.md) and
[Scheduled tasks](scheduled-tasks.md).

### Settings and first connection

Settings has General / AI Providers / Third-party accounts / Task Configuration tabs and an explicit
configuration location. General exposes Language, Appearance and keyboard shortcuts;
shortcuts start collapsed on narrow windows. Locale/theme startup and live changes
follow [Internationalization](internationalization.md), preserving drafts.

Third-party accounts lists supported services before login. Each service has one
account: Log in opens browser authorization; a connected row shows the username,
status and Log out. There is no account picker, default account or separate
re-login action. Pending authorization shows a user code, browser link and Cancel.
Expired accounts remain visible until logout. See [Integrations](credentials.md).

AI Providers manages named connections, credentials, endpoints, protocols and
concrete default models. One Provider is the Space's new-assistant default.
The first saved Provider becomes the default automatically. Each configured row
retains its brand icon and offers a star to choose the default; the selected row
has a filled blue star and a Default badge. There is no separate default picker.
Changing these defaults affects new form selections, not existing assistants or
Task snapshots. There is no global Main Agent selection. Task Configuration
contains execution capacity and Provider allowances, not model-routing defaults,
Follow Chat switches, independent worker profiles or model-selection allowlists.

### Usage and Machines

Usage offers 7 / 30 / 90 days / year, totals, trends, Agent/model and tool rankings,
and configured allowances. Conversation Usage is scoped to that conversation.
Loading preserves navigation and the chart region; failures retain inline recovery.
Unknown or incomplete data is distinct from zero. Accounting and aggregation
belong in [Token usage](token-usage.md).

Machines groups inventory by Space. Cached offline data remains visible. Machine
settings expose Ask each time / Always allow execution on this machine; owner
policy changes are valid without starting a process on that machine. Viewing a
machine changes neither routing nor authorization. See [Multi-Space UI](multi-space-ui.md).

### Approvals and About

Approval cards identify assistant, machine and exact tool arguments, with
Always allow execution on this machine / Allow once / Deny actions. Instruction
changes offer only Allow once / Deny. See [Permissions](permissions.md).
About Pot is available from General settings; its content follows
[About and licenses](licenses-and-about.md).

### Menu-bar Pot activity and unread indicator

Native App's fixed-width status item pairs the monochrome Companion with a
numeric unread badge: hidden at zero, `9+` above nine. Steam indicates main Chat
or background execution; queued/waiting work alone does not cook. Unread and
execution are independent, and the accessibility label includes exact unread
count and activity/error state. Clicking opens Pot without marking everything
read. Window close leaves background work running; Quit stops the local Runtime.

## Motion

Use restrained state feedback with stable control geometry. Current shared
behavior includes a 180 ms page opacity transition, a 2.7 s executing-task dot
cycle, 210 ms summary removal, menu slide/settle and the 650 ms avatar rebound.
Native menu-bar steam also cycles while executing. Reduce Motion uses static
activity/avatar states and immediate navigation-menu updates. Do not animate
every streamed token or replay completion reactions on refresh. Brand assets and
generation instructions belong in [Pot brand](design/pot-brand/README.md).
