# Go libraries

Pot has one Go module, `github.com/jjyr/pot`, using the toolchain pinned in
`mise.toml`. Public libraries live in `pkg/`; executable entry points live in
`cmd/`; product coordination, HTTP adapters and desktop implementation live in
`internal/`. Execution and process-worker adapters target macOS and Linux.
Import the packages directly, for example:

```go
import (
    "github.com/jjyr/pot/pkg/agent"
    "github.com/jjyr/pot/pkg/core"
    "github.com/jjyr/pot/pkg/modelprotocol"
)
```

Package placement is an API boundary. Public component packages have no
transitive dependency on product implementation. `pkg/runtime` is the explicit
complete-host facade and may compose `internal/` services behind private fields.
Internal implementation types do not appear in its public signatures. See
[component boundaries](backend-components.md) for package responsibilities.

## Third-party integrations

`pkg/integrations` provides account and OAuth session lifecycle behind injected
`Adapter`, `AccountStore` and `SecretStore` ports. `SQLAccounts` accepts a
caller-owned database. `CredentialResolver` provides scoped reads for execution
callers. `pkg/integrations/github` implements GitHub browser device authorization.
Neither package imports Control or internal product code. See
[Integrations](credentials.md) for API and lifecycle contracts.

## Embed an Agent loop

`agent.Agent` runs the model/tool loop. The caller supplies:

- `agent.Services`: model response streams and compaction observations.
- `agent.Feedback`: checkpoint persistence, steering and turn settlement.
- `agent.Dispatch`: authorized tool calls, when tools are enabled.
- `agent.Cleanup` and an event sink: execution cleanup and ordered turn output.
- `core.Thread`, `core.ExecutionContext` and `core.TurnRequest`: immutable
  configuration, owner binding and the original request identity.

Build a validated configuration with `core.Profile.Resolve()`. These are Pot's
execution contracts: profile/revision, provider reference, policy and specification
hash validation still apply. Default agent settings enable product tools; set the
desired built-in tools and plugins explicitly when embedding. Tool permissions,
credentials, persistence and model HTTP access remain the caller's responsibility.
Model codecs in `pkg/modelprotocol` do not fetch provider credentials.

For message references, `core.TurnRequest.Inputs` carries each accepted user's
transcript ID, original text, attachments and selected quotes. `Agent.MessageID`
is an optional caller callback that supplies the transcript ID for each assistant
output before streaming starts; that ID also enters the durable model history.
`SteeringInput.MessageID` and `References` do the same for active-turn input.
Provider encoders render these identities as text metadata while keeping native
provider IDs and signed replay independent. See [Chat references](chat-references.md).

`pkg/agent` and `pkg/modelprotocol` do not link Wails, SQLite, Tailscale or the
complete host. Agent services do not require the product's RPC interface.
`pkg/execution` can independently provide queueing, concurrency and cancellation;
`pkg/executor` supplies command/patch execution and process sessions. The caller must select an explicit host/sandbox/session execution mode. Sandboxed
execution requires the pinned Node/SRT assets and process-worker integration; Node
is also required for JavaScript plugins. The Agent loop itself does not need Node.

## Configure composition explicitly

Model-only and ordinary tool execution need no Composer. Composed tools require
an explicit implementation of `agent.Composer`. To retain process isolation,
build the worker and inject its absolute path:

```sh
go build -o /absolute/path/pot-composition-worker ./cmd/pot-composition-worker
```

```go
composer := composition.ProcessRunner{
    Command: []string{"/absolute/path/pot-composition-worker"},
}
loop.Composer = composer
```

The worker receives private framed JSON over stdin/stdout. Tool dispatch stays
in the parent. It does not inherit provider keys, user configuration or language
runtime paths. Missing worker configuration returns
`composition.ErrWorkerCommandRequired`; it never assumes that an embedding
application implements Pot's hidden worker entry point. Pot executables explicitly
configure their own hidden worker handler. Evaluation callers inject the Composer
through `eval.RunOptions` or the suite/live entry points.
See [tool composition](tool-composition.md) for lifecycle and memory limits.

## Embed a complete host

Use `pkg/runtime` when the application needs Pot's persistent assistants, Control
services, resource provisioning and shared execution lifecycle:

```go
host, err := runtime.Open(ctx, runtime.Options{
    Root: "/absolute/private/pot-data",
    ResourceDirectory: "/absolute/path/resources",
    Composer: composer,
    Embedded: true,
})
if err != nil {
    return err
}
defer host.Close()
handler := host.Handler(operatorToken)
```

Use the import `github.com/jjyr/pot/pkg/runtime`. Root and resource directory are
required; the facade does not discover assets in the caller's checkout. Supply
the retained tool plugins, Skills and Node needed by the selected features.
Assets load without starting execution processes or downloading images. See
[Plugin packages](plugins.md). The facade exposes execution capacity and owner
resource APIs; child environment Providers and image managers are absent.

The host exclusively locks its root and owns its managers. Stop HTTP serving and
all use of borrowed managers before closing it. `Info()` returns a copy of its
identity. `Handler(token)` provides the authenticated operator API, including
streams; the caller owns the listener, transport and token distribution. An empty
token denies all requests. The facade does not publish the shared browser UI.

Provider credentials use the system key store by default. Applications can
provide a `runtime.KeySource` with a persistent 32-byte key. Preserve that key
across restarts and return a fresh byte slice on every call, because the host
clears it after use. The complete host intentionally includes SQLite and product
services; use `pkg/agent` when those services are unnecessary.

## Atomic message journals

`storage.Store.SaveWithMetadataMutation` commits a message projection, owner
metadata and a caller-owned journal mutation in one transaction. Callbacks use
the supplied transaction and perform no external effects. An accepted user
message marked `queued` has no started execution and survives storage reopening;
selecting it clears that flag with the new turn's metadata and queue removal.
