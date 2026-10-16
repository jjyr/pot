# Product scope and deferred work

[Architecture](architecture.md) defines the current implementation and ownership.
Issue [#66](https://github.com/jjyr/pot/issues/66) records the Go/Wails transition;
issue [#39](https://github.com/jjyr/pot/issues/39) records the earlier distributed
runtime design. Implementation progress and qualification results belong in the
owning issue, separate from these lasting product boundaries.

## Current scope

Pot uses Go for product logic. The macOS App uses Wails v3 and the system WebView;
its main window, tray panel and browser share HTML/CSS/JavaScript components,
visual tokens and interaction state. Linux supports standalone Control and
execution-node processes. The browser frontend is served by an independently
configured `web-ui-server`, bound to one explicitly selected Control. Native App
combines its local Space with one remote Control. See
[Shared UI](shared-ui.md), [Multi-Space UI](multi-space-ui.md) and
[Deployment](deployment.md).

Control owns assistants, conversations, instructions, Provider credentials,
Tasks, schedules, approvals, accounting and global resources. Destination Runners
execute immutable assignments against fixed Host/Sandbox targets through outward
authenticated transport. Guidance, interruption, cancellation, allowance recovery,
result transcripts and file delivery retain their original owner/run identity.
Unknown effects require reconciliation rather than replacement execution.
See [Task coordination](task-coordination.md) and [Permissions](permissions.md).

The macOS Sandbox boundary remains Swift. JavaScript plugins and Node execution,
Python guest services and the desktop viewer keep their existing responsibilities.
Go tool composition uses goja. Linux Sandbox resources are separately distributed,
versioned and verified; client packages do not silently download them. Local
resource preparation requires explicit owner consent. See
[Backend components](backend-components.md), [Sandbox providers](sandbox-providers.md)
and [Resource delivery](sandbox-resources.md).

The Go release uses independent data roots and new machine registration. It does
not import earlier Rust databases or provide mixed-version interoperability.
Existing data, images and workspaces are not implicitly deleted or adopted.

## Deferred platform work

Linux and Windows native desktop applications, Android and iOS clients, and new
Sandbox Providers remain outside the current platform scope. Browser layout
continues to support the documented phone/tablet widths; that does not imply a
native mobile client or mobile execution Provider.

Remote desktop viewing through Control remains deferred until its transport and
connection ownership are agreed. Current native desktop viewing is restricted to
the exact local Runtime; remote Task execution does not imply remote desktop
transport. General native desktop automation, live Agent relocation and automatic
machine wake-up remain outside the current execution contract.

Owner-enrolled macOS VM support remains distinct from distributing installation
media or automatically cloning prepared development templates. Automatic Sandbox
image upgrades, mutable remote resource catalogs and platform expansion require
separate design and release work. Each release must retain explicit platform,
resource-distribution and external-service qualification boundaries; process-level
recovery does not establish whole-machine power-loss behavior or operating-system
push delivery.
