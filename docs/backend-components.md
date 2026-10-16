# Backend component boundaries

[Architecture](architecture.md) defines deployment and ownership. The root Go
module uses the toolchain pinned in `mise.toml`.

| Component | Responsibility |
|---|---|
| `internal/core`, `internal/protocol` | Immutable model/execution contracts, tool schemas, public DTOs and framing |
| `internal/agent` | Injected model/tool loop, classified context, compaction and Skill metadata discovery |
| `internal/modelprotocol` | Pure Chat Completions, Responses, Anthropic Messages and Gemini codecs |
| `internal/composition` | goja orchestration, isolated worker process, turn-scoped exec/wait lifecycle |
| `internal/executor`, `internal/pluginhost` | Commands, patches, process sessions and retained Node plugin execution |
| `internal/storage` | SQLite transactions, messages, events and context persistence |
| `internal/assistant` | Identities, rules, conversation Threads, chat driver and durable mailboxes/sessions |
| `internal/relay` | Transport-independent assistant-message delivery; composed only by native App |
| `internal/gateway`, `internal/secrets` | Control-owned provider HTTP routes and encrypted credential references |
| `internal/usage` | Durable reservations, allowances, usage settlement, tools and compaction accounting |
| `internal/control`, `internal/tasks`, `internal/schedule` | Product coordination, model selection, permissions, durable Tasks and schedules |
| `internal/controlplane` | Stable signing identities, invitations, membership, outward signed WebSocket RPC |
| `internal/runner` | Destination-local immutable Task launches, checkpoints, original command inputs/receipts and event logs |
| `internal/execution` | Shared destination Agent queue, concurrency, cancellation and uncertainty fences |
| `internal/terminal` | Ephemeral user-operated PTYs, bounded replay and explicit process cleanup |
| `internal/sandbox` | Definitions, selection, lifecycle, scopes, leases, receipts, mappings and recovery |
| `internal/sandboxprovider`, `internal/platformhelper` | Shared macOS/Linux Podman provider and retained Swift native macOS VM adapter |
| `internal/resource`, `internal/resourcecontrol`, `internal/sandboxresources` | Verified resource delivery, owner consent and embedded image catalog |
| `internal/runtime` | Composition, root ownership and component lifecycle |
| `internal/transport`, `internal/spaces` | Private/native API adapters and owner-scoped local/remote client aggregation |
| `internal/webui`, `internal/access`, `internal/tailnet`, `internal/browserpush` | Independent browser service, authentication, access operator and embedded Tailscale |
| `internal/desktop`, `cmd/pot-app` | Wails v3 windows, tray, native dialogs and shared frontend bridge |
| `web/` | Shared HTML/CSS/JS frontend, DOM state, themes, localization and host adapters |
| `internal/eval`, `cmd/pot-eval`, `eval/` | Independent deterministic and explicitly selected bounded live evaluation |

`cmd/pot` hosts standalone Runtime and the explicitly configured browser service;
`cmd/pot-tool-executor` hosts guest execution. Components are libraries in the
same process unless a deployment or process-isolation boundary is explicit.
Agent and Runner receive injected services. They neither import Control/Gateway
nor acquire provider credentials. Runtime composes components through their APIs;
HTTP adapters and databases stay with their owning components.

Every product Agent invocation, including mail and summary sessions, passes
through the destination's shared execution manager. Control owns provider keys,
approvals, accounting and global resources. Connected Workers run beside their
fixed Host/Sandbox target and call Control through run-bound signed ports.

Native macOS VM support stays in `helper/` using Swift. JavaScript
plugins, Node execution and Python guest services retain their existing roles.
Go components compose these runtime assets through explicit interfaces.

`mise run check:go` checks formatting, vet and architectural imports. Headless
packages must not depend on Wails; execution components must not depend on product
Control, Runtime, transport, Gateway, Secrets or desktop implementation packages.
See [Development](development.md) for the normal check entry points.
