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
      local + remote Spaces               one serving Control
```

## Presentation and platform boundary

[`components/ui.js`](../web/src/components/ui.js) owns the shell, assistant
directory, Chat and input lifecycle. [`components/pages.mjs`](../web/src/components/pages.mjs)
owns Tasks, schedules, Machines, Usage, configuration forms and return contexts.
[`components/task-conversation.mjs`](../web/src/components/task-conversation.mjs)
owns the task status, conversation history, menu and fixed composer, reusing the
shared reply/tool/composer components. Pages retains task requests and owner scope.
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
The canonical Companion assets and deterministic avatar colors are shared.

## Markdown diagrams

[`components/markdown.mjs`](../web/src/components/markdown.mjs) renders closed
`mermaid` fences through [`components/mermaid.mjs`](../web/src/components/mermaid.mjs)
in Chat, Task details and Markdown tool results. Other fences and incomplete
streamed fences remain code. The component loads the pinned, bundled Pele ESM
module only when a diagram enters the document; no CDN is needed.

Each `pot-mermaid` element owns its Pele mount and destroys it on disconnection,
including when a streaming message replaces its content. A late module load
cannot mount a detached or superseded element. Container resizing and pending
font loading trigger Pele's layout refresh. Semantic CSS variables keep diagrams
in the current light/dark theme, and shrinking diagrams support zoom and pan.
Source is available in a disclosure and opens automatically on failure; errors
stay within that diagram. Only closed fences are drawn, and rendering is bounded
to 20,000 source characters, 1,000,000 SVG characters and 500 flowchart edges.
Diagram links and images are disabled, and raw Markdown HTML remains literal.
Pele uses its own layouts rather than Mermaid's exact appearance.

## Cached resource reads

`state/resource-cache.mjs` provides memory-only reads shared by Web and Wails.
The client explicitly allows display resources: configuration summaries, directory,
Tasks and schedules, usage pages, machines and membership status, About, and
reference completion. A resource key includes Control, boot, Space, owner and the
full query. Provider summaries share one Space entry across assistant contexts.
Credentials, editor revision reads, approval decisions and permission reads bypass
this cache. Membership status contains only identity, role, Control URL and epoch;
invitation creation remains an uncached command.

```text
page / directory / completion
            |
     client.read (allowlist)
            |
     scoped memory cache ---- cached value ---> render immediately
            |
     stale / invalidated ---- one shared read --> notify active views
            |
     client.request --> HTTP or Wails
```

Fresh configuration reads last 30 seconds; dynamic lists and completion reads last
5 seconds. TTL is checked on access and creates no polling. Domain events and
successful commands invalidate the affected Space; a confirmed new boot drops its
old resource values. Reconnect resets require revalidation while retaining display
data. Invalidation during IO requires a follow-up read before publishing. Leaving
one view never aborts a request shared by other consumers. Failures retain old
values and show a localized retry notice. The bounded cache clones values for each
consumer so local projection changes do not mutate other views.

Task and schedule pagination cache each complete source collection as one entry,
independently of backend page count, and retain the existing global merge/sort. Task detail keeps a bounded transcript cache and resumes events from
its cursor, reading messages and record updates concurrently. Usage page offsets,
scroll positions and disclosures remain view state. Chat restores confirmed
snapshots with Space and Control identity, then waits for the subscription's
initial authoritative snapshot before enabling sends; it needs no extra snapshot
GET. Existing cursor and generation fences reject late or older stream responses.
Background updates do not replace editor sessions or their dirty values. Unsaved
forms retain their existing leave protection, with no cross-tab draft persistence.
All commands still validate permissions and versions at the owning service.

## Native editing and ownership

All hosts use DOM textareas and inputs. The WebView/browser owns shaping, caret,
selection, undo and IME; the shell owns the composer frame, send/stop actions,
drafts and reference completion. Stream updates retain the live editor node and
do not rewrite its value. Only explicit restoration, matching send receipts and
completion replace text. Composition events, `isComposing` and key code 229 keep
candidate confirmation from choosing a completion or sending a message.

The shared Chat composer accepts image/PDF files through DOM selection, drop and
screenshot paste. It uses the same bounded byte upload API in browsers and the
macOS WebView; native attachment downloads use the host save dialog. See
[Chat attachments](chat-attachments.md) for formats, capability checks and retention.

Drafts, cached conversations, positions and late-response fences include the
Control/Space and assistant identity. A matching authoritative receipt clears
only its submitted draft. Selecting another assistant does not change ownership
of a request already in flight. Task Usage uses the Task ID for accounting while
requests retain the owning assistant; its Back/history path preserves the
independently selected Chat. Task approvals follow the Task owner and exact Task,
including when a different assistant remains selected in Chat. See [Multi-Space UI](multi-space-ui.md).

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
the original Space, assistant, Task/run and exact request identity.

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
and Space before every request. Desktop calls Go through Wails and receives
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

## Read state and activity

Completed replies have a durable reply ID/count. Initial catalogue history becomes
the baseline rather than unread mail. A new live reply can rebound its avatar
once; reconnect catch-up and duplicate updates do not replay it. Native read
baselines are held by the Go attention service and shared between windows through
`pot:read`. Go accepts a read only for the observed latest reply in a visible,
focused Chat. Browsers keep baselines in local storage and synchronize same-origin
tabs. Keys include the Control identity so a rebuilt database cannot inherit an
unrelated baseline. Reduced Motion disables reactions and native tray animation.

## System notifications

General settings has one **System notifications** preference, enabled by default.
This expresses delivery intent; it does not grant browser or macOS permission.
The setting shows the current permission, an explicit authorization action while
permission is undetermined, and instructions for browser/system settings after a
denial. Mounting, reconnecting and background events never request permission.
An insecure or unsupported browser reports its actual limitation. Pot does not
play audio: sound, banners and Do Not Disturb follow browser/macOS settings.

Browsers use the Notification API in a secure context (HTTPS or localhost).
Windows and Linux are supported through the Web UI. The macOS App uses Wails'
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
  identifies a completed assistant reply. Token updates do not notify.
- Task `complete` notifies success; `failed` and `interrupted` notify failure.
  Other phases, including explicit cancellation, do not notify. Task identity
  and a durable outcome sequence distinguish subsequent terminal transitions.
  The compact [Task outcome API](task-coordination.md#client-notification-outcomes)
  reads only changes after a client cursor. `task-outcomes` invalidations wake
  this reader; token/output updates do not query notification state.
- New pending permission request IDs notify approval. Tool arguments and full
  assistant responses are never included; text contains a bounded name and a
  short event description.

Each assistant has separate Task-outcome and approval refresh queues. A failed
read retains that reader's last successful cursor or baseline; it cannot reset
another assistant, erase a reply baseline, or block approvals behind Task reads.
Approval responses include an optional `task_title` for the notification name.

Initial reads, the first read after disconnection, and Runtime boot replacements
establish baselines. An in-flight read from a previous connection cannot establish
that baseline. Notifications use explicit Control, Space, assistant and optional
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

Up to 20 bounded records remain in memory and same-tab session storage. URL
queries/fragments and common credential patterns are redacted. Diagnostics do
not collect request bodies, fields or transcripts, and are never automatically
uploaded. Storage/clipboard failure does not interrupt reporting. A blocked JS
main thread cannot run exception handlers.
