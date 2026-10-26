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

General settings has one **System notifications** switch, enabled by default.
Delivery intent, API support and OS/browser permission are separate. The switch
belongs to this installation; it cannot grant permission or change another
installation. Authorization requires the explicit **Allow notifications** action
in browsers. Denial, an unanswered prompt and disabled OS banners never count as
an unsupported API and never trigger a different permission channel.

The local browser constructor is preferred whenever supported, including an
installed desktop PWA. An available `PushManager` or standalone display does not
move a local-capable browser to Push. iOS and Android expose notification interfaces
without supporting the page constructor; these environments use standard Web
Push when the required Service Worker APIs are present. On iOS 16.4 or later,
Pot must be opened as a Home Screen application over trusted HTTPS. Ordinary iOS
browsers show Safari's Share → Add to Home Screen instructions in the same setting.
Unsupported and insecure environments show their actual limitation.

The existing non-modal desktop browser introduction appears only after the main
interface is ready, with undetermined permission and enabled delivery intent.
Only its authorization click requests permission. A permanent, atomic IndexedDB
claim prevents repeat introductions across tabs and node identities; blocked
storage skips discovery and leaves settings available. The native main window's
first directory load and paint still signals its shared authorization coordinator;
tray startup cannot request permission. Native automatic/manual requests share one
in-flight attempt and the durable attempted marker. Native and Web preferences
remain independent; native preference/authorization history stays in the private
`system-notifications.json`, while browser intent stays in origin local storage.

### Events and independent delivery services

`internal/systemnotifications` produces the notification event contract. Its
resident source adapter reads authenticated Control resources after `/changes`
invalidations, independently of any page lifetime. It owns stable event IDs,
business selection, source cursors and current owner/boot authority. It has no
Push sender, subscription, permission, device registration or local/Push branch.
The adapter exists because committed reply catalogue, Task outcome and approval
resources have different cursors and capability boundaries; it does not deliver
notifications or request model work.

```text
Control commits -> /changes -> source rules + owner/cursor fences
                                      |
                           durable notification event log
                         independent consumer subscriptions
                           /                         \
                  native consumer              browser recipients
                         |                     single chosen channel
               existing preferences,            /               \
               window state, claims      local receipts       Push receipts
                         |                      |                   |
                 native notifier        page Notification    PWA Push service
                                                               subscriptions,
                                                        VAPID, retry, invalidation
                                                                   |
                                                           encrypted HTTP -> SW
```

- A changed completed `reply_id` with an increased `reply_count` produces a reply,
  including for linked assistants. Preview whitespace collapses and content is
  bounded to 120 Unicode code points; empty previews use the localized phrase.
