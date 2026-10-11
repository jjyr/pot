# Runtime

`pot-runtime` assembles the execution Host and optional Control, and owns their
startup, activation and shutdown. Every Host owns one shared Agent execution
manager, implemented by `pot-runtime-execution` to avoid a dependency cycle with
business adapters. Runtime has no chat database, Provider configuration, secrets,
accounting or HTTP handlers.

```text
Control chooses target, model and resources
             |
       memory / network
             |
       destination Runtime
       shared execution manager
       queue / capacity / cancellation / trace
             |
          Agent engine
```

Assistant and Task adapters supply context, services and an event sink. They do
not start the Agent engine themselves. All assistants and local Tasks share the
same manager as Tasks accepted from Control over the network. A remote dispatch
occupies an Agent slot only on the destination, not on the dispatching Control.

The manager assigns an execution ID and correlates it with Runtime/boot,
owner, request/run ID, generation and role. One invocation is one assistant reply
or Task run; persistent conversations do not consume idle slots. Runtime owns
in-process execution state; assistant conversation storage and destination Task
journals remain their adapters' responsibilities. The manager does not choose
machines, models, tools, instructions or approval policy.

## Execution lifecycle

```text
queued --> running --> succeeded / failed / cancelled
   |          |
   +----------+--> interrupted (running exit without confirmed settlement)
```

Queued cancellation never starts an Agent. Running cancellation is cooperative;
capacity remains held until Agent tool cleanup and feedback settlement finish.
Dropping a running future, missing terminal results or failed settlement fences
that execution and retains its slot until Runtime restart. A dropped queued
future releases its queue entry. Active duplicate owner/generation/request
invocations are rejected; durable request replay belongs to the business adapter.
Shutdown rejects new work, cancels queued/running work and waits for cleanup.

`Backend::executions()` exposes snapshots, cancellation by execution ID and drain.
Snapshots contain active executions and the latest 256 completed executions.
See [deployment](../../docs/deployment.md#runtime-agent-execution) for local limits
and bounded metadata logs. These logs are diagnostic, not a replacement for durable
Task receipts or conversation history.

`initialize` starts neutral infrastructure. Explicit resource creation enables
Host/Sandbox execution or assistant services. Enabling a component extends the
existing composition and preserves its execution manager. App and local components
start and exit together. Embedded Control never opens the remote enrollment listener.

See [components](../../docs/backend-crates.md), [Control APIs](../control/README.md)
and [architecture](../../docs/architecture.md).
