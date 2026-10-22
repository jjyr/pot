# Architecture

Pot is a Go application with two hosts: standalone `pot serve` and the macOS
Wails App. App, tray and browser share the HTML/CSS/JS frontend. Each installation
is an autonomous node; Agent Link supplies collaboration between nodes.

```text
Chat / tasks / schedules / agent tools
                  |
      Agent Link operation port
   capability discovery + authorization
                  |
       +----------+----------+
       |                     |
 local owner adapter   authenticated peer adapter
       |                     |
 local Pot execution   Pot / Codex execution
       |                     |
       +----------+----------+
                  |
     receiver-owned execution policy,
     models, resources and permissions
```


## Ownership

Each node owns its assistants, models, credentials, workspaces, permissions,
resources, usage and task execution. The local operator API uses a private Unix
socket; App uses native bindings. An independent browser service publishes
session-authenticated access to one local node. Peer connections grant
collaboration capabilities, never remote operator access or credentials.

Fresh roots contain no assistants. First-use Chat and the directory + action
open the same Add assistant page, ordered API, another Pot, then Codex. Only an
explicit API creation action creates a kitchen-named local assistant. Linked
assistants and user-originated linked tasks need neither a local Provider nor a
local assistant. Provider storage alone creates no assistant. Each root retains
its stable signing identity and secret master key. App and standalone Runtime
cannot open the same root concurrently. Closing an App window keeps background
work running; quitting stops local executions. See [Deployment](deployment.md)
and [Secrets](secrets.md).

## Collaboration

Agent Links identify nodes and advertise assistant directories, persisted defaults
and supported capabilities. Pot exposes real assistants. The Codex adapter exposes
virtual assistants for message communication. Explicit Codex tasks bypass assistants:
one task binds one persistent thread and returns approvals to the directing
assistant. Message requests use receiver policy and never forward user approvals.
Other linked tasks resolve their explicit target or the receiving default once.

The receiver owns accepted tasks and execution facts. The sender stores a durable
Task and its original execution reference in the same catalogue used for local
work. Status, results, cancellation and notifications address that original
identity. Unsupported operations fail explicitly. Accepted cancellation does not prove execution stopped.

Chat and assistant-scoped task/schedule operations enter the same Agent Link
port for local and linked assistants. UI controls follow versioned operation
descriptions, never adapter type or location. Local owner-only configuration
remains an explicit capability; peer authentication does not grant it.

Direct conversations have durable independent contexts. Incoming peer text is
reference data, not user authority. Explicit messaging tools send replies; final
model text is not automatically forwarded. Assistant-directed task results enter
the original assistant's inbox. User-directed task results return to their
original conversation and task notifications without a local assistant.
See [Agent Links](agent-links.md),
[assistant messages](assistant-messages.md), [task lifecycle](task-coordination.md)
and the [Codex adapter](../integrations/codex/README.md).

## Execution and recovery

Every Agent invocation enters the Agent Link abstraction, including local work.
Local adapters call in-process; peer adapters use authenticated transport. A Task
can target a local Pot assistant, a peer Pot assistant or a Codex executor.
The receiver selects models, prepares resources and enforces permissions on its
own filesystem. Agent receives injected execution services rather than discovering
product or machine infrastructure. Main and Worker instruction boundaries are
defined in [Agent context](agent-context.md).

`internal/control` supplies Agent Link operation adapters and product services.
`internal/tasks` owns one durable Task catalogue, request receipts, outcome cursor
and archive transactions for all targets in `tasks.sqlite`. A linked Task stores
its immutable submission and original execution reference, without a local model
snapshot or launch. Its last receipt and observation time remain available while
disconnected; lack of observation never proves execution stopped.

The receiving Pot adapter additionally owns local runs, checkpoints, workspaces
and event logs. Local admission and run creation commit together. Tool side
effects have one receipt authority in `operations.sqlite`; there is no second command journal.
Plugin packages, Skills and MCP definitions belong to the local filesystem.
Every local Pot Agent invocation passes through the shared execution manager.
Node shutdown and updates interrupt only this local execution; linked Tasks retain their receiver-owned state.
See [Backend components](backend-components.md),
[Executor](executor.md) and [Permissions](permissions.md).

There are no Control/Runner deployment roles or centrally configured remote
Runners. Each execution node owns its model configuration, credentials, resources
and permissions. Agent Link transfers task intent and results, not a prepared
local model snapshot or raw command launch. A node used only to access linked
assistants does not require a local Provider or assistant.

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
Agent Link. Unicast `tcp://host:port` endpoints use automatic self-signed
certificates and public-key pins to authenticate and encrypt content. The listener
exposes identity and collaboration services. Yamux multiplexes independent calls;
CBOR describes each message and Zstd optionally compresses large bodies.

UI identity includes node, assistant and conversation. Drafts, history and
notifications retain their owner; navigation generations reject late responses.
All conversations use the same [chat endpoint contract](agent-links.md#chat-execution-projection).
Native and peer adapters supply receiver-owned per-input execution lifecycles;
the frontend does not choose chat behavior by assistant location. The topology,
assistant directory, chat and task components remain shared. See [Node UI](node-ui.md),
[Shared UI](shared-ui.md) and [UI events](ui-events.md).
