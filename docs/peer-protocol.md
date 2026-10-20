# Peer protocol

`pkg/peer` is Pot's shared native cross-machine transport. TLS uses Go's standard
library; CBOR, Yamux and Zstd use dedicated libraries. A host supplies its stable
Ed25519 identity, private key,
service admission function and request handlers. It does not depend on Control,
machine membership, assistant databases or plugin management.

```text
Agent Link collaboration          Authenticated identity probes
 routes / durable ACK               identity proof
          |                              |
          +------------------------------+
                         |
                         pkg/peer Session
           service admission / RPC / events / connection lifetime
           CBOR metadata + opaque body / optional per-message Zstd
           Yamux streams / flow control / bounded receive windows
           TLS 1.3 / mutual Ed25519 proof / pinned public keys
                                |
                               TCP
                                |
                    public / private network / VPN
```

## Endpoints and identity

Endpoints are `tcp://host:port`, without a path, query, credentials or fragment.
Dialing accepts public unicast IPv4/IPv6, loopback, private LAN and Tailscale
addresses. Unspecified, multicast and link-local addresses are rejected. A DNS
name must resolve entirely to permitted addresses. Each
connection resolves again and dials a validated numeric address; HTTP proxy
environment variables have no effect. A listener's wildcard bind address is not
a publishable endpoint.

`Identity` contains `{node_id, public_key}`: a stable logical endpoint ID and
base64url Ed25519 public key. Multiple logical nodes may share one machine. A copied
invitation binds the expected identity to the endpoint. Discovery without a pin
proves possession of the discovered key but does not establish trust. Service
admission must independently validate the invitation or saved binding. A key
authorizes no assistants, membership, execution methods or owner APIs by itself.

TLS 1.3 encrypts all application traffic, including invitation previews and pair
claims. Each connection receives an in-memory self-signed certificate for its
existing Ed25519 identity key, with the node ID in its subject common name.
Certificates expire after 24 hours; new connections generate fresh certificates.
Certificates are not persisted, and renewal does not change the public-key pin.
No public CA, domain certificate or user-managed certificate file is required.
A VPN can still restrict network access but is not required for confidentiality.

## Handshake and frames

Protocol version 4 runs exclusively over TLS 1.3 with ALPN `pot-peer/4`.
Both peers present a self-signed Ed25519 certificate and TLS proves possession of
both private keys. Session resumption is disabled, so every connection performs a
fresh full handshake. Verification checks the exact expected node ID and public
key, certificate validity, digital-signature usage and peer authentication usage.
Public-key pins survive certificate replacement; a copied certificate or a copied
public-key suffix on an unrelated private seed cannot authenticate a node.
There is no plaintext, older TLS or former signed-protocol fallback. Both peers
must upgrade; existing invitation and connection formats and key bindings remain
unchanged.

Without an expected identity, `Dial` permits only the `identity` service with null
admission data and no reverse handler. Such discovery proves key possession, not
trust, and must never transmit a capability. Pinned callers verify TLS before
sending any application identity, service admission data or invitation capability.

After TLS completes, the client sends a bounded CBOR hello containing protocol
version, its identity, expected server identity, service name and admission data.
The client identity must exactly match its authenticated TLS certificate. The
server then calls service admission and returns its identity and decision, which
the client also checks against the authenticated certificate. Possessing a key
does not grant service access: first pairing still needs the single-use invitation,
and existing connections still check the saved node/key binding and revocation.
TLS handles transcript authentication, directional keys, integrity and record
replay protection; application packets carry no separate signatures.

The hello and acceptance each have a four-byte unsigned big-endian length
followed by CBOR inside TLS, limited to 64 KiB. One ten-second deadline covers TLS
and service admission. Admission data remains the service's JSON, encoded as a
CBOR byte string. Each connection admits one service; a host can dispatch multiple
service names through one `Server` and TCP listener.

After acceptance both peers start Yamux over the established TLS connection.
Each RPC attempt opens one bidirectional stream. One request travels forward and
one response travels back on that same stream. Events use separate streams with
no response. The sender half-closes after its message; a receiver requires FIN
immediately after the declared body before accepting the message.

