# Documentation index

Choose a page for your question; reading everything is unnecessary. Code defines the current implementation. Product goals are not delivered features, and historical acceptance results do not establish the current state.

| Goal | Read |
|---|---|
| Deploy the Web UI with an Agent; choose HTTPS or HTTP access | [Web UI deployment guide](web-ui-deployment.md) |
| Install standalone CLI or understand embedded App hosting | [Standalone Server](standalone-server.md) |
| Start, build, or test | [Development guide](development.md) |
| Review About information and dependency license checks | [About and license policy](licenses-and-about.md) |
| Publish a macOS release or update the Homebrew cask | [Releases and Homebrew](development.md#releases-and-homebrew) |
| Understand delivered and remaining work | [Development plan](development-plan.md) |
| Understand components, data flow, and task orchestration | [Architecture](architecture.md) |
| Review Host Agent / multiple Sandbox boundaries | [Local Agents and managed Sandboxes](agent-sandbox-design.md)  |
| Review the Linux and macOS target design | [Sandbox provider redesign](sandbox-providers.md) (Linux extraction delivered; macOS prepared-instance implementation awaiting VM qualification) |
| Prepare/enroll a macOS development VM | [macOS Sandbox](macos-sandbox.md) |
| Understand backend crate and standalone Server boundaries | [Backend crate split](backend-crates.md) |
| Understand AGENTS.md discovery and injection | [Agent instructions](agent-instructions.md) |
| Inject skills into main and task pot-agent sessions | [Agent skill injection](agent-skills.md) |
| Change APIs, pot-agent, task state, or recovery | [Runtime contract](../runtime/README.md) |
| Understand approved downloads, progress, cache and embedded hashes | [Sandbox resource delivery](sandbox-resources.md) |
| Build the Linux Sandbox image, or review target Linux/macOS template layout | [Sandbox image](sandbox-image.md) |
| Understand command shells, PTY, background sessions and initialization snapshots | [Sandbox command execution](executor.md) |
| Configure QR/Token browser access and encrypted main-message push | [Mobile access](mobile-access.md) |
| Change UI language, translations or locale matching | [Internationalization](internationalization.md) |
| Understand shared App/Mobile page components and platform adapters | [Shared UI](shared-ui.md) |
| Change chat, tasks, settings, or Sandbox UI | [UI specification](ui-ux-design.md) |
| Apply the approved visual, component, motion, and navigation direction | [Design language](design-language.md) (shared shell, page layouts, Chat and Pot icons implemented; acceptance limits documented) |
| Change token accounting, periodic allowances, or budget recovery | [Token usage](token-usage.md) |
| Change scheduled triggers, time zones, or notifications | [Scheduled tasks](scheduled-tasks.md) |
| Review long-term product boundaries and acceptance goals | [Product design](product-design.md) |
| Review completed tests and limitations | [Host Agents and managed Sandboxes](validation/host-pot-agent-20261006.md) |

Podman Web desktop acceptance: [Desktop and Client isolation, 2026-10-07](validation/podman-desktop-20261007.md).

Linux/Podman and multiple-client acceptance: [Linux Server and clients, 2026-10-07](validation/linux-server-clients-20261007.md).

Web access redesign: [Token authentication and embedded Tailscale, 2026-10-08](validation/web-access-token-20261008.md).

Independent Web UI access: [LAN HTTP and optional tailnet HTTPS, 2026-10-08](validation/web-access-independent-20261008.md).

Native client reliability acceptance: [Helper failures and conversation receipts, 2026-10-07](validation/client-reliability-20261007.md).

Provider/executor merge acceptance: [Cross-turn sessions and executor v2 image, 2026-10-07](validation/sandbox-merge-20261007.md).

First resource Release: [0.0.1 publication, 2026-10-07](validation/sandbox-resource-release-20261007.md).

Resource delivery acceptance: [Embedded catalog, approval and Linux VM, 2026-10-07](validation/sandbox-resources-20261007.md).

Unified desktop gateway acceptance: [Sandbox desktop sessions, 2026-10-07](validation/sandbox-desktop-20261007.md).

Sandbox UI acceptance: [Sandbox environment list and desktop, 2026-10-06](validation/sandbox-ui-20261006.md).

Sandbox architecture extraction: [Manager and Linux Provider, 2026-10-07](validation/sandbox-architecture-20261007.md).

Current decoupling acceptance: [Host pot-agent and Sandbox tools, 2026-10-06](validation/host-pot-agent-20261006.md). Earlier Pi/Codex observations remain dated history.

## Research on demand

`research/` contains dated background material, not current implementation specifications:

- UI framework: [GPUI Kit](research/gpui-kit-evaluation.md). See the UI specification for current component usage.
- VMs and desktops: [Apple Containerization](research/apple-containerization.md), [Docker GUI](research/docker-gui.md).
- macOS implementation route: [VZ/Xcode Provider research](research/macos-provider.md).
- Native OS execution: [macOS and Windows Sandbox providers](research/macos-windows-sandboxes.md).
- Deferred Android: [SDK Emulator provider research](research/android-provider.md).
- Deferred virtual iPhone: [vphone provider research](research/vphone-provider.md).
- Driver sessions: [Coordination comparison](research/session-coordination.md), [Hermes](research/hermes-sessions.md), [OpenClaw](research/openclaw-sessions.md).
- Future memory: [Identity and memory](research/identity-memory.md).

## Maintenance rules

Maintain documentation in English. Keep documentation consistent with the overall architecture; implementation details are determined by the code and do not require corresponding documentation updates. Keep one architecture overview per topic, progress in the development plan, and commands in the relevant entry-point document. Acceptance reports record the date, method, results, and untested scope; append later changes without rewriting old observations as new conclusions. Keep superseded recommendations in Git history or label them historical.
