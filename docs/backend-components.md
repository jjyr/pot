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
| `pkg/executor`, `pkg/pluginhost` | Commands, patches, tool process sessions and node-local background/metadata readers |
| `pkg/storage` | SQLite transactions, messages, events and context persistence |
| `pkg/instance`, `pkg/modeldirectory` | Private data-root identity/locking and provider model metadata |
| `internal/assistant` | Identities, rules, conversation Threads, chat driver and durable mailboxes |
| `pkg/peer` | Shared TLS 1.3 TCP sessions, mutual identity proof, Yamux streams, CBOR/Zstd framing and service admission; no business authorization |
| `pkg/agentlink` | Product operation port, per-assistant capability contracts, node-bound collaboration and durable request recovery |
| `internal/linkhost` | Agent Link adapters plus one host-owned peer service registry/listener |
| `internal/gateway`, `internal/secrets` | Node-owned provider HTTP routes and encrypted credential references |
| `pkg/integrations`, `pkg/integrations/github` | Independent service login, single-account connections and credential caching through injected account/Secrets stores; GitHub device authorization adapter |
| `internal/usage` | Durable reservations, allowances, usage settlement, tools and compaction accounting |
| `internal/control`, `internal/tasks`, `internal/schedule` | Local/peer operation adapters, product coordination, model selection, permissions, durable Tasks, local execution/checkpoints/event logs and schedules |
| `internal/nodeidentity` | Autonomous node signing identity and local machine labels |
| `pkg/execution` | Shared destination Agent queue, concurrency, cancellation and uncertainty fences |
| `pkg/terminal` | Ephemeral user-operated PTYs, bounded replay and explicit process cleanup |
| `pkg/plugin` | Common Plugin manifests, discovery, confined asset access and tool-only snapshots |
| `pkg/runtime` | Public complete-host facade, lifecycle and authenticated operator handler |
| `internal/runtime` | Product composition, root ownership and component lifecycle behind the facade |
| `internal/transport`, `internal/localui` | Private/native request translation into the operation port, node operator services and transient local UI projections |
| `internal/webui`, `internal/access` | Independent browser service, authentication and private access operator |
| `internal/desktop`, `cmd/pot-app` | Wails v3 windows, tray, native dialogs and shared frontend bridge |
| `web/` | Shared HTML/CSS/JS frontend, DOM state, themes, localization and host adapters |
| `pkg/eval`, `cmd/pot-eval`, `eval/` | Independent deterministic and explicitly selected bounded live evaluation |

`cmd/pot` hosts standalone Runtime and the explicitly configured browser service. `cmd/pot-composition-worker` supplies
an isolated composition worker for embedding applications. Components are libraries in the
same process unless a deployment or process-isolation boundary is explicit.
Agent and task execution receive injected services. They neither import Control/Gateway
nor acquire provider credentials. Runtime composes components through their APIs;
HTTP adapters stay with their owning components. Control owns the single
`tasks.sqlite` connection pool and lends it to the Task service, run journal,
Mailbox and scheduler. Borrowers share transactions when an operation crosses
those components; shutdown closes the pool after consumers stop.

Every local Pot Agent invocation, including correspondence and result handling in
its owning conversation, passes through the shared execution manager. The owning
node manages provider keys,
approvals, accounting and global resources. Workers run on their owning node and use its local execution services.

Task operations and model task tools enter the same Agent Link operation port.
The local Pot adapter accepts owner-authorized configuration through `AcceptLocal`;
peer Pot and Codex adapters own their execution policy. All caller-facing Tasks,
including linked submissions, share the Task database, list query, request
receipts, outcome index and archive transaction. There is no parallel live
Delegation catalogue in Control. Linked Tasks carry an execution binding instead
of a local configuration or launch. The Pot adapter's local execution manager,
run journal and cleanup proof remain receiver implementation details.
Historical remote run records cannot enter local execution. Activity indicators,
topology observations, maintenance and allowance recovery use the same
receiver-ownership filter, separately from the location-independent catalogue.

Builtin packages ship as runtime resources. User packages and Skills live in
`~/.pot/plugins/<name>` and `~/.pot/skills/<name>`. File settings are read by the
owning node; execution freezes selected resources in its local cache. The Go
host loads manifests independently of execution.

`mise run check:go` checks formatting, vet and architectural imports. Headless
packages must not depend on Wails; execution components must not depend on product
Control, Runtime, transport, Gateway, Secrets or desktop implementation packages.
Public component packages have no transitive product implementation dependencies;
`pkg/runtime` is the explicit exception because it embeds the complete host.
See [Development](development.md) for the normal check entry points.
