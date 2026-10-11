# Backend crate boundaries

[Architecture](architecture.md) defines deployment and ownership.

| Crate | Responsibility |
|---|---|
| pot-core | Shared profiles, typed environments, execution feedback and tool contracts |
| pot-agent | Injected model/tool loop, context, compaction and Skill discovery |
| pot-storage | Owner Threads, product records, durable transactions and usage |
| pot-gateway | Provider transport and model protocol codecs |
| pot-runtime | Resource assembly, assistants, Tasks/schedules, approvals and routing |
| pot-tool-executor | Production Host/guest commands, patches and process sessions |
| pot-server | Standalone/App adapters, authenticated APIs, Control and Web transport |
| pot-protocol | Public client DTOs and API constants |
| pot-sandbox | Definitions, lifecycle, scopes, leases, receipts and recovery |
| pot-sandbox-linux-podman | Rootless Linux Podman lifecycle and guest transport |
| pot-sandbox-macos-apple-container | Apple Containerization Helper and Linux guest transport |
| pot-sandbox-macos-apple-vm | Prepared VZ instance and pinned guest TLS; qualification pending |
| pot-resource / pot-sandbox-images | Verified delivery, image catalog and recipes |
| pot-app / pot-ui | Native client and shared UI state/components |
| pot-eval | Deterministic and explicitly selected live evaluation |

Server creates neutral Runtime infrastructure. Resource APIs assemble optional
assistant, Host and Sandbox services; persisted definitions restore them. App uses
in-memory native dispatch; CLI uses an OS-protected socket. Runtime injects
execution and owner services into pot-agent. Providers depend on Sandbox contracts,
not Runtime; Sandbox Manager does not depend on concrete Providers.

Connected Tasks use Control-owned model/global configuration and approvals.
Fresh Task Agent loops run on the destination Runtime, alongside the fixed
Host/Sandbox, with signed outward Control model/approval/guidance ports. Main
assistants remain on their owner. Guest execution services hold
no provider credentials or product management authority. Shared Sandbox workspaces
do not establish isolation between Tasks.
