# Product scope and deferred work

[Architecture](architecture.md) defines ownership and current execution.

Pot uses Go for product logic and Wails v3 for its macOS App. App, tray and browser
share HTML/CSS/JavaScript components. `pot serve` runs standalone Runtime on macOS
or Linux. An independent `web-ui-server` publishes one explicitly selected Control.
Native App combines local and remote Spaces.

The default working system is one Control with local execution. Main tools and
Tasks without an explicit machine run there. Existing enrolled Runners support
explicit remote Tasks with signed transport, resource synchronization and original
execution receipts. Runtime supplies no VM/container management or guest desktop.
Tool plugins and browser execution use the selected machine's installed software.
Existing user data, disks and workspaces are never implicitly deleted or adopted.

## Deferred execution and platform work

Optional VM management, execution-only Runtime composition, remote Main tools,
automatic cross-machine scheduling, project/workspace replication and VM execution
images require separate design and qualification under issue #74. Removing old
VM execution Providers does not implement these capabilities.

Linux/Windows native desktop clients, mobile clients, remote desktop streaming,
native desktop automation, live execution migration and automatic wake-up remain
outside the current contract. Single-machine execution uses OS service-account
permissions and process-group cleanup, not VM or cgroup isolation.
