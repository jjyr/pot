# Documentation

[Architecture](architecture.md) defines the current design and distinguishes pending
implementation. Code defines actual behavior. Issue [#39](https://github.com/jjyr/pot/issues/39)
records the earlier distributed-runtime delivery. Issue
[#66](https://github.com/jjyr/pot/issues/66) tracks the Go/Wails migration.

| Topic | Read |
|---|---|
| Deployment, resource APIs, Control membership and web-ui-server | [Deployment](deployment.md) |
| Build, test and release | [Development](development.md) |
| Delivered scope and remaining work | [Development plan](development-plan.md) |
| Temporary Go/Wails migration plan, sequencing and rules | [Go/Wails migration](migrations/go-wails.md) |
| Assistant rules, project AGENTS.md, routing and Skills | [Agent context](agent-context.md) |
| Visual language, responsive navigation and editing-page interactions | [Design language](design-language.md) |
| Native Space ownership, combined views and offline behavior | [Multi-Space UI](multi-space-ui.md) |
| Encrypted credentials and master keys | [Secrets](secrets.md) |
| Backend responsibilities and API contracts | [Crates](backend-crates.md), [Runtime](../crates/runtime/README.md), [Control](../crates/control/README.md) |
| Tools, approvals and browser execution | [Composition](tool-composition.md), [permissions](permissions.md), [computer use](computer-use.md) |
| Assistant messaging and App relay | [Assistant messages](assistant-messages.md) |
| Tasks, schedules and usage | [Tasks](task-coordination.md), [schedules](scheduled-tasks.md), [token usage](token-usage.md) |
| AI providers, model defaults and offline metadata | [Model directory](model-directory.md) |
| Sandbox execution and resources | [Providers](sandbox-providers.md), [executor](executor.md), [resources](sandbox-resources.md), [images](sandbox-image.md), [macOS](macos-sandbox.md) |
| Event-driven updates and stream lifecycle | [UI events](ui-events.md) |
| Shared UI, language and licenses | [Shared UI](shared-ui.md), [internationalization](internationalization.md), [licenses](licenses-and-about.md) |
| Product goals | [Product](product-design.md) |

## Evidence

[Pre-execution resource synchronization](validation/resource-sync-20261014.md).

[Native multi-Space UI](validation/multi-space-ui-20261013.md).

[Assistant messages and App relay](validation/assistant-messages-20261013.md).

[Execution target terminology and binding validation](validation/execution-targets-20261013.md).

[Machine capability permissions](validation/machine-permissions-20261012.md).

[Embedded Web assets and compression](validation/embedded-web-assets-20261012.md).

[Contextual onboarding](validation/contextual-onboarding-20261012.md).

[Integrated titlebar and parent navigation](validation/titlebar-parent-navigation-20261012.md).

[Unified Runtime Agent execution](validation/runtime-execution-20261012.md).

[Machine labels and composer references](validation/machine-labels-20261012.md).

[Component boundaries and regression checks](validation/component-boundaries-20261011.md).

These historical reports establish only their recorded test scope. Do not add new
validation reports to the repository; report checks and limitations in the task
response instead. Historical distributed evidence: [membership](validation/control-membership-20261011.md),
[App](validation/control-client-20261011.md), [Web](validation/control-web-20261011.md),
[Machines](validation/machines-simple-20261011.md), [Chat downloads](validation/chat-file-downloads-20261011.md),
[global resources](validation/control-global-resources-20261011.md),
[plugins](validation/control-plugins-20261011.md),
[Task Agent production integration](validation/task-agent-production-20261011.md),
[remote schedules](validation/remote-schedules-20261011.md),
[cross-machine clients](validation/control-cross-machine-ui-20261011.md),
[notifications](validation/control-notifications-20261011.md),
[final qualification](validation/control-final-qualification-20261011.md).
Existing platform/executor reports are archived under `validation/`; applicable
platform research remains under `research/`. Superseded designs and reports are
retained in Git history.

## Maintenance

Write documentation in English. Keep one architecture overview and one source per
contract; link rather than repeat. Commands belong in deployment/development.
Remove contradictions and superseded recommendations. Keep phase progress and
validation results in the owning issue (#66 for the migration), not in a second
repository TODO table. Do not claim pending features from fixture results.
The user-authorized temporary migration plan may describe phases and rules;
delete it and its links once migration is complete, after transferring lasting
contracts into the regular documentation.
