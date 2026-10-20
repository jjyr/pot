# Node and assistant UI

Agent Link supplies linked nodes and their assistant directories. Each assistant
has a full node and assistant identity. Connection icons distinguish Codex and
cloud Pot assistants in the existing directory, conversations and delegated task
rows. Names and list positions never determine routing.

The topology retains its existing graph, regions, edges, toolbar and responsive
inspector. Copy connection information on one side and paste it into Add Agent
on the other. Validation and connection return to the original graph with the
node selected. The inspector exposes its assistants and disconnect, reconnect
and delete actions. Deletion confirms removal; task and conversation history
remain available. No separate binding wizard or task creation page is required.

Local machine permissions stay with the local node. Linked nodes own their
models, credentials, workspaces and execution permissions. The UI provides no
remote Control management, Runner membership or remote owner credential entry.
Settings and native operator routes address the local installation.

The native `internal/localui` adapter provides a transient local presentation
scope for the existing shared layout and event subscriptions. It persists no
Space configuration and cannot forward operator calls to another node. Peer
assistants use Agent Link business routes. The legacy `/spaces` UI projection is
presentation grouping only and grants no ownership or permissions.

Drafts, selections, cached history, subscriptions and notifications retain full
node, assistant and conversation identities. A missing or disconnected destination
never falls back to another node or assistant. Notifications navigate to their
original conversation. Existing chat bubbles, composer, task detail and result
notification components remain shared. See [Design language](design-language.md)
and [Agent Links](agent-links.md).

Local assistants are created directly from + with random kitchen names and the
local node's default Provider and model.
