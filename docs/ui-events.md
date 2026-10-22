# Event-driven UI updates

The Wails main window, tray panel and browser load initial projections and then
subscribe to backend changes. Every chat uses the same conversation subscription;
adapters own native wakeups, peer polling and reconnection (see the
[chat contract](agent-links.md#chat-execution-projection)). A subscription does not
imply token streaming support at every endpoint. Input events, preference changes, request completion
and subscriptions wake the shared DOM components.

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

Task details refresh metadata, results, files and guidance alongside their
incremental execution transcript. Schedule details and run history share the same
background lifecycle as lists. Machine settings expand locally without reads. Agent Link state retains the
selected node and assistant directory across refreshes.
Third-party account login status uses the same queue and retains its row during
the bounded login-session status checks.

The desktop local UI adapter consumes the same backend subscriptions and emits
`pot:changed` through Wails. Responses and changes retain their local scope;
Agent Link peer operations use their explicit connection identity.

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

The UI retains at most 640 messages and a 16 MiB estimated body budget per cached
owner, with at most eight owner snapshots. Reading farther back evicts distant
messages; scrolling down pages the newer interval, and Latest reads a recent
window directly. References resolve by ID, without traversing intervening pages.
Returning to cached Chat shows its projection immediately and refreshes it from
the stream. A reset while reading an older message reloads that message's window.
All Chat entry paths share a scoped positioning intent: restore the saved reading
position, locate an explicit message, or follow Latest. Page returns, assistant
switches and browser history prepare the composer, mount the destination window,
measure its rows and restore the viewport before exposing the first Chat frame.
Hidden rows do not overwrite measured heights. Each of the eight cached owners
can retain its bounded height estimates; width and content changes remeasure the
mounted rows. Explicit message/Latest actions supersede prior positioning, and
navigation or deliberate upward reading retires an in-flight locate so its late
response cannot replace the current history window or move the reader.
An explicit message destination stays anchored through delayed preview loading;
pointer, touch, scrolling or keyboard interaction releases that positioning intent.
Returning to an assistant that was following the latest messages keeps following
through the cached projection, stream reset and background-task refresh. Visible
task rows do not turn following into a saved historical reading position. Only
paused reading restores a message or task anchor; restoration callbacks are
scoped to the originating navigation generation.

Main Chat mounts measured windows around the viewport with pixel overscan.
The overscan covers two viewports in each direction and grows ahead of fast
scrolling by up to two more. A window prepares incoming rows before retiring
outgoing rows and retains up to 32 detached rows within a 2 MiB estimate of message
content and DOM text for nearby returns. Data eviction and owner changes retire
these rows too. Detached
media release their leases; reconnecting them uses the shared resource cache.
Scroll-only updates reuse the conversation's message projection and tool forest;
new data, send state and connectivity invalidate that projection. Task transcripts
likewise reuse their projection until their next update. Collapsed tools build
their summaries and approval controls first; output and raw JSON render only when
their respective disclosures open.
Expanded tool children have their own windows sharing the chat scroller. Resize
observers update placeholders; message-ID anchors compensate for prepend,
attachment decoding, disclosure, viewport and font changes. Focus and selected
text pin mounted rows. Recycling disconnects observers and releases attachment
leases, while shared viewers/downloads retain their own leases. Rendering merges
wakeups into animation frames; unchanged replies retain their Markdown nodes.

Main Chat and Task conversations share one reading-cursor controller per history.
The cursor owns Latest following, an anchored reading position (stable row key
and offset within the history viewport), or an explicit message/region destination.
DOM `scrollTop` is a projection of that cursor, not its source of truth. Wheel,
touch, keyboard, scrollbar and selection-drag scrolling update the cursor;
Latest, references, tool-failure links and approval navigation set explicit
destinations. Conversation and page caches retain the logical cursor, including
Task history, rather than inferring reading intent from restored pixel offsets.

Composers publish synchronous layout transactions without knowing the history's
reading mode. Input resizing, draft/reference/attachment controls, row measurement,
history paging and viewport reflow preserve the existing cursor and project it
back before paint. This includes a textarea temporarily shrinking and returning
to the same height within one input event, which need not deliver a resize
observation. Compensation scroll events never overwrite reading intent. Tool
lists delegate offset compensation to the same controller; nested output panes
retain their own scrolling. Explicit navigation retains the scoped generation
checks described above, so late history responses cannot supersede newer intent.

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

The client ends each HTTP wait after ten seconds and keeps the fixed payload and
request ID while acceptance is unknown. Five receipt reads use bounded backoff;
a matching persisted message or query unlocks the next send even if the HTTP
receipt is still held. If unresolved, a localized Check and retry action queries
first and, only after an unconfirmed result, retries the same ID/payload in the
captured scope. Unavailable reads retain the unknown attempt. Subscription/boot
renewals and navigation retain it in the mounted client; newer drafts, references
and attachments survive late or duplicate confirmation. This is in-memory
recovery, not persistence across closing/reloading the app.
Assistant messages bind to their input through `reply_to`; terminal failures retain
their original diagnostic in the message's `error` field, including preparation
failures before the first model event. Chat projects a single reply bubble from
the local send through its receipt and first assistant output. Pending sends are
scoped by owner, preserve drafts and retain request identity across reconciliation.
Turn `state` distinguishes allowance waiting from active thinking.
Subscription errors retain their available diagnostics; a
frontend rendering exception is reported by the interface error handler rather
than being classified as a service disconnection.

## Task execution transcripts

`GET /api/v1/tasks/{id}/events?after=...&limit=...` pages the original Runner's
durable ordered log. Each row has an outer sequence and its execution update:
message boundaries, text deltas, tool starts/output/results and turn outcomes.
The reply contains `cursor`, `has_more` and `unavailable`. Historical remote pages
remain cached and marked unavailable. Task details advertise `task.events` when
transcript reads are supported. Clients omit unsupported reads and controls;
direct unsupported requests return a capability error. The original execution
owner remains fixed.

[`state/task-transcript.mjs`](../web/src/state/task-transcript.mjs) incrementally
reduces each ordered batch and ignores duplicates. New Runner output invalidates
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
it. Navigation restores the goal/tool disclosures and reading position. Terminal
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