```text
TCP connection
  TLS 1.3: full mutual identity proof, expected-key pin, service admission
    Yamux
      stream 1: request -----------------> response <-----------------
      stream 3: request -----------------> response <-----------------
      stream 2: <---------------- request -----------------> response
      stream 5: event -------------------> FIN

Each stream direction carries exactly one message:
+-----------------------+----------------------+---------------------+-----+
| CBOR length: 4B, BE    | CBOR metadata        | body: size bytes    | FIN |
+-----------------------+----------------------+---------------------+-----+
                         kind, id, method,      opaque service bytes
                         size, raw_size,
                         encoding?, error?
```

CBOR describes routing and body encoding; it does not re-serialize the body.
The current `Request`/`Handler` API still accepts and returns JSON documents,
which travel as their original bytes. Response metadata repeats the request ID
and method. The stream identifies the attempt, while a business ID may be reused
for durable receipt reconciliation. There is no extra correlation nonce.

Metadata uses canonical CBOR maps with text keys. Decoding rejects duplicate or
unknown fields, tags, indefinite lengths, trailing CBOR items and excessive
nesting or container counts. Metadata is limited to 32 KiB, nesting to eight,
and maps/arrays to sixteen entries. Invalid body encodings, truncated bodies,
trailing stream bytes and mismatched responses are rejected.

Encoding 0 (the default) sends the body unchanged. Encoding 1 sends an independent
Zstd frame using the fastest compression level and a 1 MiB window. Bodies below
4 KiB are never compressed; larger bodies use Zstd only when it saves at least
10%. The hello, admission credentials and metadata are never compressed, and
compression shares no history or dictionaries across messages. Compression can
still reveal information through the length of a single mixed secret/attacker
controlled body; services must not expose such bodies as a compression oracle.
Compressed wire bytes and declared raw bytes are each limited to 24 MiB. The
decoder also bounds its window, memory and output capacity, and requires the exact
declared raw size. These limits are checked before allocating body buffers.

A session admits at most 64 pending outgoing calls/events and 64 simultaneous
incoming handlers. Concurrent outgoing IDs must be unique. Resetting a stream
does not free a running business handler's admission slot. Yamux allows at most
64 incoming streams, starts with 256 KiB receive windows and grows them up to
4 MiB when RTT and memory reservations permit. It sends data in chunks of at
most 64 KiB. An unread bulk stream therefore does not block unrelated
control streams at the application layer; all streams still share TCP's ordering
and congestion control.

Receive windows and transport-owned body/compression-output buffers share a
96 MiB per-session and 256 MiB process-wide reservation budget. Reservations fail
before allocation and never wait for other streams to free memory. These are
transport buffer budgets, not process heap limits: caller-owned payloads/results,
handler memory, bounded headers, TLS/Yamux overhead and shared codec workspaces
are separate. Resource exhaustion can reject a stream or close its session.
Servers also bound accepted connections, including pending handshakes, to 128.
TCP, TLS and Yamux handle segmentation; services implement no fragment protocol.

## Lifecycle and business recovery

`Dial`'s context controls establishment, not the established connection. Use
`DialOptions.Context` or explicitly call `Close` for host-owned session lifetime.
`Server.Serve` owns the listener and sessions until cancellation or listener
failure. `OnSession` registers an authenticated session before its reader starts
and must return promptly. Hosts may register a narrow `identity` service for
authenticated identity probes; management HTTP APIs are not mounted on the peer
listener.

Sessions carry requests, responses and lossy `event.*` notifications in either
direction. A late response belongs only to its original stream, even when the
next attempt reuses the same business ID. A handler may return a bounded
`RemoteError` with opaque service fault data.

Yamux keepalives and RTT measurements run every twenty seconds. Message reads
expire after ninety seconds and writes after thirty seconds, with earlier caller
deadlines taking precedence. Cancelling a caller's wait resets only its stream;
it does not cancel an already dispatched business operation. Handlers receive the
session context and own durable cancellation through their service API. Malformed
application messages and handler overload reset their stream. TLS authentication
failures, invalid Yamux traffic and connection failures close the session.

The transport never reconnects or replays business requests automatically. A call
whose delivery or result is uncertain returns `ErrUncertain`. Agent messaging
may retry immutable messages against durable deduplication and receipts. Local task
execution must reconcile the original run/request receipt before recovery. An
event is only a resynchronizable hint, never an execution receipt or durable ACK.

Browser APIs, local IPC and external account integrations use their own
interfaces and do not acquire peer authority by sharing a process. Codex
collaboration uses the same Agent Link transport.
