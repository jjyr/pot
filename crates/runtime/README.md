# Runtime composition

`pot-runtime` assembles a local execution Host and an optional Control component.
It owns startup, activation, shutdown and component lifetimes. It contains no
chat logic, Provider configuration, secrets, accounting, database or HTTP handler.

```text
App UI -- in-memory API --> local Control -- injected services --> local execution
Remote clients ----------> remote Control -- authenticated API -> Runner
```

`initialize` starts neutral infrastructure. Explicit resource creation enables
Host/Sandbox execution or the local Control's assistant services. Enabling a
component adds it to the existing composition; it does not replace the Runtime
or temporarily suppress its destructor. App and its local components start and
exit together. The embedded Control never opens the remote enrollment listener.

See [components](../../docs/backend-crates.md), [Control APIs](../control/README.md),
[deployment](../../docs/deployment.md), and [architecture](../../docs/architecture.md).
