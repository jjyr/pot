# Documentation

[Architecture](architecture.md) defines the current design and distinguishes pending
implementation. Code defines actual behavior.

| Topic | Read |
|---|---|
| Deployment, resource APIs, Control membership and web-ui-server | [Deployment](deployment.md) |
| Build, test and release | [Development](development.md) |
| Current product scope and deferred platform work | [Development plan](development-plan.md) |
| Assistant rules, project AGENTS.md, routing and Skills | [Agent context](agent-context.md) |
| Visual language, responsive navigation and editing-page interactions | [Design language](design-language.md) |
| Native Space ownership, combined views and offline behavior | [Multi-Space UI](multi-space-ui.md) |
| Encrypted credentials and master keys | [Secrets](secrets.md) |
| Backend responsibilities and API contracts | [Backend components](backend-components.md) |
| Tools, approvals and browser execution | [Composition](tool-composition.md), [permissions](permissions.md), [computer use](computer-use.md) |
| Assistant messaging and App relay | [Assistant messages](assistant-messages.md) |
| Tasks, schedules and usage | [Tasks](task-coordination.md), [schedules](scheduled-tasks.md), [token usage](token-usage.md) |
| AI providers, model defaults and offline metadata | [Model directory](model-directory.md) |
| Sandbox execution and resources | [Providers](sandbox-providers.md), [executor](executor.md), [resources](sandbox-resources.md), [images](sandbox-image.md), [macOS](macos-sandbox.md) |
| Event-driven updates and stream lifecycle | [UI events](ui-events.md) |
| Shared UI, language and licenses | [Shared UI](shared-ui.md), [internationalization](internationalization.md), [licenses](licenses-and-about.md) |
| Agent evaluation | [CLI](../eval/README.md), [measurement design](token-evaluation.md), [small-budget suite](token-evaluation-next-suite.md) |
| Product goals | [Product](product-design.md) |

## Maintenance

Write documentation in English. Keep one architecture overview and one source per
contract; link rather than repeat. Commands belong in deployment/development.
Remove contradictions and superseded recommendations. Keep implementation progress and
validation results in the owning issue, not in a second repository TODO table.
Do not claim pending features from fixture results.
