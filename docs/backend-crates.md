# Backend crate boundaries

[Architecture](architecture.md) defines deployment and ownership.

| Crate | Responsibility |
|---|---|
| pot-core | Shared profiles, typed execution targets, execution feedback and tool contracts |
| pot-agent | Injected model/tool loop, context, compaction and Skill discovery |
| pot-storage | Message/event storage and SQLite transaction primitives |
| pot-assistant | Assistant identities, instructions, conversation Threads, chat driver and durable assistant mailboxes |
| pot-relay | Transport-independent cross-Control Assistant message relay; composed only by App |
| pot-usage | Request reservations, allowances, usage settlement and provider budgets |
| pot-secrets | Encrypted SQLite secrets with separately supplied master keys |
| pot-model-protocol | Pure model messages, wire codecs and response parsing |
| pot-gateway | Provider HTTP transport and SQLite Provider registry with secret references |
| pot-control | Owner/task coordination, model policy, API adapters and execution target integration |
| pot-runner | Destination Task Agent execution, immutable launch manifests and run journals |
| pot-runtime | Runtime composition, shared execution manager and component lifecycle |
| pot-runtime-execution | Destination-local Agent invocation, queue, concurrency, cancellation and execution tracing |
| pot-tool-executor | Production Host/guest commands, patches and process sessions |
| pot-server | Standalone/App adapters, authenticated APIs, Control transport and web-ui-server browser API adapter |
| pot-protocol | Public client DTOs, Assistant message contracts and API constants |
| pot-sandbox | Definitions, lifecycle, scopes, leases, receipts and recovery |
| pot-sandbox-linux-podman | Rootless Linux Podman lifecycle and guest transport |
| pot-sandbox-macos-apple-container | Apple Containerization Helper and Linux guest transport |
| pot-sandbox-macos-apple-vm | Prepared VZ instance and pinned guest TLS; qualification pending |
| pot-resource / pot-sandbox-images | Verified delivery, image catalog and recipes |
| pot-app / pot-ui | Native client and shared UI state/components |
| pot-eval | Deterministic and explicitly selected live evaluation |

Server creates neutral Runtime infrastructure. Resource APIs assemble optional
assistant, Host and Sandbox services; persisted definitions restore them. App uses
in-memory native dispatch; CLI uses an OS-protected socket. Control injects explicit in-memory or authenticated network services into pot-agent.
Every product Agent invocation passes through the destination Runtime execution manager.
Assistant and Task adapters supply context and consume results; they do not start
the engine directly. Agent has no implicit HTTP fallback and no Provider networking dependency. Providers depend on Sandbox contracts,
not Runtime; Sandbox Manager does not depend on concrete Providers.

Connected Tasks use Control-owned model/global configuration and approvals.
Fresh Task Agent loops run on the destination Runtime, alongside the fixed
Host/Sandbox, with signed outward Control model/approval/guidance ports. Main
assistants remain on their owner. Guest execution services hold
no provider credentials or product management authority. Shared Sandbox workspaces
do not establish isolation between Tasks.

Component boundaries are checked by `mise run test:architecture`. Assistant,
Relay, Gateway, Usage, Secrets, Agent, Runner and Runtime Execution cannot depend on Control, Runtime or
Server. Runtime has no direct database or HTTP dependencies. Control contains
adapters that bind product state to the independently testable components; it is
not a second Agent implementation. Gateway remains in the Control process.

Assistant configuration handles are shared across owners and Tasks. Provider
metadata and encrypted secrets live in Control's `control.sqlite`; creating an
assistant does not copy credentials. See [secrets](secrets.md).
