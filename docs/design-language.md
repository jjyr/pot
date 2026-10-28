# Pot design language

This is the shared App/Web reference for visual language, responsive navigation
and page interactions. Code defines current behavior. Platform responsibilities
belong in [Shared UI](shared-ui.md), node and assistant identity in [Node UI](node-ui.md),
and build and regression commands in [Development](development.md#checks).

## Visual language

Use warm paper surfaces, charcoal text, system typography and fine gray-green
dividers. Terracotta identifies assistant mentions and navigation selection. Blue
continues to identify actions, links and focus; status colors accompany text or icons.
Assistant replies use transparent natural prose with a small avatar and assistant name.
User messages align right in a single warm neutral bubble without a name row. Reserve
cards for related controls or details, rather than wrapping every row.

Tasks and Schedules use transparent assistant sections with 24 px between groups.
Headings and records share a 4 px horizontal inset inside the page padding;
headings use 8 px vertical padding and records use 12 px. Records size to their
content with a 44 px minimum target. Only adjacent records have a divider; the
heading and final record do not add enclosing lines. Keep keyboard focus visible
inside the scrollable list, and preserve full identity disclosures in headings.

### Color and surfaces

[`styles.css`](../web/src/styles.css) installs the shared semantic tokens.
System / Light / Dark appearance is available in General settings.

| Token | Light | Dark |
| --- | --- | --- |
| Canvas | `#F8F6F1` | `#22231F` |
| Surface | `#FFFEFA` | `#2B2D27` |
| Navigation / titlebar | `#F0EDE6` | `#282A24` |
| Assistant pane | `#F0EDE6` | `#2B2D27` |
| Assistant selection | `#F1E5D8` | `#49392D` |
| Primary text | `#282924` | `#F0F0E5` |
| Secondary text | `#62655C` | `#BCC2B1` |
| Divider | `#DEDED4` | `#464B3F` |
| Input border | `#C9CBC0` | `#626859` |
| Identity accent | `#98522F` | `#E2AD82` |
| Communication line | `#869477` | `#9CAE86` |
| Primary action / link | `#0068DA` | `#4D9FFF` |
| Secondary control | `#EEEEE6` | `#464B3F` |
| User message fill (`--message-user-fill`) | `#EDE8DF` | `#34332C` |
| Success | `#247844` | `#78BE8A` |
| Attention | `#885B13` | `#D7AF66` |
| Error / destructive action | `#BD342D` | `#FF6961` |

Assistant prose uses 15 px text with a 1.8 line height, capped at 66ch; narrow
conversation containers use 14 px. User bubbles are capped at 570 px and 88% of
the column (93% in narrow containers), with 14 px corners (12 px when narrow).
Reply failures emphasize the status and diagnostics while retaining normal prose.
Tool headings stay compact at 12 px; communication bodies use 14 px text with a 1.8 line height
(13 px in narrow communication containers).
Task, approval and resource components keep their existing placement and controls.

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
Chat keeps attachment, input and send controls in one compact row by default;
references and attachments expand above it and multiline input grows naturally.
Assistant copy actions sit below the body. Desktop user copy actions appear
beside messages on hover or keyboard focus; touch actions stay visible. Copy success
acknowledges in place for 1.6 seconds, retaining the current language.
Chat and Task composers share compact floating selectors above the input.
`/` lists Skills without icons; `@` lists assistant avatars, names and sources.
Rows are 28 px on desktop and at least 44 px on narrow/touch surfaces. Names and
truncated descriptions share one line, with full details on hover. Assistant
avatars are 20 px. Menus are at most 440 px wide and
their candidates scroll within the available viewport. Selected Skills are
removable chips above the native textarea. Assistant references display as tinted
`@name` labels at their insertion positions in the text, with full node/assistant
identities retained in drafts and sent messages. The compact attachment, selector
and send actions share the bottom row.
Keep copy concise; put detailed operating explanations behind disclosures or in
field help.

### Motion

Shared motion tokens use 110 ms for control feedback, 180 ms for transient panels
and disclosure arrows, and about 220 ms for gesture settling. Edge navigation
follows the finger while opening or closing the menu and returning from Chat or
an editor. Release uses distance and velocity; cancellation and reversal settle
back. Existing editor leave guards still decide whether Back may complete.
Reduced Motion removes transitions, activity animation and gesture settling.
Content updates preserve reading anchors rather than animating the whole history.
Chat and Task history retain native scrolling, inertia and keyboard/scrollbar
interaction. Their thin rounded scrollbar has a quiet thumb that strengthens on
hover, reserves stable layout space and uses system colors in forced-color mode.

## Application shell and navigation

Phones, iPads and computers use the same shell, selected by available window width
rather than device names. The phone reference is iPhone 14 Pro at 393 CSS px;
390 px is the portrait release target floor. Smaller screens are not release
targets. iPad split windows follow the same breakpoints.

| Available width | Layout |
| --- | --- |
| Below 720 px | Full-width assistant directory or conversation; global navigation opens as a full-screen menu |
| From 720 px | 56 px feature rail; Chat adds a collapsible 236 px assistant pane |
| Below 900 px | Compact page spacing and stacked form labels, independently of the navigation layout |

The rail contains Chat (Pot icon), Tasks, Topology, Extensions, Usage and bottom-aligned
Settings. Tasks includes Tasks / Schedules. Other destinations and editors hide
the assistant pane; wide editors retain the rail.

Chat uses the canonical Pot Companion brand logo at 25 px, retaining its colors
across themes. The other destinations use matching 20 px SVG glyphs on a 24 px
canvas, with rounded 1.65 px strokes and quiet surface fills: Tasks a checklist,
Topology connected nodes, Extensions a puzzle piece, Usage a bar chart and
Settings sliders. The wide rail marks the current destination with an inset
surface and a short blue edge marker; selected SVG glyphs also turn blue. The
narrow menu reuses the same logo and glyphs alongside labels.

All three columns extend to the top of the window, without a shared horizontal
titlebar. Wide columns have 66 px
headers: the assistant pane owns its heading, fold control and creation action;
the conversation header uses the conversation canvas and owns the assistant avatar,
name, source marker, slider-shaped settings action and ellipsis menu. Usage stays in the
conversation menu. Narrow windows and the tray retain a 44 px header. Native
window controls and draggable space share these headers; controls avoid the first
80 px occupied by macOS window buttons.

The wide assistant list folds in place, leaving an expand control at the start of
the conversation header. This is window-local presentation state retained across
editor navigation and narrow/wide resizing. Folding preserves the composer,
draft, directory search/scroll and visible conversation anchor; it performs no
backend reads and moves keyboard focus to the corresponding fold/expand control.
Narrow windows continue to use the full-width directory and Back navigation.
Clicking the conversation avatar/name opens assistant information with its source
and available actions; outside clicks and Escape dismiss it. Assistant settings still
open the existing editor with its return context and dirty-exit guard.

### Narrow surfaces

A fresh narrow Web visit opens Assistants; an explicit conversation link opens
that conversation. Selecting an assistant opens full-width Chat. Returning keeps
the directory search and scroll, and each conversation retains its draft, cached
history and reading position.

Browser history records the directory presentation separately from conversation
identity. Returning to a directory entry keeps the current conversation's scope,
session, draft and reading position; its recorded former owner does not select a
conversation. Reloading that entry keeps the directory visible while restoring
the retained conversation from the startup cache. Explicit conversation entries
continue to restore their recorded owner.

Root pages expose a 44 × 44 px menu button. A rightward swipe beginning within
24 px of the left edge also opens the full-screen menu with Assistants, Tasks,
Topology, Extensions, Usage and Settings. A destination, Close, Escape or a leftward swipe
anywhere in the menu dismisses it. The menu follows the finger; a quarter-width
swipe, capped at 100 px, commits, while a short, reversed or cancelled swipe
settles back. Reduced Motion removes the settling animation.

Conversation and editor pages show Back instead of the root menu button. Their
left-edge swipe uses the same Back path, including the dirty-editor guard.
The assistant directory preview uses the shell's bounds throughout the gesture
and settling, including when a small drag also activates the Back button.
Vertical scrolling and horizontal content gestures away from the left edge stay
with the content. Conversation actions are available through the ellipsis menu.
The shell follows the visible viewport as browser chrome or the keyboard changes;
editor input reveal keeps the focused supported input in view.

Web uses `viewport-fit=cover`. The shell reserves the top, right, bottom and left
safe-area insets once, keeping the column headers below the status area and
landscape controls clear of display cutouts. Chat and Task composers use ordinary
inner spacing; they do not reserve the bottom inset again. The full-screen menu,
access form and top-layer dialogs also keep controls within the safe area, with
scrolling available in short viewports. Native windows retain zero web safe insets.
VisualViewport resize and scroll events update the visible frame for keyboard
resizing and panning. Pinch zoom remains browser-owned and does not reflow the shell.
The browser document stays pinned; histories, menus and forms own scrolling and
contain overscroll. Focusing a composer keeps it at the bottom of the visible
frame. Keyboard height/offset changes do not re-measure the focused textarea;
only content changes or available-width changes resize it.

### Parent navigation

Back and ⌘[ follow page hierarchy: schedule → Schedules,
conversation Usage → conversation, allowance editor → overall Usage. Narrow Chat
returns to Assistants; wide Chat hides Back. Root lists, Topology, Usage and
Settings have no parent button. There is no forward button or ⌘] binding.
Editors instead restore their captured caller as described below.

Task Back depends on its entry point. Opening a task from the Tasks list returns
to that list, preserving its filters, pagination and scroll. All other task
entries return to an assistant conversation: Chat restores the originating
conversation; notifications and schedule run history open the task owner's
conversation with its original node and assistant identity. The titlebar button, ⌘[ and narrow edge Back
share this rule. Task Usage returns to the task first and retains its return
destination. Locale changes and browser history restoration retain that destination.

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
A changed local node or Runtime identity produces an error instead of restoring
an old context into the replacement node.

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

The directory and wide pane keep their existing rows: avatar, name, one-line
preview and unread count. Linked assistants add a Codex or remote Pot source icon
and node name in these rows and the conversation title. Source markers remain
visible while disconnected; they do not replace assistant avatars. Long names
truncate with full-text tooltips and unread counts stay at the trailing edge.
Unread reply counts use a solid terracotta badge with canvas-colored text and a
semibold assistant name. The count and name weight supplement color; the badge
stays distinct from selection, activity and approval states in both themes.
Search filters names without changing the selected conversation. Registry order
remains stable; node identity distinguishes duplicate names. Initial history does
not count as unread. Opening the conversation clears its indicator. Offline,
approval, configuration and activity previews reflect their own states.

Default avatars pair each kitchen character with one of eight soft pastel
backgrounds. A stable hash of the creation request selects the background; the
character keeps its own colors. The saved role and image remain unchanged by
theme or rename. Rows, titles and settings share the image. A new completed live reply triggers one 650 ms whole
image scale-and-tilt rebound. Initial history, duplicate updates and reconnect
catch-up do not replay it; hidden pages do not queue reactions.
Custom avatars use the same shape and animation. They display the assistant's
persisted square PNG in the directory, chat title and settings preview; restoring
the default uses the originally assigned kitchen character and pastel background.

First-use Chat and the directory + action share an Add assistant page in the
conversation region, retaining the assistant pane at wide widths. Choices always
appear in this order: Create an assistant with API, Connect another Pot, Connect
Codex. Only the explicit API creation action creates a local assistant, using the
node default or the shared Provider editor. Visiting the page never creates an
assistant. Linked chats do not require a local model connection.

Runtime choices retain their button identity during directory refreshes. Selecting
a runtime shows loading progress and permits one configuration lookup at a time;
language changes preserve that lookup. Leaving the page or retiring its Space,
Control, boot or connection prevents a late lookup from opening a form.
After the current connection is confirmed again, runtime choices recover in
place. Finishing an old lookup releases its pending state without replacing the
current choices' authority.
Configuration steps share one Add assistant history entry. Successful creation
replaces it with the returned assistant's conversation; browser Back and the
narrow conversation Back gesture return to the assistant directory.

Each choice pairs its heading and description with a consistent rounded icon
holder: a blue plug for API creation and the existing Pot/Codex brand assets for
connections. Icons are decorative; the translated heading names each choice.
The API action retains the primary button emphasis in both themes.

Subflows stay in the same region. Back restores the choices; returning to an
existing conversation restores its draft and reading position without stopping
background execution. Narrow layouts use the same content full-width with Back.
Pot connections use URL entry and existing browser login/consent in the same
editing region. Native connections show the updated receiver instructions and
return to the runtime list after explicit receiver registration. Connecting does
not open a default assistant; explicit selection or creation retains the actual
returned identity.

Every newly created assistant starts with a fixed welcome message in the creation
UI's language, introducing its saved name and naturally pointing to settings for
its name, avatar, speaking style and working preferences. The welcome is committed to both the durable transcript
and the model checkpoint, so the first model request includes it as an assistant
message. It makes no inference call, generates no unread notification and is never
reinserted or translated on reopening. Existing conversations are not backfilled.
Assistant names, avatars and rules are files in `ROOT/assistants/ID`. The UI and
ordinary model file tools share these sources. External edits update the sidebar,
chat title and settings; stale UI drafts cannot overwrite changed base content.
Execution and filesystem permissions govern model writes.

The + action opens Add assistant. API creation uses random two-word slug identities and
empty assistant instructions. Remote assistants are configured by their source
node and displayed from its directory.

Every new local Pot assistant, including the first, receives a lowercase two-word
slug such as `amber-otter`. Its default display name title-cases both words, such as `Amber Otter`; an
explicit creation name overrides only that display name. Two independent choices
from the bundled 1,221-word pool provide 1,490,841 combinations. Allocation excludes
all historical IDs, including deleted assistants, and current display names
(case-insensitive, ignoring surrounding whitespace). It chooses uniformly among
the remaining pairs without numeric suffixes. Names are identity data and are not
translated. Renaming changes only the display name; IDs, addresses and directories
remain stable. Existing assistants retain their IDs and names, including `main`.
Name allocation and registration share one transaction across windows; request
retries retain the saved identity, initial welcome and avatar. Default avatars
continue using the kitchen character artwork independently of generated names.
The creation action stays busy until the operation finishes. Before selecting the
new conversation, an authoritative catalogue read replaces any older cached list;
a failed confirmation retries the saved creation rather than registering another.

Assistant settings edit the name, avatar and configuration. Custom avatar uploads
accept PNG, JPEG and WebP up to 8 MiB, center-cropped to a 128 px PNG; restoring the
default retains the original character and background. Edits use content revisions so
stale settings cannot overwrite a newer chat profile change. **Personality and
preferences** edits the assistant's AGENTS.md without requiring users to know that
filename. Existing assistant location is read-only. Provider/model changes are saved on the assistant
without changing the shared Provider or other assistants. Model parameters and
assistant preferences start collapsed.
Nested Provider setup preserves the assistant draft and returns to its caller.
Existing assistant settings place a collapsed **More actions** disclosure above
the Cancel/Save footer. It contains a red **Delete assistant** button. A confirmation
names the assistant and explains that deletion removes it from the directory, stops its schedules
and prevents reopening its conversations. Cancel and Escape preserve the editor
draft. Ongoing replies or Tasks must end before deletion. Successful deletion
returns to Chat; deleting the last assistant offers explicit creation and never
restarts first-use bootstrap. Historical records remain private on disk and
assistant identities and creation receipts are never reused.

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

Conversation bubbles stay within the available column width on every surface.
Long text and links wrap; wide code blocks and tables scroll within their own
containers without making Chat or Task history scroll horizontally.

Task details use the conversation column without an assistant sidebar. A fixed
status row below the titlebar shows the current task state and an approval locator
when needed. The initial goal is a collapsed disclosure with a one-line preview.
Operator guidance uses user bubbles; assistant guidance and peer references use
neutral bubbles with explicit source labels. Execution prose and the final result
use the shared assistant reply bubble, without separate Goal/Execution/Result
chapters or repeated terminal text. Tools and exact approvals remain inline.

The task composer stays at the bottom while history scrolls independently. Its
nonempty input sends guidance; empty input offers Stop. The titlebar ellipsis owns
Pause/Resume, Usage, task details and cancellation. Machine/model identifiers live
in task details rather than the status row. Ended tasks replace the active composer
with a compact outcome caption; any unsent draft remains visible read-only.

Main Chat shows a neutral Background tasks · N region after the current message
window. The region remains outside recycled message rows, preserving task focus
and expansion while long-chat history is virtualized. It previews three queued,
running, allowance-waiting or approval-waiting tasks, with a
full-width Expand remaining tasks row when more are available. Expand and Collapse
operate in place, using Chat's scroll container. They preserve the reading anchor
rather than jumping to the bottom. Expansion is scoped to the local node and
assistant for the mounted window and survives detail/settings navigation. Task
additions and removals retain that choice; an empty region resets it. Task rows
retain identity and order within each approval group across metadata/clock refreshes.
Titles show at most two lines, with status and elapsed time on the next line.
Delegated rows add the Codex or remote Pot source icon and node name without
changing the task row layout.
Approval-waiting rows come first, use an attention color and show Waiting for
approval. Each row shows muted elapsed wall time from the original
receiving node's first execution claim, excluding the initial queue and including later
approval, pause and allowance waits. Durations show seconds below a minute, minutes/seconds
below an hour, hours/minutes below a day and days/hours thereafter. The clock
continues across refresh and reconnect, updates only its text while Chat is visible,
and never requests backend projections. Unknown start times omit the clock.
Task details expose the start timestamp in the viewer's local timezone.
Pending operations appear as collapsed permission cards immediately
after the last assistant message, with a transient notice and an independent
Waiting for your approval count in the assistant directory. Clicking a card
expands the same review component used in Task details. New requests announce
once; unchanged updates retain review state. Resolved cards disappear, and the
last resolution clears the region and directory marker. The region scrolls with
Chat and never becomes a persisted message or ordinary unread reply.
Rows open task details, whose Back action returns to the entry
Chat; queued, allowance-waiting and finished tasks are excluded. The region is presentation state,
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
Recovery reads authoritative state. Connection notices appear beside the assistant
name in the Chat header, keeping history and composer geometry unchanged as the
connection state changes. They are used only when no pending send or active reply
already owns that status. Long notices truncate within the header, retain their
full text on hover and leave recovery actions available.

Task file links download through the local node; App requests a save location
and Web uses browser downloads. Delivery constraints belong in
[Deployment](deployment.md). Native Chat can offer desktop recovery actions for
recognized unsuccessful-task diagnostics; these reuse existing connection/resource
forms without automatically retrying the task.

### Tool calls

Chat and Task details share compact inline tool disclosures. The closed row shows
the operation in regular-weight muted text, a nonredundant argument preview when
useful, explicit status and elapsed time. Routine task coordination uses localized
operation labels and keeps task IDs in expanded details. Ordinary closed rows use compact
spacing and no visible border; consecutive tool rows have reduced spacing.
Active operations also show their resolved machine and workdir.
Collapsed failed and denied calls show a neutral Ended label without error diagnostics
or a failure border. Opening a call reveals its precise failure or denial status,
diagnostic, arguments, ordered output, final results and a secondary raw call/result disclosure.
Waiting for approval, reconnecting and unconfirmed results keep their explicit
statuses. Tool failures alone do not request user attention; task outcomes and
approval or recovery controls communicate impact and required action.
Expanded calls use a semantic content view before the secondary raw disclosure.
Commands share a read-only terminal surface: resolved machine/workdir and shell,
the exact command, retained output and exit/source-truncation receipts. The visual
shell prompt is excluded from copying; process input and controls are labeled as
interactions rather than shell commands. Patches group actual additions/removals
by file, including move destinations. Composition children take precedence over
duplicating the orchestration source; the complete source remains in raw details.

Task reads distinguish the completed read receipt from the returned Task phase.
Their title, phase and latest result use the retained receipt, while Open task
navigates to the current known Task. Schedules, discovery tools
and unknown extensions show labeled fields and lists; unfamiliar field names and
values remain verbatim. Structured JSON in MCP text blocks uses the same readable
view. Plain result text uses safe Markdown, and supported images render inline.
Browser calls preserve their source and actual observations without inferring
page actions from JavaScript. Assistant rules and profile files use the same
command and per-file patch views as other files. Resource registration shows manifest information
and its registration receipt; presentation uses the existing conversation resource
component rather than duplicating the preview inside a tool.

Both Chat and Task use these views. Technical metadata, original arguments,
results and output remain in secondary raw details. Structured displays bound
depth, item count, node count and text; truncation points to raw details, and copy
retains the complete available value. No tool action implicitly repeats an operation.

Ordinary calls default to collapsed. Composition children nest under their original
`exec` call in start order. Exact approval requests open the affected call and its
ancestors, show the full reviewed operation, and provide Allow this operation / Deny.
Task execution groups check pending descendant approvals before mounting their
virtual child rows, so a collapsed group cannot hide a new approval.
A compact locator exposes pending approvals outside the current reading position;
requests without a transcript row retain a standalone approval card. A failed
approval submission keeps its diagnostic and controls available. Long-lived machine
policy remains in Machines.

Consecutive root command and script executions in the same turn share one collapsed
execution history in Chat and Task details. Its stable summary shows Execution
history alongside status and wall time, and omits execution counts and source code. Prose, guidance, other root operations and
turn boundaries split groups. Empty model-round placeholders do not split them.
Each `wait` receipt joins its originating `exec` by turn and `exec_id`, even across
a prose boundary, and its incremental output appears on that execution. Unknown
waits remain independent. Process `write_stdin` receipts similarly join their
originating `exec_command` by turn, machine and `session_id`, including commands
nested inside compositions.
Pure polling adds no row; input, terminal controls and approvals remain inspectable
under the command. Unknown sessions stay independent. Ordinary summaries and
details omit session IDs; raw details and exact approval arguments retain them. Command output combines retained chunks in receipt order without
duplicating the same receipt's streamed and final output. A live process session
shows Running, and an unresolved session after its turn ends is unconfirmed.
Original calls and receipts stay in raw details.
A yielded script stays active until its final receipt; an unresolved receipt after
the turn ends is unconfirmed. Group duration spans its earliest execution through
its latest finish rather than summing parallel durations. Group summaries show
progress and a neutral Ended label for completed, failed or denied execution
histories, without repeating child diagnostics or highlighting child failures.
Original failure phases and receipts remain intact; expanded individual calls
expose diagnostics, and a script diagnostic can reveal its affected child.
Expanded history and output reading positions
survive updates, and pending Task approvals open their group ancestors.

Live output has bounded height and follows only while its reader stays near the
bottom; Jump to latest resumes following. Expanding, copying, changing locale,
completion and streamed updates preserve disclosure and reading position. Narrow
rows wrap status and argument previews without making the page scroll horizontally.
Tool clocks use animation frames only while the corresponding projection is active.
See [UI events](ui-events.md#tool-call-projections) for the event and persistence contract.

### Tasks and schedules

Tasks keeps Ongoing / Needs attention / Archived groups, search and 20-item
pagination. Rows group by assistant and show title, state and execution context.
Ongoing rows with a known first execution start also show muted elapsed time,
using the same wall-time rules and formatter as Chat. Desktop aligns time to the
right of the title, before the chevron; below 720 px it sits beside the status
information. Never-started and ended tasks omit the live clock. Only visible
lists advance their clocks; navigation and disposal release the presentation
timer without requesting backend data. Filters and search remain outside the
scrolling list; search/group changes reset paging.

Schedules share the Tasks destination and provide next run, time zone, policies
and history linking to tasks. Editing uses the editing-page contract; deletion
uses second-click confirmation. Pause/delete prevents future triggers without
cancelling a current run. Tasks and schedules are created through Chat rather
than manual creation forms. See [Task coordination](task-coordination.md) and
[Scheduled tasks](scheduled-tasks.md).

### Settings and first connection

Settings has General / AI Providers / Third-party accounts / Task Configuration
tabs for the local node. General exposes Language, Appearance and keyboard shortcuts;
shortcuts start collapsed on narrow windows. Locale/theme startup and live changes
follow [Internationalization](internationalization.md), preserving drafts.

Third-party accounts lists supported services before login. Each service has one
account: Log in opens browser authorization; a connected row shows the username,
status and Log out. There is no account picker, default account or separate
re-login action. Pending authorization shows a user code, browser link and Cancel.
Expired accounts remain visible until logout. See [Integrations](credentials.md).

AI Providers manages named connections, credentials, endpoints, protocols and
concrete default models. One Provider is the local new-assistant default.
The first saved Provider becomes the default automatically. Each configured row
retains its brand icon and offers a star to choose the default; the selected row
has a filled blue star and a Default badge. There is no separate default picker.
Changing these defaults affects new form selections, not existing assistants or
Task snapshots. There is no global Main Agent selection. Each assistant's settings
offer an optional default Task Provider and editable model picker; Follow assistant
leaves this preference unset. Explicit Task routing overrides this preference.
Task Configuration contains execution capacity and Provider allowances; model
defaults belong to the individual assistant.

### Usage and Topology

Usage offers 7 / 30 / 90 days / year, totals, trends, Agent/model and tool rankings,
and configured allowances. Conversation Usage is scoped to that conversation.
Loading preserves navigation and the chart region; failures retain inline recovery.
Unknown or incomplete data is distinct from zero. Accounting and aggregation
belong in [Token usage](token-usage.md).

Topology retains its graph, ownership regions, edges, top connection toolbar and
selection inspector. Wide windows put details on the right; narrow windows place
them below the graph. Pot and Codex use their brand marks, machines use device
glyphs, and full identities and operations appear on selection. Hover never changes
selection. Keep the existing machine-policy editor and its dirty-exit protection.

MCP connections show disconnect/reconnect, reauthorization and revocation.
Legacy connections are read-only, labeled unsupported with update/reconnect
instructions and their last confirmed state. There is no invitation picker.
Accepted work and saved history remain visible after disconnect or revocation.

Linked chat, task delegation and notifications use the existing surfaces, with
source markers identifying the remote node. No separate binding wizard, task form
or notification center is added. Each node owns its execution settings and
permissions. See [Node UI](node-ui.md) and [Agent Link](agent-links.md) for connection
and lifecycle contracts.

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

Sidebar name-line activity uses a 24px monochrome Pot: a 1.5s rocking cycle,
360ms lid rattle and staggered 1.45s steam while executing. Completed unread
replies keep a settled tilted lid, success check and gentle 3.6s steam until read.
The indicator stays inside stable row geometry; long names truncate before it.
Reduce Motion replaces both loops with distinct static poses.
