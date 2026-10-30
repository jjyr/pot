# UI Transport (pot.v1)

## Overview

UI↔backend business traffic uses one WebSocket per browser/WebView host endpoint at `/api/v1/ws`. The socket carries business request/response and all subscriptions multiplexed on subprotocol `pot.v1`. There is no per-feature socket: changing the active assistant unsubscribes that subscription and retains the socket.

The developer preview uses the same envelopes at `/api/preview/ws`. Upgrade the
host and its UI together: the former browser JSON/SSE routes and native business
bindings are removed. Browser installations must activate the current UI shell;
an old cached shell has no legacy transport fallback.

```
Browser/WebView UI (JS)
  │  ws-client.mjs (one instance per host endpoint)
  │    ├─ request/response (id-keyed)
  │    ├─ subscriptions (id-keyed)
  │    └─ heartbeat / liveness
  │
  │  WebSocket  /api/v1/ws   Sec-WebSocket-Protocol: pot.v1, pot-token.<token>
  ▼
Backend transport layer
  ├─ Browser cookie same-origin auth + per-request allowlist
  ├─ Native Transport: ephemeral loopback URL + token, known Wails/null origin
  └─ Session lifecycle: logout/expiry/rotation close socket
```

The native `Transport` binding provides an ephemeral loopback URL and token. JS offers `["pot.v1","pot-token.<token>"]`; the server negotiates `pot.v1` only and additionally requires a valid token and a known Wails or null origin. Browser clients authenticate by same-origin cookie; each requested operation also passes the browser allowlist. Session logout, expiry, or rotation closes the socket.

## Wire Protocol (JSON)

All frames are JSON with a `type`. Request IDs are transport-transient, never business idempotency keys.

| Direction | Frame |
|---|---|
| client→server | `{type:"request",id,method,path,body,expected_control_id}` |
| server→client | `{type:"response",id,status,result,error}` |
| client→server | `{type:"subscribe",id,path,expected_control_id}` |
| server→client | `{type:"event",id,event,event_id,data}` |
| server→client | `{type:"end",id,status,error}` |
| client→server | `{type:"cancel",id}` / `{type:"unsubscribe",id}` |

A successful `response` (the ACK) precedes any `event` for that subscription ID. Response `error` is an `APIError` with `code` and `message`. Change events carry `event_id` as a `boot:sequence` pair; conversation events retain their owner and cursor identity. New subscriptions receive an initial reset/map. Conversation subscriptions retain cursor/reset/delta semantics.

Heartbeats signal liveness only. Frames, in-flight requests, and write deadlines are bounded; each request supports per-request cancel.

## Disconnect and Reconnect

On disconnect, pending calls are rejected. Sent calls have unknown outcomes; calls that never reached the socket are marked unsent. Mutations are never automatically replayed. Reconnect resubscribes and fetches fresh snapshots, then revalidates runtime identity and permissions before re-enabling operations. Chat uses a durable outbox with request identity to reconcile unknown sends.

Subscription ACK and heartbeat do not imply business readiness; a valid initial snapshot or change map is required. In production, persisted projections await IndexedDB commit before being treated as ready; preview has no IndexedDB.

## Scope Boundaries

- Browser UI JSON HTTP business endpoints are removed. HTTP remains for auth (login/status/logout), binaries, and static assets.
- Private operator HTTP for CLI/host remains.
- Provider model and standard MCP SSE traffic are unaffected.
- Native bindings retain windows, tray, dialogs, notifications, attention preferences, Transport, and download integrations.
- The protocol separates the frontend from the backend implementation language.
