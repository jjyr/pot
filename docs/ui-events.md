# Event-driven UI updates

The Wails main window, tray panel and browser load initial projections and then
subscribe to backend changes. Every chat uses the same conversation subscription;
adapters own native wakeups, peer polling and reconnection (see the
[chat contract](agent-links.md#chat-execution-projection)). A subscription does not
imply token streaming support at every endpoint. Input events, preference changes, request completion
and subscriptions wake the shared DOM components.

## Committed business event stream

`GET /api/v1/lifecycle` requires the same operator identity as other owner APIs.
It carries full structured reply, Task and approval facts after business commit,
plus current access metadata and heartbeat frames. Native and browser-service
notification consumers use this stream independently of page lifetime. It has no
history replay; a reconnect does not scan old business state. `/changes` remains
an invalidation interface for UI projections and is not a notification source.
See [Lifecycle hooks](hooks.md) and [System notifications](shared-ui.md#system-notifications).

## Local invalidation stream

`GET /api/v1/changes` requires operator authorization. The standalone browser
server exposes it through its authenticated proxy. SSE `changed` frames contain
a map of domain versions, such as `assistants`, `tasks`, `task-outcomes`, `schedules`, `usage`,
`machines`, `resources`, `permissions` and `updates`. Frame IDs contain the Runtime boot ID
and a stream sequence. Version markers are invalidation state, not durable
conversation replay cursors.

Subscriptions deliver their initial version map and coalesce pending changes.
The frontend compares versions per local presentation scope through
[`state/changes.mjs`](../web/src/state/changes.mjs). A boot change invalidates the
version baseline. Usage-only updates do not reload the assistant directory. Permission changes
refresh catalogue `pending_approvals` counts for every assistant independently of
reply identity and unread baselines. They also refresh the selected owner's
transient Chat approval region; duplicate request IDs do not repeat its notice.
Mutations publish after persistence; no-op reads do not produce invalidations.
The global subscription remains active even when no assistant exists, so an
assistant created by another client appears without reloading the page.

Frontend repositories persist domain invalidation epochs before refreshed queries
can publish. A query fetched before an event or successful command cannot replace
a newer projection, including across tabs. In-memory invalidation immediately
revokes stale selection eligibility; query subscribers consume only committed
values. Storage failure retains the prior projection and retries the pending
invalidation before another refresh.

Shared domain queries serialize reads and coalesce changes into one later read.
Event batches use blocking `authoritative: 'invalidated'` resource reads: they
wait for each invalidated resource, reusing a committed read that already crossed
that resource's version fence. The later query still reassembles all resources
and pagination from the beginning, so an earlier resource cannot escape an event
that arrived while a later one was loading. A query that crosses an event stays
stale until this reassembly completes. Persistent commit retries report their
last IO's starting version to the resource fence; events after that start still
require revalidation. The operator Task catalogue also depends on assistant
directory changes, which can change its owner membership.
An explicit forced refresh upgrades that queued read to authoritative IO and
waits for its own batch, including its commit or error. Explicit `true` takes
precedence over event revalidation when batches merge. Commit notifications
during that batch may schedule another local query without extending the forced
call's wait. Storage failures remain explicit errors with the prior committed
projection available for retry; directory and host metadata loaders must not
turn them into successful offline results.

Change callbacks retain their originating scope and boot identity. Each view
declares the domains and scope it reads; unrelated notifications do not refresh
it. General preferences and build information remain mounted without requesting
them again on execution updates.

[`state/refresh.mjs`](../web/src/state/refresh.mjs) coalesces invalidations for
pages, the assistant directory, background tasks, execution transcripts and
approval requests. Each view runs one read at a time and schedules one later
read when changes arrive during it. An invalidation never cancels that read.
Navigation, editor entry and filter/owner changes retire the request generation.

Existing projections remain visible during background reads and after failures,
with inline errors and retry where appropriate. Cards, table rows and chart buckets
reconcile by their scoped identities. Search inputs, focus, selection, disclosure
state and scroll positions survive unchanged projections. Shell navigation,
approval actions and Chat task strips retain their nodes too. Action handlers use
the current projection; pending mutations stay disabled across refreshes.
The assistant directory retains its last confirmed execution activity during a
background revalidation in the same connection and Control scope. A stale read
does not imply that execution ended. Retained directory entries still lose command
capabilities until confirmed again; execution activity does not grant authority.
A confirmed completion replaces that activity, and a cold retained cache or new
connection cannot establish execution activity from an older connection.
An open conversation menu keeps existing actions and keyboard focus during a
retained directory revalidation, marking unavailable actions `aria-disabled` and
rejecting their execution until fresh capabilities return. This presentation is
limited to the same scope, assistant, subscription, boot and active turn state;
offline destinations, storage failures and fresh capability removal still remove
unavailable actions. Settings remains available. Handlers recheck current capability,
scope and subscription identity; turn controls also require the same active turn,
conversation boot and pause state before sending a request.

Task details refresh metadata, results, files and guidance alongside their
incremental execution transcript. Schedule details and run history share the same
background lifecycle as lists. Machine settings expand locally without reads. Agent Link state retains the
selected node and assistant directory across refreshes.
Third-party account login status uses the same queue and retains its row during
the bounded login-session status checks.

The desktop local UI adapter consumes the same backend subscriptions and emits
`pot:changed` through Wails. Responses and changes retain their local scope;
Agent Link peer operations use their explicit connection identity.

## Browser stream recovery

Conversation and change streams send a named `heartbeat` event with `{}` data
at 15-second intervals. A heartbeat carries transport liveness only: it does
not advance a cursor, invalidate a resource or make the composer ready.

The browser adapter owns one EventSource, watchdog and retry schedule per
captured owner/Space. It closes the previous source before reconnecting, rather
than competing with native EventSource retries or an additional UI reattach.
Headers and the first valid conversation reset (or initial change event) have a
20-second deadline. Heartbeats cannot satisfy or extend that deadline. After
initial data, 45 seconds without heartbeat or data retires the stream. The first
failure retries immediately; continuing failures use exponential backoff with
jitter, capped at 30 seconds. Data or heartbeat after 15 seconds with an accepted
initial frame resets backoff; repeatedly opening, sending a reset and immediately
closing cannot create a hot retry loop.

Hidden pages pause watchdog/retry timers. Visible, focus, pageshow and online
wakeups compare wall-clock deadlines before resuming them, so a suspended PWA
can replace an expired stream without waiting for a throttled timer. Healthy
streams survive these wakeups. Offline transitions close the stream; owner
changes and disposal cancel its timers and terminal-status request. Cancellation
revokes probe ownership synchronously, so online recovery does not wait for an
aborted request to settle. Old source and probe callbacks cannot update the new
generation or clear a newer probe.

A terminal EventSource response triggers one bounded, cancellable same-origin
access-status check. An expired session remains offline with an explicit
reconnection message instead of a retry loop; an explicit access denial (403)
also pauses recovery. Both remain paused across lifecycle wakeups and
offline/online transitions. A confirmed valid session or an unknown result
(including a probe timeout or network failure) retries with the same backoff.
This also lets the global changes stream recover without a consumer Retry
button. Opening the stream and receiving heartbeats never bypass the
conversation cache: only a committed current snapshot makes Chat connected.
Storage failures remain visible and retain drafts; transport reconnects neither
replay sends nor recreate the composer or its reading position.

## Main conversation streams

`GET /api/v1/events?assistant_id=...&locale=...` sends UI-only `conversation` frames.
Snapshot reads and subscriptions carry the UI locale through the common endpoint
so first-use introductions use the reader's language.
The first frame has `kind: reset`; subsequent frames have `kind: delta` and a
`base_cursor` matching the preceding frame's cursor. `view.state` carries Runtime
boot identity, conversation cursor, turn, budget, assistant capabilities and the
original continuation boundary. Continuation survives bounded reset/delta reads
even when its original input is outside the loaded message window.
`view.messages` contains message envelopes (`message`, decimal-string `sequence`,
`version`, optional `context_only`); `view.history` describes the loaded interval.
Correspondence inputs also carry UI-only `correspondence` envelopes read from
the original inbox batch journal. Snapshot, delta and history reads project the
same incoming-message disclosure record. This metadata is not persisted into the
conversation message or used as model history; the system reference input is
already part of the owning assistant's transcript.
`view.replies` describes per-input execution using `input_id`, `state`, optional
`error` and `uncertain`. States are pending, queued, running, waiting, paused,
cancelling, completed, failed, cancelled and unknown. Pending means a locally
accepted input has no confirmed receiver execution state yet. Unknown is a
terminal unconfirmed outcome that must not replay work. Uncertain marks a cached
state whose receiver cannot currently be queried; it never fabricates failure.
Both native and peer adapters supply this projection. Reset frames replace the
projection; delta frames update the lifecycles they contain. A completed intermediate
message does not end an active reply. A successful result without text or a
resource presentation silently removes its activity bubble; the UI does not
invent a chat message from execution completion. Failures, cancellation and
unconfirmed outcomes remain visible without requiring reply text.
A completion persistence failure keeps the subscription open and projects the
reply as `unknown` with the original diagnostic. The owning receiver retries
only completion writes with bounded attempts and backoff; separately accepted
user inputs remain queued. Once persistence succeeds, the original outcome and
queued work advance normally. The private completion journal also supports
restart without replaying model or tool work. If the durable cursor cannot
advance during a storage failure, reset frames can refresh the retained cursor.
Cancelling an allowance-waiting turn during completion recovery records the
intent under its original identity. The completion loop persists cancellation
and acknowledges it before admitting the next queued input.
Lifecycle placeholders without a durable message sequence stay after their
input's last loaded output or tool call. Later messages follow them; lifecycle
array order and subsequent refreshes do not move old outcomes to the chat tail.

`GET /api/v1/snapshot` returns the same bounded reset frame. Protocol version 7
uses this contract for browser and Wails streams.
Completed assistant messages may include a typed resource `presentation` without
Markdown text; it has its own durable message identity and unread entry. System
messages may carry `compaction` with operation ID, trigger, lifecycle state,
estimated before/after tokens and completion time. These are independent UI
operations, never tool calls or model-history instructions.

Reset frames read the latest 80 messages. `GET /api/v1/history` accepts one of
`before`, `after` (exclusive sequence), or `around` (message ID), and an optional
`limit` of 1–120 (default 80). The response includes boot/conversation identity,
read cursor, messages, and `history: {before, after, has_more, has_newer}`. Sequence
is the SQLite insertion row ID: message edits preserve it and timestamps do not
affect ordering. Missing `around` targets return `not_found`; the normal scoped
assistant lookup and authenticated browser proxy govern every read.

A window adds up to 32 presentation dependencies (reply input and composition
ancestors), plus the active input if needed. Context-only input rows inform reply
identity without appearing as extra user messages. Context tool ancestors appear
as enclosing disclosures and do not advance pagination boundaries. This cap keeps
a very long active turn from forcing an unbounded initial read.

Subscriptions register a capacity-one wakeup channel before reading state. On a
wake, the server reads the durable cursor interval and coalesces changed message
IDs in SQL, returning their current bodies plus current state. A gap exceeding
256 journal entries produces a bounded reset. Message text updates replace that
message's projection; they do not resend unrelated history. HTTP writes retain
the stream write deadline. No token queue grows behind a slow consumer.

The browser uses EventSource and closes the old source when switching owners.
Desktop forwards the same frames through `pot:snapshot`; its local UI adapter
reconnects with bounded backoff. Every new connection begins with a reset. The
client rejects old cursors and duplicate deltas and renews the subscription on a
base-cursor gap or boot change. Read versions prevent a concurrent older page
from overwriting newer token text. A bounded recent-update map also covers
updates received while a page is in flight.

Main Chat uses IndexedDB as its only retained transcript cache in browsers and
Wails. The scope includes local node, connection, assistant and conversation
identity. Message bodies, versions, reply lifecycles and the accepted cursor
commit together before the renderer reads the committed projection. Streaming
deltas coalesce in short batches; the pending transport buffer is bounded and
cannot publish uncommitted output. A failed cache write retains the existing
view and shows a localized retry error; it never creates a memory-cache fallback.

```text
HTTP/SSE or Wails -> scoped synchronization -> IndexedDB transaction
                                                 |
                                            commit complete
                                                 |
                                      read current view window
                                                 |
                                      measure + position -> reveal
```

Only the selected assistant's bounded projection stays in application memory:
at most 640 messages and a 16 MiB estimated body budget. IndexedDB retains up to
2048 messages / 32 MiB per assistant and 256 MiB globally; older inactive scopes
are evicted first. Cached history intervals are used before backend reads, and
unknown or evicted intervals are fetched again. Reset gaps invalidate cached
interval provenance. Versions fence late pages, transactions serialize writers,
and retired boot identities cannot restore an earlier projection. The transcript projection is a
rebuildable UI cache, never the authoritative conversation store or a send queue.
[Assistant drafts](assistant-drafts.md) share the database through separate stores
that are excluded from projection retention and cache clearing.

Window and persistent body budgets count UTF-8 encoded message JSON plus a
256-byte allowance per row. Task text, combined tool output text, serialized
arguments and results each retain at most 1 MiB of UTF-8 content; truncation keeps
complete Unicode characters and marks the cached output as bounded. Window
eviction retains active output and context dependencies. An oversized row or
dependency set that cannot fit fails explicitly rather than publishing an
over-budget projection.

The reducer coalesces short stream bursts in a 16 ms window and drains terminal
and error frames promptly. Its one active committed projection is checked against
the persistent revision. Writes read and update changed message IDs without a
full history scan or a write-followed-by-full-read. Sequence indexes return bounded
history windows, and tool indexes resolve required ancestors. Counts and byte
totals update incrementally; over-budget writes visit eviction victims only.
Deletion retires the local key so late frames cannot restore it; replaced
conversations also reject their retired identities. Cross-tab notifications
announce committed keys; recipients query storage. Version changes close old
database connections.

Position/disclosure saves prune expired surfaces after 30 days and retain at most
32 surfaces per conversation, outside the stream write path. Position-save failure
shows a storage notice without declaring the network offline. Transcript commit
failure keeps the last committed output and blocks new sends until recovery;
Stop remains available from confirmed live state. Restoring cached output does
not mark the subscription connected or re-enable sending.

Reading position, disclosure state and bounded height hints persist per view;
main/tray windows and browser tabs have independent positions. Returning to Chat
reads its target window from disk while the subscription refreshes it. A reset
while reading an older message reloads that message's window. References resolve
by ID, without traversing intervening pages.
All Chat entry paths share a scoped positioning intent: restore the saved reading
position, locate an explicit message, or follow Latest. Page returns, assistant
switches and browser history prepare the composer, mount the destination window,
measure its rows and restore the viewport before exposing the first Chat frame.
Composer preparation includes restoring the selected assistant's persisted draft,
references and attachments. A failed draft read exposes its recovery controls;
an unrelated assistant's cleanup failure does not complete the selected draft's
preparation.
Hidden rows do not overwrite measured heights. The destination remains visually
hidden through mounting, measurement and position restoration; only the prepared
viewport is exposed. Persisted height hints are qualified by width, locale, theme
and font revision; content changes remeasure mounted rows. Explicit message/Latest actions supersede prior positioning, and
navigation or deliberate upward reading retires an in-flight locate so its late
response cannot replace the current history window or move the reader.
An explicit message destination stays anchored through delayed preview loading;
pointer, touch, scrolling or keyboard interaction releases that positioning intent.
Returning to an assistant that was following the latest messages keeps following
through the cached projection, stream reset and background-task refresh. Visible
task rows do not turn following into a saved historical reading position. Only
paused reading restores a message or task anchor; restoration requests are
scoped to the originating navigation generation.

Submitting a message explicitly resumes Latest following before its optimistic
row is rendered, including while the submitted draft is being persisted. A later
receipt or draft flush does not issue another positioning command. Subsequent
reading gestures and assistant navigation therefore retain their newer intent;
ordinary background snapshots preserve historical reading.

Main Chat and Task transcripts share the same conversation-list controller. It
owns one intent: Latest following, historical reading by stable message/row ID
and viewport offset, or an explicit message destination. Pages supply committed
messages with parent identities, issue explicit navigation commands, and consume
`subscribeReading` publications. `setMessages` changes data; `restore` requests a
saved destination once on navigation; `scrollToLatest` and `locate` are deliberate
commands. `snapshot` reads the last published state without adopting native
movement or commanding another restoration. A scope reset retires the old
intent, pending engine commands and touch ownership before the new projection.
The persisted reading-position representation remains the same.

The list uses the vendored TanStack Virtual Core for indexed item offsets,
measured ranges and scroll execution. Its independent append following, end
anchoring and size-change compensation are disabled. Only the conversation
controller allocates a geometry correction. It first consumes real native input
and synchronizes the engine offset, then commits the flow DOM and measurements,
resolves its current stable reading point and applies any remaining displacement.
Native input translates the previously committed anchor by the input delta;
it does not recapture an unreported DOM height change as new reading intent.
Programmatic write echoes do not become input. Geometry that happens to reach
the bottom does not enable following; downward user movement to the bottom or
an explicit Latest command does.

The same reconciliation handles prepend, trimming, reorder, width reflow,
external header/composer layout and asynchronous image or disclosure sizes.
Newly mounted or updated rows have synchronous initial measurements, including
during native scrolling; later ResizeObserver batches update the same geometry.
A delayed or repeated measurement of an already accounted height yields zero
remaining displacement. Independent height changes can receive independent
corrections. Reading state is published after the resulting geometry settles,
rather than by a separate cursor's layout-finally callback.

TanStack retains native touch ownership and executes deferred corrections through
contact, dragging and the momentum tail. The controller accounts for a held
correction when the engine announces its release before the native write, so a
synchronous range commit cannot allocate that displacement twice. Wheel and key
motion use the same input-before-geometry model; native keyboard tails remain
owned through engine idle rather than being retired by a programmatic scrollend.
A new gesture cancels explicit navigation. Nested scrollable outputs consume
input while they have room; a pointer cancellation handing control to touch does
not retire native momentum protection. The controller adds no motion timers.

Pot retains its normal message DOM/CSS and renders virtual gaps as flow spacers,
with twelve overscan items on each side. Native scroll notifications commit the
range synchronously. Held touch corrections may require a negative leading
spacer margin. Each DOM replacement temporarily reserves the host extent until
all rows and spacers are in place, preventing native clamping against an
intermediate shorter list. Initial measurement and subsequent bounded range
commits avoid scanning or rendering the whole transcript. Item margins are
included in sizes; width changes invalidate mounted measurements, and each
cached owner retains bounded height hints.

Expanded tools have range-only child virtualizers sharing the chat scroller;
only the root controller owns reading intent and scroll compensation. Child
windows share its destination and parent path, report geometry and never write
the parent scroller. Focus and selected
text pin mounted rows through a custom range extractor. Navigation targets resolve
by message identity against each current window; trimming, replacing or reordering
rows never reuses a previous numeric target index. Retiring a row immediately
disposes its DOM, nested window and attachment leases. No detached transcript DOM
or inactive assistant projection remains cached in application memory. Returning
to a recycled row renders it from its persisted message and disclosure state;
resources use the shared resource cache.
Scroll-only updates reuse the conversation's message projection and tool forest;
new data, send state and connectivity invalidate that projection. Collapsed tools
build their summaries and approval controls first; output and raw JSON render
only when their disclosures open. Recycling releases observers and attachment
leases, while shared viewers/downloads retain their own leases. Unchanged replies
retain their Markdown nodes.

Main Chat and Task conversations publish their list controller's reading state
for page and conversation persistence. Composers retain native textarea, IME,
selection and undo behavior. Their normal DOM sizing changes are observed by the
list; they do not wrap work in reading-cursor transactions or issue position
restores after rendering. Input growth while following keeps the end visible;
historical reading keeps its stable row offset through viewport changes. The
same owner handles background Task disclosure, approval regions and delayed
resources outside recycled message rows.

These APIs only project UI messages. Internal `Conversation.Snapshot`, the full
`c.messages` collection, model checkpoints, input queues, persistence, recovery,
retry and model requests are unchanged. Sidebar reply IDs and counts still come
from the complete conversation catalogue/outbox, independently of UI windows.

Returning to a visible window, restoring a page or regaining network connectivity
also renews the conversation and change subscriptions and refreshes current
projections. Paired focus/visibility events coalesce, and recovery reads do not
overlap. Recovery retains drafts and in-flight send receipts without resending
input; retired stream callbacks cannot overwrite the renewed subscription.

The shared conversation state validates owner/Runtime/boot identity and rejects
older cursors. Navigation generations fence late responses, including A → B → A.
Snapshot refresh retains composer nodes, drafts and reading position. It follows
the bottom only when the reader was already near it; otherwise the latest-message
action remains available. Upward wheel, touch and keyboard gestures pause following
immediately, including small movements within the bottom proximity threshold.
Following resumes at the bottom or through Latest. Older and newer history start
prefetching within two viewports of the relevant edge (at least 360 px), extending
up to four viewports with observed scroll speed and request latency. Upward gestures also trigger it
when the loaded content cannot scroll. Page reads remain serialized and preserve
the visible message anchor. When the complete conversation fits in the viewport,
upward gestures leave it still, without history reads or a Latest action; new
messages continue following. Only confirmation matching the captured request and
owner clears a submitted attempt. A user message's `request_id` confirms an ordinary send; steer messages
carry independent `accepted_request_id` and `accepted_turn_id` fields, leaving
`request_id`'s turn semantics unchanged. Reset/delta acceptance is checked before
UI window filtering. It confirms persistence, not Agent consumption.

`GET /api/v1/messages/receipt` reads durable storage outside the message window.
The authenticated node/assistant scope and `conversation_id`, `request_id`, and
optional `user_message_id` (steer) or `retry_user_message_id` identify the attempt.
The response repeats that identity plus `runtime_id`, `owner_id` and `state`:
`accepted` includes `message_id`; `unconfirmed` proves neither outcome; `rejected`
means an absent steer targets a turn that no longer accepts guidance. Reads do
not start or interrupt Agent work.

The normal submit path waits only for its business response: success confirms
immediately, and an explicit rejection retains the draft and reports the error.
No receipt query runs while that POST is pending, including on foreground/online
signals. A matching persisted SSE message may also confirm the same request
without waiting for the POST; Agent execution completion is not required.

The production send adapter returns a valid acceptance response before local
command projection saving completes. Its `committed` promise and `retryCommit`
function track that separate save; the submit timeout never covers it. Saving
still uses the original command ID and invalidates conversation/directory reads
after commit. A local save failure shows the existing accepted-action storage
notice in the captured owner's Chat. Retry saves that response locally without
another POST or receipt read; it cannot restore the submitted draft or erase a
newer one. Other mutation commands retain their commit-before-return behavior.

A timeout, network failure or another inconclusive submit error makes the result
unknown and enables receipt checks. Each HTTP wait is bounded to ten seconds.
Five reads then use bounded backoff for the fixed payload and request ID. If they
are exhausted, foreground/online signals trigger one further read-only check.
Each unknown attempt has one active check; bursts coalesce with a one-second
cooldown and never restart periodic polling. Unconfirmed results or unavailable
reads retain the unknown attempt; 401/403 pauses automatic checks. Backgrounding
pauses receipt checks but lets a pending POST return its definitive response.
Offline/disposal cancels waits and revokes ownership before abort, so late results
cannot affect a newer check. Automatic recovery never transmits a message.

If unresolved, a localized Check and retry action queries
first and, only after an unconfirmed result, retries the same ID/payload in the
captured scope. Unavailable reads retain the unknown attempt. Subscription/boot
renewals and navigation retain it in the mounted client; newer drafts, references
and attachments survive late or duplicate confirmation.
[Assistant drafts](assistant-drafts.md) persist that immutable request fence across
closing/reloading the app and restore it through read-only confirmation.
Assistant messages bind to their input through `reply_to`; terminal failures retain
their original diagnostic in the message's `error` field, including preparation
failures before the first model event. Chat projects a single reply bubble from
the local send through its receipt and first assistant output. Pending sends are
scoped by owner, preserve drafts and retain request identity across reconciliation.
Turn `state` distinguishes allowance waiting from active thinking.
Connection status and reconnect actions stay in the Chat header, including while
a reply is running. Reply bubbles retain their available text without a separate
connection notice; Task conversations use their existing stage status.
Subscription errors retain their available diagnostics; a
frontend rendering exception is reported by the interface error handler rather
than being classified as a service disconnection.
The error log retains up to 20 distinct exceptions in the browser tab's session,
including across reloads; its count is historical, not a count of active faults.
Closing the alert leaves those records available. Clear log removes the records
and hides the log button; a subsequent exception starts a fresh log and alert.
Report issue opens the repository's GitHub Bug report form with the latest error
as its title, the available Pot version, browser environment and retained error
logs. The user can edit the report before submitting. The report uses the same
credential-redacted diagnostics as Copy log and omits the current page URL.
Long logs are included newest first and truncated with a notice to keep the
encoded link below 7,800 characters; Copy log retains the full available log.
Desktop hosts open the form in the system browser.
Task file download failures stay in the task view with localized retry and
diagnostic details rather than entering this page exception log.

## Task execution transcripts

`GET /api/v1/tasks/{id}/events?after=...&limit=...` pages the original run's
durable ordered log. Each row has an outer sequence and its execution update:
message boundaries, text deltas, tool starts/output/results and turn outcomes.
The reply contains `cursor`, `has_more` and `unavailable`. Historical remote pages
remain cached and marked unavailable. Task details advertise `task.events` when
transcript reads are supported. Clients omit unsupported reads and controls;
direct unsupported requests return a capability error. The original execution
owner remains fixed.

[`state/task-transcript.mjs`](../web/src/state/task-transcript.mjs) incrementally
reduces each ordered batch and ignores duplicates. New execution output invalidates
Tasks; an open Task detail fetches only rows after its cursor. Incremental DOM
updates preserve the guidance draft instead of rebuilding the form. Pagination
rejects a non-advancing cursor. Tools remain expandable and file links retain the
Task's explicit Control/assistant identity.

Task invalidations also refresh status, pause controls, guidance/reference delivery
states and terminal output in place. The original input appears once in a collapsed
Goal disclosure. The Agent emits an `input_applied` event with the input's request ID
after checkpoint persistence and before acknowledgment. These durable bookmarks
place delivered guidance and references before their continuation, independently
of response arrival order or machine clocks. Repeated bookmarks do not reorder
history. Pending inputs remain at the tail; retained historical inputs without a
bookmark remain identifiable without inventing a delivery position.

The task conversation uses the shared reply bubbles, tool disclosures, composer
binding, measured message windows and reference completions. Expanded tool trees
share its scroll container and retain disclosure state when recycled. Only its history scrolls; status and the composer
remain visible. Following stops when the reader scrolls upward, and Latest resumes
it. When the upper history boundary becomes visible, the view automatically reads
an earlier committed page while preserving its message anchor. An initial short
window uses the same boundary. There is one in-flight request and no repeated
attempt for an unchanged cursor; a failed page offers explicit Retry. Latest,
a new run and disposal cancel old history work. New execution output updates
matching historical rows without selecting the live tail. Navigation restores the goal/tool disclosures and reading position. Terminal
text already present in the final reply is not repeated. Ended tasks retain an
unsent draft read-only and expose no send action. Only a matching successful send
clears an unedited draft; failed sends retain their request identity for retry.

## Tool-call projections

Main Chat persists a structured `message.tool` projection separately from prose:
call/turn identity, composition parent, original arguments, resolved execution
target/workdir, lifecycle timestamps, ordered stream chunks and the raw result.
Completion never overwrites arguments or streamed output. Agent events carry
`at_ms`; `tool_state` records preparing, exact approval waiting, running and denied
transitions. Unsettled calls become stopped after cancellation or result-unconfirmed
when the original run cannot establish a terminal receipt. Restart preserves these
details and marks unfinished projections unconfirmed rather than replaying them.

Task transcripts reduce the same start/state/output/finish events. The local coordinator supplies
run-bound state updates to the execution Agent, which sequences them into its
durable journal alongside executor output.
Composition children have explicit `parent_id` values scoped by the original turn.
A returned process session or yielded script is distinct from completed execution.
The shared execution-history projection folds command session receipts before
building the composition forest. It associates process receipts by turn, machine
and session ID, and script receipts by turn and execution ID. This is presentation
only: original arguments, results and lifecycle events remain unchanged.

Chat and Tasks use the shared `components/tool-call.mjs` disclosure. Ordinary calls
start collapsed; exact approval requests open their call and composition ancestors.
Owner-scoped disclosure state and stable output nodes survive snapshots and
navigation. Output following pauses when the reader scrolls upward. Large displays
are bounded and clearly labeled; copy retains the complete available text, while
source truncation remains explicitly identified. Displayed outputs cannot recover
bytes the executor did not retain.

## Lifecycle and remaining clocks

SSE keepalive comments run every 15 seconds; writes have bounded deadlines that
are cleared after flushing, so idle HTTP/2 streams remain open between frames.
Backend shutdown or request cancellation closes subscriptions. Browser logout
revokes access and reloads into authentication. Window/client disposal removes
listeners and releases subscriptions.

Execution, schedule deadlines, authorization expiry, resource preparation and
Agent Link recovery retain their own backend lifecycle responsibilities.
Animation frames advance only active presentation effects; Reduced Motion makes
activity indicators static and disables avatar/tray animation. Local stream keepalives do not add UI snapshot reads.

See [Development](development.md) for backend and shared UI verification commands.
