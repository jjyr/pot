# Documentation

[Architecture](architecture.md) defines the current design and distinguishes pending
implementation. Code defines actual behavior. Issue [#39](https://github.com/jjyr/pot/issues/39)
is the single phase progress table.

| Topic | Read |
|---|---|
| Deployment, resource APIs, Control membership and Web access | [Deployment](deployment.md) |
| Build, test and release | [Development](development.md) |
| Delivered scope and remaining work | [Development plan](development-plan.md) |
| Assistant rules, project AGENTS.md, routing and Skills | [Agent context](agent-context.md) |
| Chat, Tasks, Machines and configuration interactions | [UI specification](ui-ux-design.md), [design language](design-language.md) |
| Encrypted credentials and master keys | [Secrets](secrets.md) |
| Backend responsibilities and API contracts | [Crates](backend-crates.md), [Runtime](../crates/runtime/README.md), [Control](../crates/control/README.md) |
| Tools, approvals and browser execution | [Composition](tool-composition.md), [permissions](permissions.md), [computer use](computer-use.md) |
| Tasks, schedules and usage | [Tasks](task-coordination.md), [schedules](scheduled-tasks.md), [token usage](token-usage.md) |
| Sandbox execution and resources | [Providers](sandbox-providers.md), [executor](executor.md), [resources](sandbox-resources.md), [images](sandbox-image.md), [macOS](macos-sandbox.md) |
| Shared UI, language and licenses | [Shared UI](shared-ui.md), [internationalization](internationalization.md), [licenses](licenses-and-about.md) |
| Product goals | [Product](product-design.md) |

## Evidence

[Execution target terminology and binding validation](validation/execution-targets-20261013.md).

[Machine capability permissions](validation/machine-permissions-20261012.md).

[Embedded Web assets and compression](validation/embedded-web-assets-20261012.md).

[Contextual onboarding](validation/contextual-onboarding-20261012.md).

[Integrated titlebar and parent navigation](validation/titlebar-parent-navigation-20261012.md).

[Unified Runtime Agent execution](validation/runtime-execution-20261012.md).

[Machine labels and composer references](validation/machine-labels-20261012.md).

Current [component boundaries and regression checks](validation/component-boundaries-20261011.md).
Previous [design cleanup](validation/design-cleanup-20261011.md).

Validation reports record date, method, results and limitations. They establish only
the tested scope. Current distributed evidence: [membership](validation/control-membership-20261011.md),
[App](validation/control-client-20261011.md), [Web](validation/control-web-20261011.md),
[Machines](validation/machines-simple-20261011.md), [Chat downloads](validation/chat-file-downloads-20261011.md),
[global resources](validation/control-global-resources-20261011.md),
[plugins](validation/control-plugins-20261011.md),
[Task Agent production integration](validation/task-agent-production-20261011.md),
[remote schedules](validation/remote-schedules-20261011.md),
[cross-machine clients](validation/control-cross-machine-ui-20261011.md),
[notifications](validation/control-notifications-20261011.md),
[final qualification](validation/control-final-qualification-20261011.md).
Platform/executor reports remain under `validation/`; applicable platform research
remains under `research/`. Superseded designs and reports are retained in Git history.

## Maintenance

Write documentation in English. Keep one architecture overview and one source per
contract; link rather than repeat. Commands belong in deployment/development.
Remove contradictions and superseded recommendations. Progress belongs in #39;
do not create a second TODO table or claim pending features from fixture results.
