# Backend component boundaries

[Architecture](architecture.md) defines deployment and ownership. The root Go
module uses the toolchain pinned in `mise.toml`.

`pkg/` contains public, importable libraries; `cmd/` contains executable entry
points; `internal/` contains product implementation. All packages share one root
`go.mod`. See [Go libraries](go-libraries.md) for embedding and asset ownership.

| Component | Responsibility |
|---|---|
| `pkg/core`, `pkg/protocol` | Immutable model/execution contracts, tool schemas, public DTOs and framing |
| `pkg/agent` | Injected model/tool loop, classified context, compaction and Skill metadata discovery |
| `pkg/modelprotocol` | Pure Chat Completions, Responses, Anthropic Messages and Gemini codecs |
| `pkg/composition` | goja orchestration, isolated worker process, turn-scoped exec/wait lifecycle |
| `pkg/executor`, `pkg/pluginhost` | Commands, patches, tool process sessions and Control-local background/metadata readers |
| `pkg/storage` | SQLite transactions, messages, events and context persistence |
| `pkg/instance`, `pkg/modeldirectory` | Private data-root identity/locking and provider model metadata |
| `internal/assistant` | Identities, rules, conversation Threads, chat driver and durable mailboxes/sessions |
| `pkg/peer` | Shared signed TCP sessions, host identity proof, framing and service admission; no business authorization |
| `pkg/agentlink` | Node-bound authenticated collaboration, assistant directories and durable request recovery |
| `internal/linkhost` | Agent Link adapters plus one host-owned peer service registry/listener |
| `internal/gateway`, `internal/secrets` | Node-owned provider HTTP routes and encrypted credential references |
| `pkg/integrations`, `pkg/integrations/github` | Independent service login, single-account connections and credential caching through injected account/Secrets stores; GitHub device authorization adapter |
| `internal/usage` | Durable reservations, allowances, usage settlement, tools and compaction accounting |
| `internal/control`, `internal/tasks`, `internal/schedule` | Product coordination, model selection, permissions, durable Tasks and schedules |
| `internal/nodeidentity` | Autonomous node signing identity and local machine labels |
| `internal/runner` | Destination-local immutable Task launches, checkpoints, original command inputs/receipts and event logs |
| `pkg/execution` | Shared destination Agent queue, concurrency, cancellation and uncertainty fences |
| `pkg/terminal` | Ephemeral user-operated PTYs, bounded replay and explicit process cleanup |
| `pkg/plugin` | Common Plugin manifests, discovery, confined asset access and tool-only snapshots |
| `pkg/runtime` | Public complete-host facade, lifecycle and authenticated operator handler |
| `internal/runtime` | Product composition, root ownership and component lifecycle behind the facade |
| `internal/transport`, `internal/localui` | Private/native operator adapters and transient local UI projections |
| `internal/webui`, `internal/access`, `internal/tailnet` | Independent browser service, authentication, access operator and embedded Tailscale |
| `internal/desktop`, `cmd/pot-app` | Wails v3 windows, tray, native dialogs and shared frontend bridge |
| `web/` | Shared HTML/CSS/JS frontend, DOM state, themes, localization and host adapters |
| `pkg/eval`, `cmd/pot-eval`, `eval/` | Independent deterministic and explicitly selected bounded live evaluation |

`cmd/pot` hosts standalone Runtime and the explicitly configured browser service. `cmd/pot-composition-worker` supplies
an isolated composition worker for embedding applications. Components are libraries in the
same process unless a deployment or process-isolation boundary is explicit.
Agent and Runner receive injected services. They neither import Control/Gateway
nor acquire provider credentials. Runtime composes components through their APIs;
HTTP adapters and databases stay with their owning components.

Every product Agent invocation, including mail and summary sessions, passes
through the destination's shared execution manager. The owning node manages provider keys,
approvals, accounting and global resources. Workers run on their owning node and use its local execution services.

Plugin manifests and declared assets under `plugins/` are packaged runtime data.
The Go host loads them independently of execution.

`mise run check:go` checks formatting, vet and architectural imports. Headless
packages must not depend on Wails; execution components must not depend on product
Control, Runtime, transport, Gateway, Secrets or desktop implementation packages.
Public component packages have no transitive product implementation dependencies;
`pkg/runtime` is the explicit exception because it embeds the complete host.
See [Development](development.md) for the normal check entry points.
