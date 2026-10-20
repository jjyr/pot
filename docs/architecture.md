# Architecture

Pot is a Go application with two hosts: standalone `pot serve` and the macOS
Wails App. App, tray and browser share the HTML/CSS/JS frontend. Each installation
is an autonomous node; Agent Link supplies collaboration between nodes.

```text
Chat / task delegation / notifications
                 |
       Agent Link business contract
                 |
      +----------+-----------------------+
      |                                  |
local in-process adapter          authenticated peer TCP
      |                                  |
local Pot assistants             Pot / Codex node
      |                          assistant directory + default
local executor                   own execution and task facts
```

## Ownership

Each node owns its assistants, models, credentials, workspaces, permissions,
resources, usage and task execution. The local operator API uses a private Unix
socket; App uses native bindings. An independent browser service publishes
session-authenticated access to one local node. Peer connections grant
collaboration capabilities, never remote operator access or credentials.

Fresh roots contain no assistants. First-use Chat creates a kitchen-named
assistant once a default Provider exists; + creates later assistants with the same
local defaults. Provider storage alone creates no assistant. Each root retains
its stable signing identity and secret master key. App and standalone Runtime
cannot open the same root concurrently. Closing an App window keeps background
work running; quitting stops local executions. See [Deployment](deployment.md)
and [Secrets](secrets.md).

## Collaboration

Agent Links identify nodes and advertise assistant directories, persisted defaults
and supported capabilities. Pot exposes real assistants. The Codex adapter exposes
virtual assistants bound to persistent sessions. An explicit target wins; omission
resolves the receiving node's default once. Retries retain the accepted assistant.

The receiver owns accepted tasks and execution facts. The sender stores a durable
delegation and original remote reference. Status, results, cancellation and
notifications address that original identity. Unsupported operations fail
explicitly. Accepted cancellation does not prove execution stopped.

Direct conversations have durable independent contexts. Incoming peer text is
reference data, not user authority. Explicit messaging tools send replies; final
model text is not automatically forwarded. Results enter the original assistant's
existing notification flow. See [Agent Links](agent-links.md),
[assistant messages](assistant-messages.md), [task lifecycle](task-coordination.md)
and the [Codex adapter](../integrations/codex/README.md).

## Execution and recovery

Local execution targets stay local; remote work uses Agent Link delegation.
The receiver selects models, prepares resources and enforces permissions on its
own filesystem. Agent receives injected execution services rather than discovering
product or machine infrastructure. Main and Worker instruction boundaries are
defined in [Agent context](agent-context.md).

`internal/control` coordinates the local product; `internal/runner` owns local
launches, checkpoints, command receipts and cleanup evidence. These remain local
implementation components. Every product Agent invocation passes through the
shared execution manager. See [Backend components](backend-components.md),
[Executor](executor.md) and [Permissions](permissions.md).

A request ID binds immutable contents. Exact retries return the original receipt;
conflicting contents fail. Lost acknowledgments, restart and reconnect never
create replacement work. Disconnect pauses a link; delete removes its saved
connection. Both retain history and neither silently cancels accepted tasks.
Unknown outcomes and unconfirmed cleanup block replay and replacement.

Upgrades retain original identities, journals and cached transcript pages while
archiving retired distributed membership. Historical remote tasks keep their
original destination and recovery fences. They are never converted into local
launches or new delegations. SQLite schemas and upgrades belong to
[Database migrations](database-migrations.md).

## Transport and presentation

[Peer transport](peer-protocol.md) uses stable Ed25519 identities and mutually
authenticated TLS 1.3 sessions. Service admission separately verifies the saved
Agent Link. Private `tcp://host:port` endpoints use automatic self-signed
certificates and public-key pins to authenticate and encrypt content. The listener
exposes identity and collaboration services. Yamux multiplexes independent calls;
CBOR describes each message and Zstd optionally compresses large bodies.

UI identity includes node, assistant and conversation. Drafts, history and
notifications retain their owner; navigation generations reject late responses.
Local changes and conversation output use subscriptions. Linked conversations
currently refresh snapshots on a bounded interval. The existing topology,
assistant directory, chat and task components remain shared. See [Node UI](node-ui.md),
[Shared UI](shared-ui.md) and [UI events](ui-events.md).
