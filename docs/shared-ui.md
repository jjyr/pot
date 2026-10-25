# Shared App and browser UI

The HTML/CSS/JS frontend in [`web/src`](../web/src) implements the main window,
tray panel and browser pages. Wails v3 hosts the macOS windows in the system
WebView; the independent `web-ui-server` serves the same embedded frontend to
browsers. Visual and interaction contracts belong in
[Design language](design-language.md).

```text
web/src: shell, pages, composer, drafts, configuration, navigation
                              | production client
               +--------------+----------------+
               |                               |
         Wails bindings                  same-origin HTTP/SSE
       Go desktop service                 browser server
      local node + Agent Link peers       one local node
```

## Presentation and platform boundary

[`components/ui.js`](../web/src/components/ui.js) owns the shell, assistant
directory, Chat and input lifecycle. [`components/pages.mjs`](../web/src/components/pages.mjs)
owns Tasks, schedules, Topology, Usage, configuration forms and return contexts.
[`components/task-conversation.mjs`](../web/src/components/task-conversation.mjs)
owns the task status, conversation history, menu and fixed composer, reusing the
shared reply/tool/composer components. Pages retains Task navigation, editor drafts and reading intent; domain repositories own shared requests and owner scope.
[`components/usage.mjs`](../web/src/components/usage.mjs) owns exact-count usage tooltips, the trend chart and independently loaded detail lists.
[`components/reply.mjs`](../web/src/components/reply.mjs) owns reply content,
activity and the single failure presentation; [`state/chat-replies.mjs`](../web/src/state/chat-replies.mjs)
binds optimistic and durable reply identities without changing execution state.
Reusable controls, Markdown, completion, resource and About components live beside
those files. Portable conversation/editor/ownership state lives in `web/src/state`.
The shared components use a client interface; they do not call Wails directly.

