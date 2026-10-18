# Event-driven UI updates

The Wails main window, tray panel and browser load initial projections and then
subscribe to backend changes. They do not poll lists or conversation snapshots
on a presentation interval. Input events, preference changes, request completion
and subscriptions wake the shared DOM components.

## Control invalidation stream

`GET /api/v1/changes` requires operator authorization. The standalone browser
server exposes it through its authenticated proxy. SSE `changed` frames contain
a map of domain versions, such as `assistants`, `tasks`, `schedules`, `usage`,
`machines`, `resources` and `permissions`. Frame IDs contain the Runtime boot ID
and a stream sequence. Version markers are invalidation state, not durable
conversation replay cursors.

Subscriptions deliver their initial version map and coalesce pending changes.
The frontend compares versions per Space through
[`state/changes.mjs`](../web/src/state/changes.mjs). A boot change invalidates the
version baseline. Usage-only updates do not reload the assistant directory. Permission changes
refresh catalogue `pending_approvals` counts for every assistant independently of
reply identity and unread baselines. They also refresh the selected owner's
transient Chat approval region; duplicate request IDs do not repeat its notice.
Mutations publish after persistence; no-op reads do not produce invalidations.
The global subscription remains active even when no assistant exists, so an
assistant created by another client appears without reloading the page.

Change callbacks retain their originating Space and boot identity. Each view
declares the domains and Space it reads; unrelated notifications do not refresh
it. General preferences and build information remain mounted without requesting
them again on execution updates.

[`state/refresh.mjs`](../web/src/state/refresh.mjs) coalesces invalidations for
pages, the assistant directory, background tasks, execution transcripts and
approval requests. Each view runs one read at a time and schedules one later
read when changes arrive during it. An invalidation never cancels that read.
Navigation, editor entry and filter/owner changes retire the request generation.

Existing projections remain visible during background reads and after failures,
with inline errors and retry where appropriate. Multi-Space projections retain
cached rows with explicit stale status. Cards, table rows and chart buckets
reconcile by their scoped identities. Search inputs, focus, selection, disclosure
state and scroll positions survive unchanged projections. Shell navigation,
approval actions and Chat task strips retain their nodes too. Action handlers use
the current projection; pending mutations stay disabled across refreshes.

Task details refresh metadata, results, files and guidance alongside their
incremental execution transcript. Schedule details and run history share the same
background lifecycle as lists. Machine settings expand locally without reads;
generated invitations remain visible until replaced or their membership changes.
Third-party account login status uses the same queue and retains its row during
the bounded login-session status checks.

Desktop Space adapters consume the same backend subscriptions and emit
`pot:changed` through Wails. Responses and changes carry their Space identity;
a failed remote request never falls back to another Control.

## Main conversation streams

`GET /api/v1/events?assistant_id=...` sends authoritative `snapshot` frames.
Subscription captures initial state and then sends current state as execution
updates arrive. The snapshot contains a durable conversation cursor, Runtime
boot identity, messages, active turn and assistant configuration. SSE IDs combine
the boot ID, owner and cursor. Slow subscribers receive the latest projection
without requiring an unbounded token queue.

The browser uses EventSource, closing the old source before switching owners.
Desktop subscribes through Wails `Subscribe(space, owner)` and receives
`pot:snapshot` plus `pot:subscription` connection status. Unsubscribe cancels its
backend context. The native Space adapter reconnects with bounded backoff;
browser reconnect is managed by EventSource. Reconnection starts from an
authoritative snapshot.

Returning to a visible window, restoring a page or regaining network connectivity
also renews the conversation and change subscriptions and refreshes current
projections. Paired focus/visibility events coalesce, and recovery reads do not
overlap. Recovery retains drafts and in-flight send receipts without resending
input; retired stream callbacks cannot overwrite the renewed subscription.

The shared conversation state validates owner/Runtime/boot identity and rejects
older cursors. Navigation generations fence late responses, including A → B → A.
Snapshot refresh retains composer nodes, drafts and reading position. It follows
the bottom only when the reader was already near it; otherwise the latest-message
action remains available. Only a matching request receipt clears a submitted
draft. Connection failure keeps the draft and exposes recovery.
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
The reply contains `cursor`, `has_more` and `unavailable`. When the original
machine is disconnected, cached rows remain readable and are explicitly marked
unavailable; the UI does not silently change execution owner.

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
binding and reference completions. Only its history scrolls; status and the composer
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

Task transcripts reduce the same start/state/output/finish events. Control forwards
run-bound state updates to the original Runner, whose Agent sequences them into
its durable log; destination executors stream output through that same Agent.
Composition children have explicit `parent_id` values scoped by the original turn.
A returned process session or yielded script is distinct from completed execution.

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

Execution, schedule deadlines, authorization expiry, resource transfer and
membership monitoring retain their own backend lifecycle responsibilities.
Animation frames advance only active presentation effects; Reduced Motion makes
activity indicators static and disables avatar/tray animation. These clocks do
not add interval-based UI projection refreshes.

See [Development](development.md) for backend and shared UI verification commands.
