# Node and assistant UI

Agent Link supplies linked nodes and their assistant directories. Each assistant
has a full node and assistant identity. Connection icons distinguish Codex and
cloud Pot assistants in the existing directory, conversations and delegated task
rows. Names and list positions never determine routing.

Execution-machine names use the current OS hostname, with a local interface IP
and finally the OS name as discovery fallbacks. `GET /api/v1/runtime` includes a single
`host` object with `hostname`, `ip_addresses`, `os` and local `tags` as live,
best-effort presentation metadata. Host information does not duplicate node
identity or permission policy, persist interface addresses, or change stable
routing/policy IDs. There is no machine-list API or Add machine action; the
graph projects the owning runtime's host and Agent Link connects autonomous nodes.
Only up, non-loopback interfaces supply
unicast addresses; link-local addresses are omitted. Addresses are deduplicated
and sorted with IPv4 before IPv6. These addresses do not prove remote reachability.
The Link graph and inspector show the same host information directly.
Tasks, tool execution details and approval cards resolve execution IDs through
the owning node's runtime host metadata and show hostname plus the first interface IP.
Schedule editors select these machine labels while submitting stable IDs.
Unavailable inventory shows a localized missing-information label, never an ID.
Connection lists and assistant source labels show the peer name and available
host information; they never fall back to a node ID.
Execution machines show their hostname and one interface IP. Linked nodes show
the reported hostname together with the saved TCP endpoint's host and port;
this endpoint is a connection address, not a discovered remote interface inventory.
An incoming-only link may have no saved endpoint. Missing host information stays
empty. Internal node IDs and a technical-details disclosure are not shown.

The icon topology adds task load below each Agent and current totals above the
graph. Waiting for approval, allowance exhaustion, unconfirmed execution cleanup,
failed chat execution, unavailable status and connection loss are marked on the
affected node with an icon and text; there is no global attention counter.
Ordinary execution and queueing do not produce warnings. The inspector shows
per-assistant activity, task counts and their node-wide scope, receipt time and
directional message queues. Edges show locally pending delivery counts; moving
lights indicate pending delivery on a connected link, not model execution or
measured network throughput. Selection and keyboard focus survive refreshes.
See [Agent Link](agent-links.md) for sampling and stale data.

The topology retains its existing graph, regions, edges, toolbar and responsive
inspector. Copy connection information on one side and paste it into Add Agent
on the other. Validation and connection return to the original graph with the
node selected. The inspector exposes its assistants and disconnect, reconnect
and delete actions. Deletion confirms removal; task and conversation history
remain available. No separate binding wizard or task creation page is required.
Provided assistants use their saved profile avatars, matching the chat directory;
node headers keep the Pot or Codex brand icon.
Agent Link directories carry the receiver's saved avatars. Pot and Codex
assistants advertise profile settings for names and avatars independently of
model configuration. Profile edits preserve routing and the original session.
The authenticated browser service publishes the same profile read, update and
default-avatar operations as the native UI. Settings loading and retries retain
the selected assistant's full identity. A failed load replaces the loading state
with a localized error and retry action; leaving cancels the pending read.
Local and linked settings share the **More actions → Delete assistant** disclosure,
confirmation dialog and error states. Linked deletion requires the separately
advertised `assistant.delete` capability and an online destination. Disconnecting
while editing disables deletion without discarding drafts. Confirmation explains
that retirement affects every client of that remote assistant. The receiver
rejects ongoing replies, queued work and uncertain execution cleanup. Deleting
one assistant preserves its node connection and other assistants.

Chat, task controls, assistant settings and notifications use the selected
assistant's versioned Agent Link capabilities. They never infer support from
local/remote placement or the Pot/Codex icon. Unsupported operations stay hidden
or disabled; linked task results and their conversation keep their original route.

Local machine permissions stay with the local node. Linked nodes own their
models, credentials, workspaces and execution permissions. The UI provides no
remote operator configuration or owner credential entry.
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

First-use Chat and + open the same Add assistant page in the conversation region.
Its choices are API, another Pot, then Codex. The directory remains available on
wide windows; narrow windows use a full-width page with Back. Returning to a
conversation preserves its draft and reading position. API creation uses the
local default Provider/model or opens the shared Provider editor first. Linked
assistants come from the receiver directory; adding a node does not create a
local assistant. A sole chat-capable assistant, or the receiver's chat-capable
default, opens automatically; otherwise the page offers a choice. A connected
Codex task executor returns to a connection confirmation explaining how the
current assistant can delegate a task; it does not open a Codex chat. Empty remote
chat directories offer refresh, and failed directory reads offer recovery.

Browser language and appearance use `pot-ui-locale` and `pot-ui-theme` per origin.
The `pot-ui-v3` service worker cache replaces obsolete `pot-ui-*` asset caches.
Preferences use only the current keys; old development preferences are not imported.
