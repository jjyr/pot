# Documentation index

Choose a page for your question; reading everything is unnecessary. Code defines the current implementation. Product goals are not delivered features, and historical acceptance results do not establish the current state.

| Goal | Read |
|---|---|
| Start, build, or test | [Development guide](development.md) |
| Understand delivered and remaining work | [Development plan](development-plan.md) |
| Understand components, data flow, and task orchestration | [Architecture](architecture.md) |
| Understand backend crate and standalone Server boundaries | [Backend crate split](backend-crates.md) |
| Change APIs, Pi, task state, or recovery | [Runtime contract](../runtime/README.md) |
| Build the Sandbox image, desktop theme, or CJK fonts | [Sandbox image](sandbox-image.md) |
| Change chat, tasks, settings, or Sandbox UI | [UI specification](ui-ux-design.md) |
| Apply the approved visual, component, motion, and navigation direction | [Design language](design-language.md) (target design; implementation pending) |
| Change token accounting, periodic allowances, or budget recovery | [Token usage](token-usage.md) |
| Change scheduled triggers, time zones, or notifications | [Scheduled tasks](scheduled-tasks.md) |
| Review long-term product boundaries and acceptance goals | [Product design](product-design.md) |
| Review completed tests and limitations | [Agent layer](validation/agent-layer.md), [Pi MVP](validation/pi-mvp.md), [Multitask](validation/multitask.md), [UI](validation/ui-ux.md), [Sandbox image](validation/sandbox-image.md), [Backend crates](validation/backend-crates.md), [AI configuration](validation/ai-configuration.md) |
| Investigate old integrations or technical decisions | [Historical POC index](poc-plan.md) |

## Research on demand

Latest AI integration acceptance: [Simplified AI integration, 2026-10-06](validation/ai-integration-20261006.md). Earlier Terminal-based screenshots are historical.

`research/` contains dated background material, not current implementation specifications:

- UI framework: [GPUI Kit](research/gpui-kit-evaluation.md). See the UI specification for current component usage.
- VMs and desktops: [Apple Containerization](research/apple-containerization.md), [Docker GUI](research/docker-gui.md).
- Driver sessions: [Coordination comparison](research/session-coordination.md), [Hermes](research/hermes-sessions.md), [OpenClaw](research/openclaw-sessions.md).
- Future memory: [Identity and memory](research/identity-memory.md).
- Original note sources and hashes at import: [Source manifest](source-manifest.json). These are not hashes of the current files.

## Maintenance rules

Maintain documentation in English. Keep one current specification per topic, progress in the development plan, and commands in the relevant entry-point document. Acceptance reports record the date, method, results, and untested scope; append later changes without rewriting old observations as new conclusions. Keep superseded recommendations in Git history or label them historical. Machine information belongs in `~/Workspace/Machines`.
