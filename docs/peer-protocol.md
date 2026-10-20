# Peer protocol

`pkg/peer` is Pot's shared native cross-machine transport. It depends only on the
Go standard library. A host supplies its stable Ed25519 identity, private key,
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
           TLS 1.3 / mutual Ed25519 proof / pinned public keys
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

TLS 1.3 encrypts all application traffic, including invitation previews and pair
claims. Each connection receives an in-memory self-signed certificate for its
existing Ed25519 identity key, with the node ID in its subject common name.
Certificates expire after 24 hours; new connections generate fresh certificates.
Certificates are not persisted, and renewal does not change the public-key pin.
No public CA, domain certificate or user-managed certificate file is required.
A VPN can still restrict network access but is not required for confidentiality.

## Handshake and frames

Protocol version 3 runs exclusively over TLS 1.3 with ALPN `pot-peer/3`.
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

After TLS completes, the client sends a bounded JSON hello containing protocol
version, its identity, expected server identity, service name and admission data.
The client identity must exactly match its authenticated TLS certificate. The
server then calls service admission and returns its identity and decision, which
the client also checks against the authenticated certificate. Possessing a key
does not grant service access: first pairing still needs the single-use invitation,
and existing connections still check the saved node/key binding and revocation.
TLS handles transcript authentication, directional keys, integrity and record
replay protection; application packets carry no separate signatures.

Every application handshake packet and frame has a four-byte unsigned big-endian
length followed by JSON inside TLS. The application handshake is limited to
64 KiB, with one ten-second deadline covering TLS and service admission. Frames
carry kind, request ID, per-attempt correlation nonce, method and payload. Each
connection admits one service; a host can dispatch multiple service names through
one `Server` and TCP listener.

Application frames are limited to 32 MiB; RPC JSON payloads to 24 MiB. A session
admits at most 64 pending calls and 64 simultaneous incoming handlers. Servers
bound accepted connections, including pending handshakes, to 128. The limits are
checked before allocating a declared frame or dispatching a handler. TCP handles
network segmentation and TLS handles encrypted record splitting; services do not
implement another transport fragment format.

## Lifecycle and business recovery

`Dial`'s context controls establishment, not the established connection. Use
`DialOptions.Context` or explicitly call `Close` for host-owned session lifetime.
`Server.Serve` owns the listener and sessions until cancellation or listener
failure. `OnSession` registers an authenticated session before its reader starts
and must return promptly. Hosts may register a narrow `identity` service for
authenticated identity probes; management HTTP APIs are not mounted on the peer
listener.

Sessions carry requests, responses and lossy `event.*` notifications in either
direction. Responses preserve the request ID, method and per-attempt correlation
nonce, so a late response cannot complete a later call reusing a business ID. A
handler may return a bounded `RemoteError` with opaque service fault data. Heartbeats run every twenty
seconds, reads expire after ninety seconds, and writes have bounded deadlines.
Readers enqueue bounded heartbeat replies without waiting for writers, so duplex
large writes continue draining.

The transport never reconnects or replays business requests automatically. A call
whose delivery or result is uncertain returns `ErrUncertain`. Agent messaging
may retry immutable messages against durable deduplication and receipts. Local task
execution must reconcile the original run/request receipt before recovery. An
event is only a resynchronizable hint, never an execution receipt or durable ACK.

Browser APIs, local IPC and external account integrations use their own
interfaces and do not acquire peer authority by sharing a process. Codex
collaboration uses the same Agent Link transport.
