# MCP collaboration contract C0

`pkg/agentlink/mcp_contract.go` and `mcp_receiver.go` define the minimal shared
contract for ordinary MCP callers, receiver Hosts and Pot-to-Pot collaboration.
These are library types and validators. They do not install an MCP listener,
authentication server, queue, execution driver or persistence schema. Existing
production Agent Link transport remains as documented in [Agent Link](agent-links.md)
until its coordinated replacement. [MCP](mcp.md) owns the standard SDK boundary.

```text
Ordinary MCP client ---- configured Pot URL ---- authenticated gateway
                                                       |
                                         existing Agent Link business APIs
                                                       |
Receiver Host -------- initiates MCP connection --------+
  register -> addressed work/control -> claim -> durable ack
  existing Host launch/recovery/cleanup -> driver -> event/result receipt
```

MCP standardizes transport and tool calls. Receiver coordination is a Pot
extension, versioned by `MCPContractVersion` independently of MCP negotiation.
Notifications are hints to poll; they do not prove model wakeup or processing.
Neither a second reachable receiver endpoint nor a local Host is required for
ordinary tool calls. Schedulable execution requires the shared receiver Host.

## Authentication and trusted metadata

The connection flow uses standard OAuth authorization code with PKCE, existing
Pot login and connection confirmation. Authorization is at connection level and
uses the existing peer/caller collaboration scope. C0 adds no OAuth wire fields,
credential format, registration policy, refresh policy, role editor or per-task
credential system. The authentication implementation must supply the mechanisms
required by its supported clients before claiming client interoperability.
HTTP/HTTPS, certificates and endpoint reachability belong to the deployer.

`MCPCaller` and `MCPReceiver` have private fields. Their constructors validate
identity shape; the authenticated gateway must verify credentials, consent,
revocation and connection ownership on every request. Never decode a constructor's
arguments from model tool input or treat an SDK session lookup as authentication.

| Context | Meaning |
|---|---|
| `NewMCPCaller` | A provisioned ordinary user-kind caller, with no owner authority |
| `NewMCPNodeCaller` | An authenticated node representing its own assistant/task |
| `MCPExecutionBinding` | Execution boundary verified active actor, Task and run |
| `NewMCPReceiver` | Independently authorized receiver participation for a node |

A node cannot assert user/runtime/owner identities or another node's actors.
Task actors require an execution binding. Assistant actors may use existing node
trust for peer correspondence; calls from a supervised execution must additionally
bind the actual active Task/run. The boundary checks liveness again at dispatch.
Ordinary OAuth login cannot supply that execution binding. Credential rotation
retains the same logical connection and caller identity.

Receiver registration uses `MCPReceiverRegistration`, one concrete node or
assistant endpoint per registration. It retains `Identity` and `Description`
semantics without changing persisted `Directory` JSON. `ValidateAgainst` checks
the authenticated node, version, peer/caller scope, unique operations, and the
driver's actual effective description. Actual capabilities must come from the
execution implementation. Registration alone cannot prove support. Unknown
operations, owner scope, approval management and configuration are rejected.
Advertising files/events/input requires their real implementation. Input means
terminal continuation; it does not claim live steering or native resume.

## Tool input and admission

`MCPSubmitInput`, `MCPTaskInput` and `MCPCancelInput` are separate typed inputs.
They contain explicit destinations or resource IDs and request IDs, never `from`,
owner, connection selector, execution policy, workspace or approval metadata.
`Submit.Bind` injects the authenticated actor into the existing `TaskRequest`.
`Input.Guidance` produces the existing `TaskGuidance` only for the expected run
in a terminal state. It accepts both existing `complete` and Host `completed`
receipts; it creates no new business phase. Existing cleanup fences still apply.

Before a mutation, the gateway resolves a saved exact retry, then checks current
authorization, original caller ownership, target capability and admission. Input
and cancel require an atomic expected-run check against the owned Task. The
`Task` passed to `Guidance` must already have been loaded under that caller's
ownership; shape validation is not an ownership lookup. The legacy Backend's
run-less Input/Cancel methods require the gateway/Host to enforce this fence in
the same serialized admission path. A separate read followed by an unfenced
mutation is insufficient.

Messages continue to use `protocol.SendMail`, `Envelope` and `Receipt`. The gateway
constructs the envelope with the authenticated sender and the bound processing
parent's conversation/reply/hops. It retains the immutable envelope on retry;
models cannot supply provenance, owner guidance or human approval. C0 does not
add a universal operation/JSON dispatcher or expose `Port.Call` as a remote tool.

## Delivery, execution and correlation

`MCPWorkDelivery` contains the existing authenticated `TaskRequest` plus a stable
`MCPDispatchBinding`: the queue's business Task address and concrete receiver.
There is no run prerequisite for delivery, claim or durable acceptance ack.
`MCPControlDelivery` contains exactly one typed input or cancel, original caller,
dispatch binding and the currently expected real execution binding. These are
internal receiver payloads, not tool inputs. The queue verifies caller ownership
before constructing them. Work rejects owner configuration and caller-selected
workspace settings.

After durable backend admission, `MCPRunAdmission` binds that claim to a real
`MCPRunBinding`. Its Task address belongs to the execution-side node; its ID may
differ from the dispatch Task. Preserve both through the stored mapping. A
submission receipt with no `Task.RunID` may still be delivered, claimed and
accepted, but cannot report execution events until the real run is known and its
admission mapping is committed. Do not invent a run from a task ID or native turn.
An accepted ack does not imply a run already exists.