- Task outcome sequences identify `complete` and `interrupted`. Failure first
  enters assistant recovery; only an unresolved assistant reply alerts.
  Cancellation/intermediate phases never alert. The compact owner-scoped
  [Task outcome API](task-coordination.md#client-notification-outcomes) supplies
  cursors, without polling tool/output histories.
- New pending approval IDs produce bounded names and a localized description;
  notification events never read tool arguments.

Reply, Task and approval baselines are independent. A slow or failing reader
cannot block another reader/owner. A transient resource read keeps its successful
cursor and retries with cancellable backoff from 250 milliseconds to 15 seconds,
even if no new topic arrives. Named heartbeats do not change business versions.
An initial snapshot schedules baselines once. Startup, source reconnect and
Runtime replacement establish fresh baselines, with no historical flood. Late
reads revalidate owner, boot and linked capabilities before committing. Retained
catalogue data never grants authority.

Each consumer has an independent in-process cursor over the persisted log. Its
buffered memory channel is only a coalesced wakeup; notifications are not passed
through a competing Go channel. A failed consumer queue write leaves the record
unacknowledged for retry. New subscriptions start at the log tail, intentionally
not replaying old events after startup. This is not an exactly-once broker.
Browser recipients record their activation sequence in the same SQLite writer
transaction as intent changes, so delayed event consumption cannot notify a new
or re-enabled installation about events committed before activation.

`internal/browsernotifications` stores browser intent, session bindings, reading
leases and per-installation receipts. Its single event consumer
queues each installation once on its selected channel; local receivers and
`internal/pwapush.Service` consume their own receipts. Push owns subscription
registration, private VAPID material, encrypted HTTP sending, provider retries
and invalidation. Composition/startup connects these services;
the source never starts or selects a delivery mechanism.

Events use stable Control identity as canonical Space. Browser presentation may
call that Space `local`; current confirmed identity is mapped at lease/click
boundaries. The shared `View.Viewing` predicate suppresses an assistant's events
while its corresponding Chat is focused, or an exact Task's events while that
Task is focused. A different Chat, background or minimized window permits delivery.
Browser sessions renew reading leases every 10 seconds, expiring at 25 seconds;
monotonic sequences reject late updates. Native delivery uses actual Wails window
visibility/focus directly, without browser registration, leases or receipts.
Main/tray share the existing native preference, authorization and durable claims.
An OS send error retains uncertainty rather than blindly resending.

Browser event/device receipts distinguish suppressed, queued, sending, accepted,
failed, unknown and expired. Reservation records uncertainty before an external
effect; acceptance afterward does not prove the OS showed a banner. Devices cannot
consume each other's work. Network errors/interruption never trigger a blind
resend or another channel. Push owns at most three attempts for explicit temporary
provider rejection, with backoff bounded to one minute; 404/410 clears that
subscription. Local age/lease limits are 25 seconds; Push queue age/TTL is five
minutes. Event history is bounded to seven days. Source authority cycles fence
queued events from disconnected/replaced owners.

Local browser receivers retain one Web Lock per installation during transient
IO retry. The first receiver establishes its baseline while holding the lock;
standby takeover retains current queued work. Permission rejection and disabled
intent stop retry. `pagehide`, offline and freeze abort notification IO and the
lease timer; `pageshow`, online and resume restore only notification transport.
Every setup continuation checks current lifetime/intent before writes or more IO.
Disposal/suspension keeps an enabled background Push installation, while explicit
off removes only that device. Chat subscribe/attach/recovery is independent.

A live page is required for local browser delivery. Native consumption continues
while windows are hidden and stops on Quit. The backend Push consumer continues
with closed/suspended pages; it does not rely on page SSE/timers or silent Push.

### Push installation and operation

The browser host's authenticated `/api/v1/notifications/` routes register, update,
remove and acknowledge installations. They require the current HttpOnly session
cookie, same-origin checks and current Control/owner scope. Devices are bound to
that session and access configuration generation, with the cookie's expiry. A
registration reads and validates its body before taking the server authority
lock, then rechecks current authentication and commits while holding that same
lock used by logout and access replacement. Revocation cannot be followed by a
late registration from a request authorized before its body was read. A
logout retires local installation writers before revoking that session's devices;
late responses cannot re-enable them. Token replacement/disable revokes all of
that browser host's devices, including after restart. Turning the notification
switch off removes only the current device registration. The browser may retain
its inert Push subscription for explicit re-enable without another permission
prompt. Reload reuses an existing subscription with the current VAPID key and never
unsubscribes it on startup. A subscription bound to an old/different VAPID key
requires explicit setup and is not adopted into the new service.

VAPID is generated once and stored privately in the browser service's
`control.sqlite`, separate from the retired `browser-push.sqlite` and legacy
configuration secrets. Keep the browser service's complete private root across
restarts/backups; replacing that root replaces its installation and VAPID identity.
The additive Control migration preserves existing registry, credentials, chats,
Tasks and receipts. It does not read, import or activate old Push subscription
files. Notification installation/click IndexedDB state has its own versioned
migration in `pkg/storage/migrations/ui-notifications/`; it contains no access
token, subscription secret or tool arguments.

Only supported browser push providers (Apple, FCM, Mozilla and Windows) are
accepted. Endpoint scheme/host, P-256 point and authentication key lengths are
validated. The HTTP transport rejects redirects, proxies and non-public DNS
addresses, including tailnet/private/link-local destinations, and pins resolved
addresses for the connection. Payloads contain bounded display content and an
exact structured destination, with no access token or subscription keys. A 404/410
clears and invalidates that device subscription; a late response cannot invalidate
a replacement. Re-registering the same invalid subscription requests local
cleanup and an explicit setup action, rather than silently looping.

The module Service Worker retains the immutable asset cache and excludes API,
authentication and native bridge traffic. Push promptly calls `showNotification`
after verifying the current installation ID, Control and enabled channel. Clicks
focus an existing same-origin Pot root window or open the fixed `/` root; payloads
cannot choose a URL. A pending exact Chat/Task target survives authentication and
is consumed only after the current main directory is ready. Logout clears it;
a missing or stale destination uses the existing localized navigation error.

Tailscale plus trusted HTTPS needs no public inbound Pot listener. The backend
needs outbound HTTPS to the browser's push service. The phone must be able to
connect back to Pot through its tailnet when opening a notification. Delivery is
subject to browser/OS scheduling, Focus and power policies and is not strictly
instantaneous. Pot does not play audio; sounds and banners follow system settings.

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
