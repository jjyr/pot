# Peer protocol

`pkg/peer` is Pot's shared native cross-machine transport. It depends only on the
Go standard library. A host supplies its stable Ed25519 identity, private key,
service admission function and request handlers. It does not depend on Control,
machine membership, assistant databases or plugin management.

```text
Agent messages          Runner execution          Other native services
routes / durable ACK    membership / run receipts  service-specific authority
       |                        |                           |
       +------------------------+---------------------------+
                                |
                         pkg/peer Session
           service admission / RPC / events / connection lifetime
           signed session + direction + sequence / bounded framing
                                |
                               TCP
                                |
                       private network / VPN
```

## Endpoints and identity

Endpoints are `tcp://host:port`, without a path, query, credentials or fragment.
Dialing accepts loopback, RFC 1918 IPv4, IPv6 ULA and Tailscale's
`100.64.0.0/10`. A DNS name must resolve entirely to permitted addresses. Each
connection resolves again and dials a validated numeric address; HTTP proxy
environment variables have no effect. A listener's wildcard bind address is not
a publishable endpoint.

`Identity` contains `{node_id, public_key}`: a stable logical endpoint ID and
base64url Ed25519 public key. Multiple logical nodes may share one machine. A copied
invitation binds the expected identity to the endpoint. Discovery without a pin
proves possession of the discovered key but does not establish trust. Service
admission must independently validate the invitation or saved binding. A key
authorizes no assistants, membership, execution methods or owner APIs by itself.

The protocol does not encrypt traffic. Ed25519 authenticates identities and
protects integrity; Tailscale or another encrypted VPN can provide network
confidentiality without application certificates. This protocol is not Noise.

## Handshake and frames

Protocol version 2 rejects the former identity `id` field and version 1 hello.
Every packet has a four-byte unsigned big-endian length followed by JSON.
Signed packets contain an exact JSON payload and base64url signature over a
domain separator and that payload. The client hello binds protocol version,
identity, random 32-byte nonce, expected server identity, service name and
service admission data. The signed server challenge binds a fresh nonce to the
entire signed hello. The client signs a confirmation of the combined transcript.
Only after checking that confirmation does the server call the service's
admission function. A signed acceptance binds its decision to the same transcript.
Handshake packets are limited to 64 KiB and have a ten-second deadline.

The transcript hash identifies the session. Every application frame signs that
session ID, its client/server direction, a strictly increasing per-direction
sequence, message kind, request ID, per-attempt correlation nonce, method and payload. A modified signature,
wrong direction, skipped/repeated sequence or replay from another connection
closes the session before dispatch. Each connection admits one service; a host
can dispatch multiple service names through one `Server` and TCP listener.

Application frames are limited to 32 MiB; RPC JSON payloads to 24 MiB. A session
admits at most 64 pending calls and 64 simultaneous incoming handlers. Servers
bound accepted connections, including pending handshakes, to 128. The limits are
checked before allocating a declared frame or dispatching a handler. TCP handles
network segmentation; services do not implement another transport fragment format.

## Lifecycle and business recovery

`Dial`'s context controls establishment, not the established connection. Use
`DialOptions.Context` or explicitly call `Close` for host-owned session lifetime.
`Server.Serve` owns the listener and sessions until cancellation or listener
failure. `OnSession` registers an authenticated session before its reader starts
and must return promptly. Hosts may register a narrow `identity` service for
signed identity probes; management HTTP APIs are not mounted on the peer listener.

Sessions carry requests, responses and lossy `event.*` notifications in either
direction. Responses preserve the request ID, method and per-attempt correlation nonce, so a late response cannot complete a later call reusing a business ID. A handler may return a
bounded `RemoteError` with opaque service fault data. Heartbeats run every twenty
seconds, reads expire after ninety seconds, and writes have bounded deadlines. Readers enqueue bounded heartbeat replies without waiting for writers, so duplex large writes continue draining.

The transport never reconnects or replays business requests automatically. A call
whose delivery or result is uncertain returns `ErrUncertain`. Agent messaging
may retry immutable messages against durable deduplication and receipts. Runner
execution must reconcile the original run/request receipt before recovery. An
event is only a resynchronizable hint, never an execution receipt or durable ACK.

Browser APIs, local IPC, external account integrations and Codex MCP use their
own interfaces. They do not acquire peer authority by sharing a process.
