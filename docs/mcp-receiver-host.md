# MCP receiver Host

The native Agent Host can explicitly participate as a receiver in Pot's
[MCP collaboration contract](mcp-collaboration-contract.md). Ordinary MCP clients
configure their own Pot URL and do not start this Host. The receiver connects
outbound using the standard SDK Streamable HTTP transport. It does not start a
peer listener, use P2P pairing, or fall back to the old transport.

```text
Caller -> Pot MCP queue -> claimed Work/Control -> outbound receiver Host
                                                    |
                         receiver SQLite journal + existing backend admission
                                                    |
                         existing native execution and cleanup supervision
                                                    |
Caller <- Pot Task/run projection <- durable event outbox + exact receipt
```

## Explicit operation

Install the receiver executable separately from the optional MCP setup helper.
Use a private absolute root for its native runtime. The same root cannot be
operated by another Host or receiver process. Codex and Antigravity use separate
roots and keep their own native login, model, workspace and permission settings.

```sh
pot-codex-agent receiver-connect --root /absolute/private/codex --url https://pot.example/mcp
pot-codex-agent receiver-serve --root /absolute/private/codex --connection CONNECTION_ID
```

Replace `pot-codex-agent` with `pot-antigravity-agent` for Antigravity. The URL is
the deployer's actual Pot MCP resource. Deployers manage HTTP/HTTPS, certificates
and reachability. There is no certificate or network configuration service.

`receiver-connect` uses public OAuth client registration and authorization code
with S256 PKCE. The browser confirms this receiver node separately from ordinary
caller access. The resulting JSON contains only the logical connection ID. The
receiver token and public client ID are stored in its own encrypted secrets
store; native-client authentication files are not imported. The access lifetime
is supplied by Pot; there is no refresh-token flow.

Renew authorization for the existing logical connection with:

```sh
pot-codex-agent receiver-connect --root /absolute/private/codex --connection CONNECTION_ID
```

This reuses the saved public OAuth client, requires the same Pot URL and receiver
node, and opens a new PKCE authorization. A running receiver accepts the renewed
grant through its local owner interface, so renewal does not stop native jobs.
The authorization server permits ephemeral IP-loopback callback port changes;
the code exchange remains bound to the exact callback selected for that request.
A revoked connection cannot be resurrected through renewal.

`receiver-serve` runs in the foreground. It starts the existing local management
HTTP interface and native execution supervision, then registers the driver's
actual node capabilities. The local interface retains owner authentication;
the receiver token is never a local owner credential. Transport reconnect uses
the same connection and receiver node rather than the SDK session ID.

To pause communication while keeping native execution alive:

```sh
pot-codex-agent receiver-disconnect --root /absolute/private/codex --connection CONNECTION_ID
```

The command contacts an already-running local Host when available. It records a
disconnected connection without cancelling Tasks. Reauthorizing that connection
reactivates its outbound loop. Revocation or authorization expiry likewise stops
admission/reporting, retaining accepted execution and its journal. Stopping the
Host process has its existing native shutdown/cleanup semantics and is a separate
operation from disconnecting.

## Admission, reports and recovery

The receiver saves a claim and immutable delivery before acknowledging it.
Input/cancel rejection is saved before its first acknowledgement. An accepted
work acknowledgement can precede creation of the execution run. The backend's
job/session or terminal continuation and receiver execution mapping commit in
one Tasks transaction. The real Task/run comes from that backend record; the
origin queue Task and execution Task may have different IDs and nodes.

The backend records native launch intent before starting a process. Continuation
checks the original caller, Task and old run inside the same transaction that
creates the new run and mapping. It requires a terminal previous run, confirmed
cleanup and its reconciled terminal event. A stale scheduler selection cannot
launch a newer run. Input does not advertise live steering.

Each claim has a single pending event outbox. The event is saved before its
network call; its claim, sequence and canonical digest must match the returned
receipt. A lost response repeats that same event, not native execution. The next
sequence is allocated only after its predecessor's receipt commits. Cancellation
is intent until the execution state provides terminal evidence; uncertain cleanup
is never reported as successful cancellation.

The pending claim request ID is persisted before polling and cleared only after
the delivery is journaled, or an empty poll completes. A crash between the Tasks
and Control writes retrieves and reconciles the same claim. Claims have no
receiver-managed expiry or automatic reassignment. Revision CAS rejects stale
journal writers; the existing private-root process lock prevents two operated
pumps from owning a native execution queue.

On a process restart, the existing backend fences unfinished native work. Queued
work becomes a known pre-execution failure; previously running work becomes a
failure with uncertain cleanup. Neither is automatically launched again. Pending
acknowledgements, admissions and events are reconciled under their original
claim/Task/run. Explicit terminal continuation remains blocked when cleanup is
uncertain. History, reports, artifacts and old pairing records are retained; the
new receiver does not consume an old pending mailbox as newly authorized work.

## Driver and execution context boundary

The current receiver advertisement includes node Task submission, read,
cancellation, terminal input and lifecycle event snapshots for Codex/Antigravity.
It does not advertise native token streaming, live input, file transfer, assistant
mail, or remote approval management. Existing native review and assistant mail
code remains for its existing callers; connecting those business paths to MCP
requires their own complete trusted routing. This receiver path alone does not
complete the MCP cutover.

Native subprocesses receive the actual job/run and their own assistant identity
when applicable. A direct Task executes as that receiver-owned Task actor, even
when its original caller is an OAuth user. The original caller does not become
the execution actor or an owner. Receiver OAuth credentials are not injected into
native MCP configuration or model tools.

`execution-tools --root ROOT` connects only to an already-running local Host;
it never starts one. It requires trusted job/run/root environment and currently
exposes only `pot_execution_context`. Each call checks the persisted run and
active native execution. Arguments cannot supply an actor or binding. The tool
grants no approval authority. Codex can receive this context server through
thread-scoped native configuration; Antigravity's execution-support package
retains its required policy hook. This context tool is not an implemented remote
message or task-delegation callback. An ordinary global MCP login cannot replace
that missing trusted execution gateway.

Updated Pot and plugins/adapters require explicit MCP reconnection. Old P2P
pairing is unsupported by this path, with no automatic migration or fallback.
The coordinated cutover owns final deletion of obsolete entry points.