[`styles.css`](../web/src/styles.css) owns semantic colors, system typography,
breakpoints and control geometry. All hosts use the same assets and layout.
Width controls navigation. Touch density applies below 720 px or with a coarse
pointer; the tray keeps its explicit compact layout.
Default assistant avatars use the [approved kitchen characters](design/pot-brand/README.md#assistant-characters).
`pkg/assistantavatar` composites the assigned artwork over one of eight soft pastel
backgrounds as an opaque 128px PNG. The creation request chooses the background;
the saved character remains stable through renames, uploads and resets. Creation persists the PNG in the assistant files alongside its profile, using the
same file for default images and custom uploads. The initial database baseline
does not adopt old development avatar records; see
[Database migrations](database-migrations.md). The settings editor requests the original
image from `/api/v1/assistants/default-avatar?assistant_id=...` for reset previews.
Directory, chat title, settings and tray render the stored PNG through one image
component. Unloaded identities use a neutral placeholder.

## Markdown diagrams

[`components/markdown.mjs`](../web/src/components/markdown.mjs) renders closed
`mermaid` fences through [`components/mermaid.mjs`](../web/src/components/mermaid.mjs)
in Chat, Task details and Markdown tool results. Other fences and incomplete
streamed fences remain code. The component loads the pinned, bundled Pele ESM
module only when a diagram enters the document; no CDN is needed.

Each `pot-mermaid` element owns its Pele mount and destroys it on disconnection,
including when a message is recycled or its diagram source changes. Appended
reply text reparses only the final Markdown block; completed paragraphs, code,
tables, image previews and diagrams retain their nodes and local state. A late module load
cannot mount a detached or superseded element. Container resizing and pending
font loading trigger Pele's layout refresh. Semantic CSS variables keep diagrams
in the current light/dark theme, and shrinking diagrams support zoom and pan.
Source is available in a disclosure and opens automatically on failure; errors
stay within that diagram. Only closed fences are drawn, and rendering is bounded
to 20,000 source characters, 1,000,000 SVG characters and 500 flowchart edges.
Diagram links and images are disabled, and raw Markdown HTML remains literal.
Pele uses its own layouts rather than Mermaid's exact appearance.

## Generated image previews

Typed assistant resource replies and uploaded attachment previews use the shared
`ResourceView` component. Media playback, isolated HTML and explicit download
behavior are defined in [Display resources](display-resources.md).

The shared Markdown renderer displays owner-bound `pot-file://TASK_ID/PATH`
images inline in Chat, Task conversations and Markdown tool results. Both image
syntax (`![description](pot-file://...)`) and ordinary artifact links ending in
`.png`, `.jpg`, `.jpeg`, `.webp` or `.gif` show a preview, with enlarge and original
download controls. Other file links retain their download behavior. Remote HTTP
images remain explicit links and are never fetched automatically.

The production client reads the existing authenticated Task file chunks through
HTTP or the native bridge, retaining the original node and assistant for every
chunk. It validates offsets, total size and revision before creating a Blob URL.
Previews accept PNG, JPEG, WebP and GIF signatures, up to 16 MiB per image;
unsupported, oversized and unavailable originals show a localized download
fallback. No new file routes or model prompt instructions are needed.

Connected `pot-file-image` elements share pending reads for the same node, assistant,
Task and path. Disconnection releases the lease, aborts unused reads and revokes
unused Blob URLs; a synchronous streaming replacement reuses its lease. Preview
dialogs close when their message leaves the document. Loading, error and dialog
controls follow the current UI language, including during asynchronous reads.

## Business repositories

`state/domains/index.mjs` supplies one Chat, directory, Tasks, schedules and Providers
repository per client. Production and fixtures consume these same interfaces.
A domain query exposes `watch(listener) -> unsubscribe`, a committed `data`
value, `status`, `freshness`, `error`, `lastConfirmed` and derived `fresh`.
Convenience methods such as `tasks.watchDetail(task, scope, listener)` and
`providers.watchCatalog(scope, listener)` select the shared query. They own the
first database read, background confirmation, pagination, invalidation dependencies
and subsequent notifications. Unsubscribing one view does not cancel another
view's IO. Unused completed query entries are bounded to 128; active queries and
in-flight reads remain leased until their consumers finish.

```text
shell / page / background strip
              | watch + typed business command
    Chat | directory | Tasks | schedules | Providers
              | shared queries + command attempts
         resource / transcript repository
              | transaction commit
             IndexedDB
```

The directory combines serving Spaces, local and linked assistants and machine
labels, removing execution and approval authority from retained identities. Tasks
shares catalogue reads, metadata, guidance and the transcript reducer with the
Chat background strip. Schedules owns collection pagination and preview/save/run
commands. Providers owns the shared catalog, routing and allowance settings,
connection DTOs and quota monitor commands. Provider catalogs normalize away
assistant/editor context; sensitive editor revision reads remain transient.

Chat opens a scoped surface session through `chat.open(scope, {surface, context})`.
`query` restores committed content and saved view hints; `watch` owns transport
and same-client/cross-tab commit notifications; `refresh` commits an explicit
snapshot read. `history` chooses indexed coverage or shared HTTP and returns a
committed window for pagination and reset restoration. `saveView` stores reading,
layout and disclosure hints against that window. The domain privately owns cache
construction, persistent identities and IO. UI context supplies the controller's
anchor/position, and a history validity guard only fences superseded navigation.
The UI never provides an IO callback or receives a cache/key. Each surface has its
own reading intent. Closing one leaves shared history IO available to another;
retire, scope change and deletion fence late publication and cache resurrection.

UI components keep focus, unsaved values, expanded disclosures, filters and the
history sentinel. They pass business inputs to typed commands instead of building
domain URLs or repeating mutation/refresh sequences. Command attempts share
pending state and immutable input/request identity across consumers; explicit
retry retains that identity. The production adapter commits accepted response
receipts and invalidations before notifying domain queries. A manual retry after
a local commit failure retries that accepted receipt without sending the command
again. Provider monitor revisions and exact-intent recovery retain their existing
compare-and-swap behavior.

Permissions, pairing/authentication, binary resources,
Topology and detailed Usage retain their specialized interfaces. These combine
live authority, transport lifecycle or route-specific aggregation that is outside
the migrated display domains; they still use the shared storage boundary where
applicable. They must not create a second directory, Task or Provider cache.

## Committed local projections

`state/resource-cache.mjs` routes allowed display GETs from both `client.read` and
`client.request` through `state/projection-store.mjs`. HTTP/native responses and
successful display mutations commit to IndexedDB before returning query values.
Domain invalidations persist epoch counters; a response fetched against an older
epoch must revalidate before it can publish. Memory holds only committed query
copies inside the repository. Production never falls back to a volatile store
when IndexedDB fails.

```text
HTTP / native / SSE / successful mutation
                  |
          repository + reducer
                  |
      IndexedDB transaction complete
                  |
       query projection + notification
                  |
              DOM views
```

Keys include the local node, boot, presentation scope, assistant and full query.
Provider summaries share one local entry across assistant contexts. Credential
bytes and authorization URLs/codes are excluded from persisted provider,
integration and Agent Link display projections. Authentication, pairing, live
permissions, editor revision reads, binary resources and conversation transport
keep their specialized interfaces. The machine editor stores only its derived
policy summary; a failed live read exposes that summary as read-only and never
stores pending approvals or restores authorization. See [UI events](ui-events.md) for transcript
commit boundaries.

A returning page first queries its committed local data and refreshes in the
background. Queries carry `lastConfirmed` and fresh/stale/retained state. Dynamic
lists remain fresh for 5 seconds, configuration for 30 seconds; freshness checks
on access create no polling. A retained directory cannot restore execution,
approval counts or operation capabilities. Task and schedule controls wait for
current authoritative metadata; approvals always read the server. Provider
selection and creation flows explicitly wait for current configuration revisions.
The server remains the business and permission authority for every command.

Projections retain at most 256 entries and 64 MiB, with a seven-day expiry;
command response receipts expire after one day and share those limits. Writes
evict older confirmed projections when needed. Oversized values and transaction
or quota failures retain the previous committed value and show a localized retry
notice. A failed invalidation remains pending until it can commit. There is no
memory fallback, offline command queue or automatic side-effect replay. If the
server accepted a command but its local receipt failed to commit, a manual retry
in the same client retries only the local commit using the accepted response.
Reload discards that in-memory recovery receipt; existing server request identity
and uncertain-result rules still apply.

Task lists read the local operator catalogue; assistant count does not multiply
reads. Task transcripts use `state/task-repository.mjs`: event cursor, input
bookmarks and active-tool checkpoint commit with their changed message records.
The current projection holds at most 640 messages within the shared 16 MiB
window budget. Each Task retains up to 2048 records / 32 MiB on disk, with 64 MiB
across Tasks. Message text, tool arguments/result and combined output streams are
bounded to 1 MiB of UTF-8 content each; truncated cached output is labeled.
Task result metadata and retained guidance also have bounded display projections;
guidance keeps at most 256 entries within 16 MiB, with the same truncation notice.
The server retains the original output. A visible upper history boundary automatically
queries an indexed page of 80 records, including when the initial window is
shorter than the viewport. Prepending preserves the visible message anchor and
pixel offset. One page may be in flight; unchanged cursors do not retry. Failures
show a local Retry action and await explicit input. Latest returns to the committed
current window and cancels pending historical reads. New live output preserves a
reader's historical window. Navigation, disposal and a new run fence late results.

If a historical range was evicted, the existing after-only event API requires the
first reconstruction to start at cursor zero and stop before the requested
boundary. Its working window remains bounded to 640 messages / 16 MiB. Processed
ranges commit to the historical cache, so neighboring pages reuse that coverage
while retained. Eviction removes only the affected coverage rather than discarding
all ranges. Coverage metadata retains at most 128 intervals; discarded coverage
requires a conservative backfill. Backfill never replaces the live cursor/checkpoint or replays commands.
A new run has its own conversation boundary.

Task and usage page offsets, reading positions and disclosures remain view state.
The Chat background-task strip consumes committed query updates and holds only
its current view projection; restored states omit running animation and clocks
until confirmed. Background refresh preserves keyed rows, focus and editor sessions. Dirty forms
keep their leave protection and have no cross-tab draft persistence. Cross-window
notifications carry commit identities and invalidation domains; recipients query
the database instead of consuming broadcast response bodies. Browser logout
retires existing cache writers and clears business projections and transcripts,
while retaining authentication storage, preferences and notification claims.

## Native editing and ownership

All hosts use DOM textareas and inputs. The WebView/browser owns shaping, caret,
selection, undo and IME; the shell owns the composer frame, send/stop actions,
drafts and reference completion. Stream updates retain the live editor node and
do not rewrite its value. Only explicit restoration, matching send receipts and
completion replace text. Composition events, `isComposing` and key code 229 keep
candidate confirmation from choosing a completion or sending a message.

Assistant references use compact `@name` text in the native textarea and an
inert, scroll-synchronized highlight layer. Identity ranges serialize to the same
full-address links used in messages and drafts; ordinary typed names never resolve
to identities. Completion, restored drafts and pasted links share this display.
Editing or deleting a reference replaces the whole label, while copying a complete
reference preserves its address. Chat and Task guidance use the same editor.

Phone and tablet browsers leave Enter, including modified Enter and Enter with
reference completions open, to native textarea editing and IME. Ordinary Enter
inserts a line break; sending requires activation of the send button. Their
composers advertise `enterkeyhint="enter"` and ignore form submission events.
Desktop Enter/Shift+Enter behavior is unchanged. Input policy uses the existing desktop/browser host identity, UA Client Hints' mobile flag,
mobile/Android/iOS user agents, and iPadOS's MacIntel plus multiple touch points
identity. It does not use viewport width or pointer/hover media queries, so
resizing a desktop window or using a touch laptop does not change Enter behavior.
Browsers that conceal all mobile identity fall back to desktop behavior.

The shared Chat composer accepts image/PDF files through DOM selection, drop and
screenshot paste. It uses the same bounded byte upload API in browsers and the
macOS WebView; native attachment downloads use the host save dialog. See
[Chat attachments](chat-attachments.md) for formats, capability checks and retention.

Drafts, cached conversations, positions and late-response fences include the
local node and assistant identity. A matching authoritative receipt clears
only its submitted draft. Selecting another assistant does not change ownership
of a request already in flight. Task Usage uses the Task ID for accounting while
requests retain the owning assistant; its Back/history path preserves the
independently selected Chat. Task approvals follow the Task owner and exact Task,
including when a different assistant remains selected in Chat. See [Node UI](node-ui.md).

Pending approvals appear in one transient region immediately after the latest
assistant message. New messages move this region to the new anchor, preserving
expanded cards, focus and decision errors. Without an assistant message it sits
at the end of the conversation. It scrolls with Chat and never opens a modal.
New request IDs trigger one accessible notice; unchanged invalidations do not
repeat it. This projection comes from pending permissions, creates no conversation
message or model request, and does not change reply/unread counts. Reload restores
unresolved cards from the authoritative pending requests.

Chat and Task details use the same expandable
[`approval-card.mjs`](../web/src/components/approval-card.mjs) component, including
Task name, machine, exact operation, explanation, decision controls and errors.
Cards start collapsed and expand in place. A successful Allow/Deny removes the
resolved card in both views; the last decision removes the notice and assistant
badge. Failed submissions keep the operation and retry controls. Decisions retain
the original node, assistant, Task/run and exact request identity.

The assistant catalogue exposes `pending_approvals` independently of replies.
Permission invalidations refresh the directory even when another Chat is selected;
waiting assistants show the count in their row. Selecting one locates its approval
region. Task lists, details and the background strip show Waiting for approval,
and the strip puts these Tasks first. Chat tool rows retain their execution state
while the dedicated region owns approval controls; Task controls attach only to
the exact Task call/turn, with a shared standalone card if no row is available.

Task details retain their entry route in navigation history. Opening a Task from
Chat makes Back return to that assistant's Chat with its draft and reading position;
opening from Tasks makes Back return to the list. Nested Task Usage and browser
Back/Forward preserve this origin.

[`adapters/production.mjs`](../web/src/adapters/production.mjs) captures the owner
and local presentation scope before every request. Desktop calls Go through Wails
and receives
`pot:snapshot`, `pot:subscription`, `pot:changed` and `pot:read` events. Browser
uses same-origin fetch and EventSource. The Go desktop service owns window/tray
lifecycle, the native file save dialog and desktop viewing. Browser adapters own
authentication, installation, notifications and browser downloads. App window
close hides the window while the local Runtime continues; Quit stops it.

The App does not publish or configure the browser endpoint. Browser access belongs
to the standalone server and its operator controls; see [Deployment](deployment.md).

## Configuration and asynchronous actions

Shared editor sessions capture their caller, owner, Runtime boot identity,
revision and request identity. Back/Cancel and navigation use the same dirty-exit
guard. Pending saves block leaving while their result is handled. Browser history
contains only route/session markers, never credentials or form values. Reload
recovery is outside this memory-only editor contract.

Nested Provider setup suspends the actual assistant form, including draft fields,
expanded disclosures, focus and scroll. Save refreshes Provider choices and
restores that form. Cancel returns through the same guard. Global navigation
also checks suspended dirty editors. Source focus is restored only on wide
windows, avoiding automatic keyboard activation on phones.

Assistant model selection and nullable parameter overrides belong to that
assistant. Saving them does not mutate the shared Provider. Providers retain their
own concrete default and configured model IDs; API model checking does not save
configuration or send a model message. The backend validates revisions and applies
pending assistant configuration at a fresh input boundary.

A failed save retains its draft and request identity. Partially acknowledged form
changes are recorded so retry does not repeat completed writes. Runtime identity
changes reject an old session. Authoritative responses refresh live data without
replacing another owner's draft. While a Main turn is running, a nonempty composer sends guidance to that exact
turn; an empty composer shows Stop. Guidance retries retain their original turn
identity even if the turn ends before the receipt arrives. The conversation menu
exposes Pause/Resume; pausing takes effect at a safe model boundary and preserves
in-flight work. Task Configuration edits Provider allowances with revision CAS,
independently of concurrency and assistant model selection.

Inline machine permission selections retain a separate draft during background
updates. A dirty selection keeps its original policy revision for compare-and-swap
saves. An external change exposes a reload action; only that explicit reload or a
successful save adopts the latest policy. Refreshing status never silently rebases
an unsaved choice onto a different revision.

Conversation and Task stream contracts are in [UI events](ui-events.md).

Chat and Task transcripts project consecutive completed Task `send_message` and
`receive_message` records through `components/communication-window.mjs` before
virtualization. Two or more eligible siblings share a native disclosure; its
summary retains each distinct direction and Task identity, and separately counts
records, messages and Tasks. A receive batch remains one record with its original
message order. Prose, other tools, mixed assistant mail, incomplete calls,
approvals and failures break the stack. Individual calls and failure labels stay
visible independently. Expanding a stack shows every original message body, with
call details available in a second disclosure. This is a UI projection only;
model history, correspondence, transport receipts and Task lifecycle are unchanged.

The adapter retains group keys by membership and original record nodes across
append, prepend and splits, giving the existing reading cursor and virtualizer
stable member targets. Disclosures are scoped to the conversation and survive
virtual recycling. `state/task-identity.mjs` is shared with Task views: complete
`node_id / kind / agent_id` addresses identify Tasks. The shared `taskShortID`
helper displays `#` followed by the first four
characters of the real `address.agent_id` (or the entire ID when shorter).
Equal prefixes deliberately share the same short label, including equal IDs on
different nodes. Full addresses, catalogue scope and owner still determine
identity and navigation; labels never route requests. Only a confirmed TaskInfo
address mapping permits icon color. Linked
catalogue IDs are never treated as remote address IDs. UTF-8 FNV-1a32 selects one
of the six fixed families in `state/task-palette.json`; only the small Task mark
uses the same family across light, dark and system themes. Missing mappings use
neutral marks, and short labels remain visible when names are long.

`components/task-identity.mjs` renders the original `icon('tasks')` inside a
22 px tinted container and a neutral monospace short label. Communication
endpoints, expanded records and background rows use the same mark and
authoritative address mapping. The small tinted background has no colored
outline; task names and labels stay neutral. Runtime activity and semantic
status remain separate from the fixed identity color. A confirmed endpoint and
its background row open the same existing Task detail with the catalogue ID;
unknown addresses retain a neutral mark without inventing a navigation target.
Each background row keeps the icon, neutral monospace short ID, ellipsized title
and right-aligned activity/status on one line, including narrow layouts. IDs align
directly after the icons, with a slightly larger gap before the titles.
Communication endpoints use the same icon, ID and title order.
Source and elapsed time remain in the accessible description and Task details.
The original three-row preview and
expand/collapse control retain task ordering and counts without a second summary.
Keyed projections retain live controls through title, status, locale and
short-label updates.

## Read state and activity

Completed replies have a durable reply ID/count. Initial catalogue history becomes
the baseline rather than unread mail. A new live reply can rebound its avatar
once; reconnect catch-up and duplicate updates do not replay it. Native read
baselines are held by the Go attention service and shared between windows through
`pot:read`. Go accepts a read only for the observed latest reply in a visible,
focused Chat. Browsers keep baselines in local storage and synchronize same-origin
tabs. Keys include the local node identity so a rebuilt database cannot inherit an
unrelated baseline. Reduced Motion disables reactions and native tray animation.

Each sidebar assistant name has a monochrome cooking Pot while its main turn or
one of its background Tasks is executing. The compact assistant catalogue's
`executing` projection combines those sources without loading message histories;
queued, allowance-waiting, approval-waiting Tasks and paused work do not cook.
Task, local executor and Agent Link invalidations refresh that catalogue,
including assistants whose Chat is not selected. Offline cached entries never animate work.
Execution takes priority over older unread replies. When execution stops, an
unread reply shows the settled lid, a success check and occasional gentle steam;
the existing reply read receipt removes the Pot. Counts and approval controls
remain independent. Reduced Motion keeps distinct static states, and a hidden
document pauses these animations. Refreshing projections retains the indicator's
DOM and does not replay completion motion.

## System notifications

General settings has one **System notifications** preference, enabled by default.
This expresses delivery intent; it does not grant browser or macOS permission.
The setting shows the current permission, an explicit authorization action while
permission is undetermined, and instructions for browser/system settings after a
denial. After the authenticated Web main interface is ready, an eligible desktop
browser with undetermined permission sees a non-modal introduction. Only its
**Allow notifications** click requests browser permission, within that click's
user activation. **Not now**, closing the browser prompt, errors, and page reloads
do not repeat the introduction. A permanent origin/profile IndexedDB transaction
claims the introduction before display across tabs and local node identities; if durable atomic
storage is unavailable, discovery is skipped and settings remains available.
This record is independent of delivery intent and OS permission.

In the macOS App, the main interface's first successful directory load and paint
signals readiness to the shared Go service. If Pot notifications are enabled and
native authorization is undetermined, it requests permission automatically. Tray
initialization cannot trigger it. Automatic and manual requests share one in-flight
coordinator; a durable attempted marker prevents automatic retries after errors
or unknown native status. Settings still supports explicit retry. Already granted,
denied, or disabled clients never automatically request permission.
An insecure or unsupported browser reports its actual limitation. Pot does not
play audio: sound, banners and Do Not Disturb follow browser/macOS settings.

Browsers use the Notification API in a secure context (HTTPS or localhost).
Windows and Linux are supported through the Web UI. Mobile browsers do not get
this introduction or a misleading authorization button: the current constructor
send path does not implement Android Service Worker delivery or verified iOS Home
Screen delivery. Settings explains the specific environment limitation.
Browser delivery checks permission and the local switch before opening the
notification deduplication database. Unsupported mobile clients do not access
that store. Storage failures preserve request diagnostics, always reject with an
error, and remain handled by the notification coordinator rather than rejecting
the assistant directory refresh.
The macOS App uses Wails'
UserNotifications service from a bundled, signed App, with the official
`UNNotificationSettings.authorizationStatus` for permission presentation. Web and
App preferences are independent: the browser stores its preference in local
storage for the current origin/profile/device; App stores it under its private
root in `system-notifications.json`. Neither preference is an account setting.
Browser startup makes a best-effort local cancellation of any previous Web Push
subscription without contacting a server endpoint or delaying system notifications.
The PWA Service Worker retains asset caching only; its updated cache version
activates immediately and takes over existing pages, with no push or notification
click handlers.

Notifications observe owner-scoped projections on domain invalidations:

- A changed assistant catalogue `reply_id` with an increased `reply_count`
  identifies a completed assistant reply. The notification body uses that reply's
  catalogue preview for both local and linked assistants. Whitespace is collapsed
  and previews are limited to 120 Unicode code points, with an ellipsis when
  notification formatting truncates them. Empty previews use a localized reply
  completion message. Token updates do not notify.
- Task `complete` notifies success and `interrupted` notifies interruption.
  A `failed` transition first enters assistant recovery; unresolved failures
  notify through the assistant reply rather than an immediate Task alert.
  Other phases, including explicit cancellation, do not notify. Task identity
  and a durable outcome sequence distinguish subsequent terminal transitions.
  The compact [Task outcome API](task-coordination.md#client-notification-outcomes)
  reads only changes after a client cursor. `task-outcomes` invalidations wake
  this reader; token/output updates do not query notification state.
- New pending permission request IDs notify approval. Tool arguments are never
  included. Task and approval notifications contain a bounded name and a short
  localized event description.

Each assistant has separate Task-outcome and approval refresh queues. A failed
read retains that reader's last successful cursor or baseline; it cannot reset
another assistant, erase a reply baseline, or block approvals behind Task reads.
Approval responses include an optional `task_title` for the notification name.

Initial reads, the first read after disconnection, and Runtime boot replacements
establish baselines. An in-flight read from a previous connection cannot establish
that baseline. Notifications use explicit node, assistant and optional
Task identity. Clicks activate the browser tab or App main window and open that
exact Chat/Task; a missing destination reports an error. App retains a click until
its main WebView and directory are ready.

A visible, focused corresponding Chat suppresses its assistant, Task and approval
notifications. An exact Task detail suppresses that Task's events. Another Chat,
a background window or a minimized window permits delivery. Browser checks both
page visibility and window focus; same-origin tabs ask each other whether the
target is being viewed. App additionally checks actual Wails window visibility
and focus, coordinating the main and tray windows.

IndexedDB read/write transactions claim browser events once across same-origin
tabs; App uses its Go store to claim events once across native windows. Claims
remain for seven days, including suppressed events, so re-enabling or leaving a
Chat does not replay those events. These scopes do not coordinate App with Web,
different browser profiles/origins, or different machines. Browser sessions
and native Runtime identities provide no shared device identity; this
feature adds no registration or coordination service to infer one.

Delivery requires a running client and its live connection. A closed, discarded
or suspended browser page does not receive new notifications through this path;
Pot no longer provides Web Push offline notifications, subscription APIs, VAPID
configuration or a server delivery worker. The macOS
App keeps its WebViews and Runtime resident when its window is hidden; Quit ends
notification observation. Actual display remains subject to OS/browser policies.

### Removing legacy Web Push deployments

Updated servers never open or read the retired `browser-push.sqlite` subscription
store, load VAPID keys, poll the old assistant notification feed or send to saved
subscriptions, including installations that have not reopened Pot. Existing
subscription files and deployment secrets are inert and are not deleted by this
source change. The completed-reply records used by the assistant catalogue remain
local state for sidebar updates and system notification event detection.

Deploy and restart each browser service to stop its old worker. Source changes do
not hot-update an already running old deployment or an already loaded old page.
Online browsers adopt the replacement Service Worker on update; until then an old
worker may still handle an already queued push. The replacement worker has no
push handler, and local cancellation needs no new notification permission.

## Browser crash reporting

[`web/src/errors.js`](../web/src/errors.js) loads before the application module.
Uncaught JS errors and unhandled rejections produce a non-modal DOM toast;
handled request failures retain their page presentation. The toast offers manual
refresh and diagnostic copying, hides after eight seconds unless hovered,
focused or expanded, and merges repeats without resetting the timer.

Browser ResizeObserver loop notifications without an exception are excluded from
the crash toast and log: they indicate deferred resize delivery. Browser
diagnostics remain available; thrown exceptions and unhandled rejections still
report normally.

Up to 20 bounded records remain in memory and same-tab session storage. URL
queries/fragments and common credential patterns are redacted. Diagnostics do
not collect request bodies, fields or transcripts, and are never automatically
uploaded. Storage/clipboard failure does not interrupt reporting. A blocked JS
main thread cannot run exception handlers.
