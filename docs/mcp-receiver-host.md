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

Local Host commands accept `--listen LOOPBACK_IP:PORT`. Codex defaults to
`127.0.0.1:8792` and Antigravity to `127.0.0.1:8795`. Use an explicit unused port
for an isolated receiver alongside an existing Host, and pass the same value to
connect, serve, disconnect and local management commands for that private root.
Only a loopback IP literal and a nonzero port are accepted; wildcard interfaces,
hostnames and automatic port selection are rejected. The selected address is
carried to spawned daemons and supervised native tools; their root credentials
and active Task/run checks still apply. It is not saved as a new connection or
permission setting.

```sh
pot-codex-agent receiver-connect --root /absolute/private/codex --url https://pot.example/mcp
pot-codex-agent receiver-serve --root /absolute/private/codex --connection CONNECTION_ID
```

For example, add `--listen 127.0.0.1:17756` to both commands for an isolated
Codex receiver; use it again on renewal, disconnect and management commands.

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

Task input is an asynchronous durable mailbox. The queue verifies the original
caller and expected run before enqueue; an active native turn does not cause a
busy rejection. The Host acknowledges each journaled input without interrupting
the turn, then consumes FIFO at the terminal, cleanup-confirmed and
report-reconciled boundary. Each consumed input admits a separate continuation
run. Multiple messages are never collapsed into the latest text; the original
accepted run remains provenance while each continuation binds the actual current
predecessor. Transport ACK, consumed input and execution completion have separate
receipts. Pending messages survive a journal reopen and lost ACK/admission/input
receipt responses reconcile without launching another turn. Cancellation intent
keeps messages pending; confirmed cancellation discards the preceding unconsumed
guidance. Later owner guidance can continue from the confirmed terminal run.

Native submission and terminal continuation exclude retired jobs from capacity
checks. Retired jobs retain their saved bytes and last state; current work still
shares the existing 32-active-job limit, including continuations.

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
uncertain. History, reports, artifacts and minimal unsupported connection summaries are retained; the
new receiver does not consume an old pending mailbox as newly authorized work.

## Driver and execution context boundary

The receiver advertises node Task submission, read, cancellation, terminal input
and lifecycle snapshots. Codex additionally advertises native human review;
Antigravity retains its native permission policy and does not claim human review.
Configured native assistants register their implemented mail capabilities.
Token streaming, live steering and remote file transfer remain unsupported.

Native subprocesses receive the real job/run and their own assistant identity
when processing assistant mail. A directly submitted Task remains a Task actor,
even when its original caller is an OAuth user. Receiver OAuth credentials never
enter native MCP configuration or model arguments.

`execution-tools --root ROOT` connects only to an already-running Host and checks
the persisted Task/run plus the active native execution on every dispatch. Direct
Task workers expose only `pot_execution_context`; they gain no delegation or
cross-node message tools. Assistant message execution additionally exposes typed
sender-free discovery, messaging and Task operations through the authenticated
receiver gateway. Codex injects this service through thread-scoped configuration;
Antigravity retains its mandatory independent execution-support hook and native
read-only policy. Neither path replaces execution binding with a global login.

The service's existing human review UI receives Codex pending requests through
the receiver extension. Decisions are once-only controls bound to the original
caller/request and real execution Task/run. Models cannot approve. Pending native
channels are process-local: restarts invalidate them, and uncertain decision
intents are not replayed. See the shared contract for correlation and fences.

When a native interaction requires local review, the Task error retains
`codex_approval_requires_local_review` with a known App Server protocol method
and rejection reason. Unknown methods are recorded as `unrecognized`; interactive
parameters and request content are omitted. This diagnosis does not enable
additional interaction types. Elicitation diagnostics retain only known mode
enums, field presence and thread/turn association states; they retain no message,
URL, schema contents or credentials. The method alone does not distinguish native
approval from server-initiated elicitation. `pot_execution_context` advertises its
read-only behavior; other tools retain their own annotations and review rules.
Command/file review keeps its existing checks,
MCP elicitation is cancelled, and unsupported requests remain rejected.

Updated Pot and plugins/adapters require explicit MCP reconnection. Old P2P
pairing is unsupported by this path, with no automatic migration or fallback.
The product no longer mounts the old pairing/listener entry points.

## Human management of native assistants

The native receiver retains the existing human-only profile, retirement and
assistant-management catalogue. Pot's authenticated human UI can read/edit
presentation or create/update assistants using the native backend's existing
model choices and owner-registered workspace scope IDs. It cannot add raw paths,
credentials or permission modes through that channel. The optional setup plugin
never supplies this authority.

The receiver journals human operations before entering the existing backend.
Revision, busy, unknown-cleanup, immutable workspace and idempotent management
receipt checks still apply. Unknown effects remain fenced after restart; completed
responses can be reconciled. Retired assistant tombstones prevent stale directory
registrations from reviving an identity. Old native jobs/messages remain saved at
their last confirmed state and are excluded from execution recovery.
