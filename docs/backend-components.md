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
| `pkg/executor`, `pkg/pluginhost` | Commands, patches, process sessions and retained Node plugin execution |
| `pkg/storage` | SQLite transactions, messages, events and context persistence |
| `pkg/instance`, `pkg/modeldirectory` | Private data-root identity/locking and provider model metadata |
| `internal/assistant` | Identities, rules, conversation Threads, chat driver and durable mailboxes/sessions |
| `pkg/agentlink` | Assistant-bound authenticated message exchange, encrypted connection references and durable recovery |
| `internal/linkhost` | Typed Agent message and Runner execution adapters, composed by their owning host |
| `internal/gateway`, `internal/secrets` | Control-owned provider HTTP routes and encrypted credential references |
| `pkg/integrations`, `pkg/integrations/github` | Independent service login, single-account connections and credential caching through injected account/Secrets stores; GitHub device authorization adapter |
| `internal/usage` | Durable reservations, allowances, usage settlement, tools and compaction accounting |
| `internal/control`, `internal/tasks`, `internal/schedule` | Product coordination, model selection, permissions, durable Tasks and schedules |
| `internal/controlplane` | Stable signing identities, invitations, membership, outward signed WebSocket RPC |
| `internal/runner` | Destination-local immutable Task launches, checkpoints, original command inputs/receipts and event logs |
| `pkg/execution` | Shared destination Agent queue, concurrency, cancellation and uncertainty fences |
| `pkg/terminal` | Ephemeral user-operated PTYs, bounded replay and explicit process cleanup |
| `pkg/plugin` | Common Plugin manifests, discovery, confined asset access and tool-only snapshots |
| `pkg/runtime` | Public complete-host facade, lifecycle and authenticated operator handler |
| `internal/runtime` | Product composition, root ownership and component lifecycle behind the facade |
| `internal/transport`, `internal/spaces` | Private/native API adapters and owner-scoped local/remote client aggregation |
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
through the destination's shared execution manager. Control owns provider keys,
approvals, accounting and global resources. Connected Workers run beside their
fixed Runtime and call Control through run-bound signed ports.

Tool plugin manifests and Node sources live under `plugins/`. Runtime asset loading
is independent of execution; no VM lifecycle or driver package is linked.

`mise run check:go` checks formatting, vet and architectural imports. Headless
packages must not depend on Wails; execution components must not depend on product
Control, Runtime, transport, Gateway, Secrets or desktop implementation packages.
Public component packages have no transitive product implementation dependencies;
`pkg/runtime` is the explicit exception because it embeds the complete host.
See [Development](development.md) for the normal check entry points.
