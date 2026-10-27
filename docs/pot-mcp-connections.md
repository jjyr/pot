# Pot MCP connections

Pot collaboration uses the shared [MCP contract](mcp-collaboration-contract.md).
One Pot connects to the other Pot's configured HTTP(S) MCP resource. The
initiating Pot registers its existing assistants as receivers and polls addressed
Work/Control over that same connection. It needs no public receiver listener.
Deployers own endpoint reachability, HTTP/HTTPS and certificates.

```text
Pot B owning Control ---- OAuth + PKCE / MCP client ----> Pot A MCP service
   |                                                        |
   | existing assistants <---- register / claim Work --------+ local producers
   | existing Task service <-- durable journal / admission   |
   +-- real Task/run ---------> events / exact receipts ------+ Task catalogue
   +-- own assistant ---------> scoped task / mailbox calls --+ local backend
```

The common receiver transport, journal CAS and polling/reconciliation driver live
in `internal/mcpreceiver`. Native Hosts and Pot use this driver. Each backend
continues to own admission, execution, cleanup and recovery; MCP does not create
another scheduler. A claim acceptance is distinct from execution admission, a
real run and a terminal result.

## Connect and operate

Open connection settings in the browser, enter the other Pot's MCP resource URL
(for example `https://pot.example/mcp`) and choose **Connect with browser**. Sign
in using the other Pot's existing login and confirm the displayed receiver node.
The browser returns to the initiating Pot's existing UI. Its callback must be
HTTPS or loopback HTTP as required by the OAuth server. Browser Space selection
is retained in that callback. A native UI needs an existing supported browser
callback origin; a custom application URL is not an OAuth callback.

The browser host permits a top-level cross-site GET navigation only to the fixed
callback page. The page consumes its single-use state through the authenticated
same-origin owner API; loading the page does not establish an owner session.

The connection stores its own encrypted OAuth grant and a stable logical
connection ID. It never imports a model provider's or another application's
login. Authorization codes use S256 PKCE and a single-use pending state. Renewing
an expired grant reuses the existing registered client and connection; revoked
connections require a new explicit connection. No refresh token is assumed.

Connecting registers only existing assistants. It creates no assistant and
selects no default. The node itself registers identity with no default execution
capability. Each assistant has its own real address and effective operations;
display names and connection IDs are never actors. The connected directory shows
the other Pot's own endpoints, not its unrelated downstream connections.

Disconnect pauses communication without cancelling accepted work. Reconnect
uses the same saved connection and reconciles the original claim request,
admission and event outbox. Revocation invalidates the remote grant and retains
Tasks, results, mailbox history and journal entries. A failed remote revocation
leaves the connection paused and reports failure; it does not claim success.
The receiving service also provides its existing MCP connection consent/revocation
page.

Availability requires successful authenticated communication. Persisted catalogue
entries and unexpired tokens do not prove a receiver is online. Inbound receiver
leases reset when the MCP service restarts. Offline directories remain useful for
history and explicit targets; they are not permission to replay work.

## Identity and execution

Ordinary browser/MCP calls stay user-kind. Own-node assistant correspondence and
root assistant Task delegation use a separately registered node path. A local
Worker keeps its real Task/run and the existing product restrictions: it cannot
delegate or send messages to remote assistants. Active-run attestation establishes
identity without granting those operations or owner authority.

The owning Control derives a Task actor from its trusted execution call binding
and persisted active Task/run/generation. Received Tasks retain their real claim.
Locally originated Tasks use the same connection's execution admission/end
association. Both forms are fenced on the exact run; neither uses a made-up claim,
a copied Task ID as a missing run, or an assistant identity in place of a Task.

A receiver first journals the immutable delivery. Task admission and the mapping
to its actual local Task/run commit in the same Tasks transaction. The origin
queue Task and execution Task may differ. Terminal input compares the old run and
confirmed cleanup before creating a new real run on the same execution Task.
Cancellation remains intent until the execution backend reports terminal
cleanup evidence. Transport retries and disconnects never launch work again.

Messages retain their original immutable envelope, conversation, reply and hop
metadata. Pot delivers correspondence into its existing serial assistant mailbox;
it does not fabricate a Task/run for mailbox acceptance. A message receipt proves
that the mailbox accepted it, not that a model processed it. Direct browser chat
uses a user-kind caller and its existing conversation projection.

## Human decisions

Pending execution requests remain in the executing Pot's existing PermissionStore.
The receiving Pot's local user cannot approve a remotely directed Task. The
originating Pot's authenticated human control plane can inspect and decide only
its originally accepted delegation, with the real execution Task/run/generation,
request ID, approval ID and process epoch checked again at consumption.

For receiver execution, pending requests travel over the receiver's authenticated
MCP call and the original user's once-only decision returns as Control. For
execution on the listening Pot, the initiating Pot polls scoped pending reviews
and sends the original human's decision over that same outbound connection.
Both paths share the existing permission request; neither creates a second
approval ledger. Ordinary MCP/model tools expose no human decision operation.
User-kind callers remain separate from node assistant/Task actors.

For a direct user Task executed on the listening Pot, the initiating Control
uses receiver-only `pot_receiver_user_task_submit` with `MCPHumanTaskSubmit`.
It derives the source human from the persisted original execution request,
separately from the listening Pot's provisioned OAuth user caller. The source
human is stored with that caller, logical connection and original request in the
same transaction as submission intent. Exact retries retain it; conflicting
human or submission data cannot overwrite it. Human decisions compare this exact
source identity. Assistant delegations retain the owning Control's stable
operator identity and existing authenticated `ManageApproval` entry point.
The receiver credential attests this trusted Control boundary; a supplied user
field alone is not proof of a human action. Models and ordinary OAuth clients
cannot use these receiver tools.

Tasks migration 0011 adds this provenance association without inferring it for
existing work. A submission missing its original association cannot acquire it
through a later retry or review. Its saved history and pending work remain;
operators must resolve that work explicitly rather than guessing a human or
replaying it as a new request.

Only **allow once** and **deny** apply to these requests. A remote decision cannot
create a Task grant, command rule or permanent permission. Completed, cancelled,
restarted, revoked, stale-run or mismatched requests are fenced. A lost transport
does not imply a human decision and does not cancel accepted work. Explicit origin
invalidation expires the waiting local approval without authorizing execution.

## Files and upgrades

This receiver does not advertise file-byte transfer. Result/evidence metadata
retains its execution origin, but a remote path is not a download and does not
grant remote filesystem access. Local product file delivery still enforces its
existing source-node checks. Remote bytes require a separately implemented,
capability-checked transfer path.

Update both Pots and relevant adapters/plugins, then explicitly reconnect. There
is no old P2P pairing compatibility, automatic old-connection migration or fallback
from a failed MCP request to TCP. The product no longer mounts the old entry points. Existing historical Tasks, reports and artifacts remain with
their original identities. Unresolved old work must not be automatically cancelled,
replayed or erased. An MCP connection never imports an old pending mailbox as new
work.
