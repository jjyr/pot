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
version baseline. Usage-only updates do not reload the assistant directory.
Mutations publish after persistence; no-op reads do not produce invalidations.
The global subscription remains active even when no assistant exists, so an
assistant created by another client appears without reloading the page.

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

The shared conversation state validates owner/Runtime/boot identity and rejects
older cursors. Navigation generations fence late responses, including A → B → A.
Snapshot refresh retains composer nodes, drafts and reading position. It follows
the bottom only when the reader was already near it; otherwise the latest-message
action remains available. Only a matching request receipt clears a submitted
draft. Connection failure keeps the draft and exposes recovery.

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

## Lifecycle and remaining clocks

SSE keepalive comments run every 15 seconds; writes have bounded deadlines.
Backend shutdown or request cancellation closes subscriptions. Browser logout
revokes access and reloads into authentication. Window/client disposal removes
listeners and releases subscriptions.

Execution, schedule deadlines, authorization expiry, resource transfer and
membership monitoring retain their own backend lifecycle responsibilities.
Animation frames advance only active presentation effects; Reduced Motion makes
activity indicators static and disables avatar/tray animation. These clocks do
not add interval-based UI projection refreshes.

See [Development](development.md) for backend and shared UI verification commands.
