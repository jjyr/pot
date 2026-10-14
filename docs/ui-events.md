# Event-driven UI updates

Native App and Web load initial projections, subscribe to backend changes, and
fetch invalidated domains. Healthy clients do not refresh snapshots or lists on
an interval. Mouse/keyboard events, native geometry/appearance notifications,
request pending/completion states and backend subscriptions wake presentation.
Conversation text uses streamed deltas and the same `pot-ui::state::apply_event`
reducer in native Rust and Web/WASM.

## Control invalidation stream

`GET /api/v1/changes` requires operator authorization. Browser Access exposes this
read-only route through its authenticated proxy; model tools do not expose it.
SSE `changes` frames contain `runtime_id`, `boot_id` and a `versions` map. Keys are
`machines`, `resources`, `permissions`, `assistants`, `mail`, or assistant-scoped
`tasks/{assistant_id}`, `schedules/{assistant_id}`, `usage/{assistant_id}`,
`settings/{assistant_id}` and `conversation/{assistant_id}`. Server-level writes
may invalidate an entire domain using its unscoped key.

The first frame is sent immediately, even with an empty versions map. Subsequent
frames carry the latest complete map. Clients compare revisions, merge dirty
domains and fetch the affected projections. Slow readers coalesce changes rather
than accumulate an unbounded event queue. These versions are process-local
invalidation markers, not durable conversation cursors. A new runtime/boot or a
reconnected stream causes a full refresh. No-change reads and membership timestamp
updates do not produce UI invalidations.

Mutations publish after their commit. Conversation lifecycle changes invalidate
summaries; each streaming token does not refetch task lists or usage. Task
persistence compares stored content before invalidation. Permission, resource,
assistant, mail and configuration mutations also publish from their owning
services, including pending configuration becoming active at a turn boundary.

## Conversation streams

`GET /api/v1/assistants/{owner}/events?snapshot=true` subscribes and captures an
authoritative snapshot under the conversation lock, skips historical replay and
then delivers live events. Existing durable replay callers can use `after`.
`GET /api/v1/assistants/{owner}/tasks/{task_id}/events` supplies task snapshots and
live events using the public task conversation identity. Queue changes produce a
new snapshot and replace the receiver atomically so older ephemeral events cannot
follow a newer snapshot.

Native and Web reconnect from snapshots, reject wrong conversation identities,
ignore duplicate durable events and reconnect on cursor gaps. Navigation generation
checks reject late subscriptions after owner/task changes, including A → B → A.
Projection reads cannot replace a newer streamed snapshot. In-process streams use
the same framing and authorization contract as HTTP without a loopback UI socket.
Frames are bounded to 8 MiB; fragmented UTF-8, CRLF and multiline data are supported.

Subscriptions are cancelled when their native owner is dropped, when browser
navigation changes conversation, or when Web is hidden/logged out. Web resubscribes
on visibility/online recovery. Backend shutdown closes change streams. Native
Control reconfiguration interrupts subscriptions so they bind to the new endpoint.
Reconnect uses bounded backoff; HTTP/browser stream inactivity is bounded. Browser
authentication rejection returns to Token configuration instead of retrying forever.
Access continues validating revocation during an open stream.

## Timers that remain

- SSE keepalive comments every five seconds and connection inactivity deadlines
  detect broken transports. Failed requests and reconnects use retry delays.
- Animations advance while active; the native tray animation stops when idle or
  Reduced Motion is enabled. There is no 200 ms native snapshot fallback.
- Push visibility heartbeats and server authorization/liveness checks remain.
- Sandbox providers and Runner membership expose status queries, not a complete
  lifecycle stream. An independent backend monitor checks every three seconds
  only while a visible Machines/Sandbox page holds an authenticated
  `GET /api/v1/machines/watch` subscription. Opening the page starts an immediate
  asynchronous check; checks have a bounded timeout and publish material changes
  through the existing `machines` invalidation topic. UI rendering never waits for
  the monitor and the watch callback does not mutate presentation state.
  Multiple pages share one monitor per backend. Leaving/hiding the page, logout,
  disconnect or token revocation releases interest; the last subscriber cancels
  both the active check and retry interval. Ordinary `/changes` subscriptions do
  not activate checks. Normal mutations continue publishing events on every page.
  External crashes/offline changes are actively reconciled only while the relevant
  page is visible; other pages retain their last known machine projection.

The migration removes UI data polling; it does not claim every external provider
has push-based lifecycle reporting. Calendar/schedule execution and allowance
window clocks also retain their backend scheduling responsibilities.

## Verification

Run the normal backend/native checks in [Development](development.md), plus
`node --test scripts/tests/ui-events.mjs`. Tests cover bounded/coalesced invalidation,
snapshot/replay ordering, task queue snapshots, local/HTTP streams, authentication,
no read-triggered notifications, shutdown, shared reducer identity/gaps/duplicates,
fragmented browser streams, idle subscriptions and cancellation/reconnect.
These deterministic checks do not measure physical-device input latency or certify
visual appearance; native/Web interactive qualification remains separate.


### Data-local loading

Page navigation, Settings tabs, machine inventory chrome and Usage range controls
are independent of request completion. Usage renders its summary and chart shell
with empty values before the first matching snapshot; only those data regions
report loading. Native aggregate usage excludes snapshots for a different query,
and pending snapshots do not become error notices. Actual transport failures keep
inline errors and retry actions. Visual changes use existing invalidations and
local interaction state, without adding polling or a global loading gate.
