# Product scope and deferred work

[Architecture](architecture.md) defines ownership and current execution.

Pot uses Go for product logic and Wails v3 for its macOS App. App, tray and browser
share HTML/CSS/JavaScript components. `pot serve` runs standalone Runtime on macOS
or Linux. An independent `web-ui-server` publishes its local node. Native App
presents local assistants and Agent Link peers through existing chat and task UI.

Each node owns models, credentials, workspaces and local execution. Cross-node
work uses Agent Link delegation to an autonomous recipient. Local and linked
agent operations share a capability-driven port; linked chat and task submission
need no local assistant. The local agent and scheduler remain in-process, and
Skills remain local to the execution implementation. Plugin extraction and Skill
transport are deferred. The internal local
task executor retains original receipts and cleanup fences. Runtime supplies no
VM/container management or guest desktop. Existing user data and unknown work
are never implicitly deleted, adopted or replayed.

## Deferred execution and platform work

Optional VM management, execution-only Runtime composition, remote Main tools,
automatic cross-machine scheduling, project/workspace replication and VM execution
images require separate design and qualification under issue #74. Removing old
VM execution Providers does not implement these capabilities.

Linux/Windows native desktop clients, mobile clients, remote desktop streaming,
native desktop automation, live execution migration and automatic wake-up remain
outside the current contract. Single-machine execution uses OS service-account
permissions and process-group cleanup, not VM or cgroup isolation.