For first execution, `PreviousRunID` is absent. For terminal continuation, the
input targets the prior run, gets its own delivery/claim, and admission compares
`PreviousRunID` with the stored prior execution before binding a distinct new run.
The execution Task and receiver remain the same. Commit this comparison with the
mapping; reconcile exact duplicate admissions before checking a new transition.
The Host supplies the actual backend Task/run, not a caller-selected replacement.
Cancellation creates no new execution run: its ack records intent, and terminal
evidence is reported under the executing claim.

The queue commits `MCPClaim` before returning it. `ValidateAgainst` compares the
exact stored claim, logical connection, delivery, dispatch Task and receiver against
the authenticated receiver context. Do not compare a client-supplied claim with
another client-supplied value. Connection/node mismatches are unauthorized;
superseded claims and resource bindings are fenced.

`MCPDeliveryAck` records durable receiver acceptance or rejection. It is distinct
from queue delivery, claim allocation, execution start and completion. The Host
must journal acceptance and native launch intent using its existing recovery
and cleanup fences before dispatch. An identical ack is reconciled from storage;
a conflicting ack is rejected. A claim timeout, disconnect, lost response or
missing heartbeat never authorizes execution replay or reassignment.

`MCPExecutionEvent` uses existing `protocol.TaskPhase` values. Its sequence is
monotonic within the exact claim. Terminal phases require the existing
`TaskResultEvent`, matching execution Task, run and phase. `ThreadID` records the native
thread/session without replacing the business identity. Results, evidence and
artifact references retain that Task/run and execution node mapping. Paths do
not grant remote reads; file transfer is separately capability checked.

For reports, authenticate and verify the current claim and admitted run first,
reconcile saved claim/sequence/digest duplicates, then call `ValidateAfter` with
the stored execution binding for a new event. Commit
the event receipt, business projection and notifications atomically where they
share storage. Existing expected-run CAS and business transition validation must
also reject terminal regressions or events from an unaccepted delivery.
`ValidateAfter` checks association and sequence; it is not another task engine.
`MCPEventReceipt.Retry` returns a duplicate only for the same canonical event;
conflicting sequence reuse fails. Resending a report does not rerun work.

Cancellation remains intent until execution-side terminal and cleanup evidence
confirm it. Disconnect/revocation stops admission and communication while retaining
accepted work and history. Unknown execution effects require reconciliation.

## Bounds, strictness and idempotency

Use `DecodeMCPContract` and bound the HTTP body before decoding. It uses the
shared strict decoder: duplicate/unknown fields, null non-pointer values,
missing required fields, invalid UTF-8 and trailing JSON are rejected. Run
context-specific validators after decoding. Do not use ordinary `json.Unmarshal`
as admission validation. Encoder schemas must likewise reject additional fields.

| Value | Bound |
|---|---|
| Identifiers | Existing `core.Identifier` / `AgentRef` validation, at most 128 bytes |
| Submit / message text, result error | Existing 65,536-byte bound |
| Task input text | Existing Host 24,000-byte bound |
| Terminal result text | Existing Agent Link 1 MiB bound |
| Entire extension payload | Existing `peer.MaxPayload`, 24 MiB, including artifacts/evidence |
| Native thread/session ID | 4,096 bytes |
| Capability operations | Existing Directory limit of 64, unique and supported |
| Event sequence | Positive JavaScript-safe integer |

Mutation `Digest` methods hash `core.CanonicalJSON` using SHA-256. The exact
preimage is `{version, connection_id, actor, execution, operation, input}`;
`execution` is explicitly null when absent. Typed input includes every accepted
field, including request ID and expected run. Connection and effective actor
scope request ownership; credential bytes and transient SDK session IDs do not
participate. The golden fixture `pkg/agentlink/testdata/mcp_c0.json` freezes the
canonical preimage, digest and correlated receiver example.

Persist mutation deduplication under logical connection, effective actor,
operation and request ID, together with digest and original business receipt.
`MCPMutationReceipt` means durable business acceptance. Its `Retry` returns the
saved acceptance for an identical digest and rejects conflicting request reuse.
Look up accepted retries before current target/capability checks so a retired
target does not erase an accepted receipt. C0 supplies no in-memory substitute
for durable storage. Messages must include their immutable trusted causal
metadata in their owning implementation's canonical digest.

`MCPError` carries machine codes; presentation layers localize user-facing text.
`unavailable` permits the identical request only when non-admission is known.
Response loss or uncertainty after mutation dispatch is `outcome_unknown`, whose
retry directive is `reconcile`. Unknown outcomes do not authorize a fresh request
or automatic replay. Conflict, fenced, unauthorized and unsupported errors do
not authorize automatic retries. No transport receipt implies business completion.

The eventual cutover requires updated Pot/plugins/adapters and explicit reconnect.
There is no old P2P pairing compatibility, fallback or automatic old-connection
migration. Historical Tasks, reports and artifacts remain; unresolved old work
must not be silently cancelled or replayed. Persistence requirements belong to
the implementation phase and require centrally owned migrations and upgrade
coverage; C0 changes no stored format.
