# Documentation index

Choose a page for your question; reading everything is unnecessary. Code defines the current implementation. Product goals are not delivered features, and historical acceptance results do not establish the current state.

| Goal | Read |
|---|---|
| Install standalone CLI or understand embedded App hosting | [Standalone Server](standalone-server.md) |
| Start, build, or test | [Development guide](development.md) |
| Review About information and dependency license checks | [About and license policy](licenses-and-about.md) |
| Publish a macOS release or update the Homebrew cask | [Releases and Homebrew](development.md#releases-and-homebrew) |
| Understand delivered and remaining work | [Development plan](development-plan.md) |
| Understand components, data flow, and task orchestration | [Architecture](architecture.md) |
| Review Host Agent / multiple Sandbox boundaries | [Local Agents and managed Sandboxes](agent-sandbox-design.md)  |
| Understand backend crate and standalone Server boundaries | [Backend crate split](backend-crates.md) |
| Understand AGENTS.md discovery and injection | [Agent instructions](agent-instructions.md) |
| Inject skills into main and task pot-agent sessions | [Agent skill injection](agent-skills.md) |
| Change APIs, pot-agent, task state, or recovery | [Runtime contract](../runtime/README.md) |
| Build the Sandbox image, desktop theme, or CJK fonts | [Sandbox image](sandbox-image.md) |
| Configure QR/pairing browser access and encrypted main-message push | [Mobile access](mobile-access.md) |
| Change UI language, translations or locale matching | [Internationalization](internationalization.md) |
| Understand shared App/Mobile page components and platform adapters | [Shared UI](shared-ui.md) |
| Change chat, tasks, settings, or Sandbox UI | [UI specification](ui-ux-design.md) |
| Apply the approved visual, component, motion, and navigation direction | [Design language](design-language.md) (shared shell, page layouts, Chat and Pot icons implemented; acceptance limits documented) |
| Change token accounting, periodic allowances, or budget recovery | [Token usage](token-usage.md) |
| Change scheduled triggers, time zones, or notifications | [Scheduled tasks](scheduled-tasks.md) |
| Review long-term product boundaries and acceptance goals | [Product design](product-design.md) |
| Review completed tests and limitations | [Host Agents and managed Sandboxes](validation/host-pot-agent-20261006.md) |

Current hosting/authentication acceptance: [Standalone CLI and embedded App, 2026-10-07](validation/standalone-hosting-20261007.md).

Sandbox UI acceptance: [Sandbox environment list and desktop, 2026-10-06](validation/sandbox-ui-20261006.md).

Current decoupling acceptance: [Host pot-agent and Sandbox tools, 2026-10-06](validation/host-pot-agent-20261006.md). Earlier Pi/Codex observations remain dated history.

## Research on demand

`research/` contains dated background material, not current implementation specifications:

- UI framework: [GPUI Kit](research/gpui-kit-evaluation.md). See the UI specification for current component usage.
- VMs and desktops: [Apple Containerization](research/apple-containerization.md), [Docker GUI](research/docker-gui.md).
- Driver sessions: [Coordination comparison](research/session-coordination.md), [Hermes](research/hermes-sessions.md), [OpenClaw](research/openclaw-sessions.md).
- Future memory: [Identity and memory](research/identity-memory.md).

## Maintenance rules

Maintain documentation in English. Keep one current specification per topic, progress in the development plan, and commands in the relevant entry-point document. Acceptance reports record the date, method, results, and untested scope; append later changes without rewriting old observations as new conclusions. Keep superseded recommendations in Git history or label them historical. Machine information belongs in `~/Workspace/Machines`.
