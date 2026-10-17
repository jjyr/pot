# Sandbox Provider capability protocol

Sandbox Providers are capabilities of ordinary [Plugin packages](plugins.md).
`pkg/sandboxprovider` loads those capabilities from already discovered local
packages, supervises their host processes and adapts their domain-specific RPC to
`sandbox.Provider`. It does not define a second plugin package format.

```text
pkg/plugin: package identity, manifest, paths and capability declarations
   |
pkg/sandboxprovider: Provider process and private RPC
   |                       |
Sandbox Manager        Resource Manager
policy/leases/receipts  consent/verification/cache
   |                       |
   +--- bound Provider instance and verified resources ---+
                            |
                     Podman / Apple VM
```

Image identity, guest bootstrap, workspace layout, rebuild and desktop transport
belong to the Provider implementation. Core retains authorization, durable state,
resource consent and verified caching. Provider implementation sources are private
to `plugins/internal/local`; core cannot link them. Tool capabilities execute
inside an already authorized Host or Sandbox through their own Node host.

## Installed capabilities

A complete package contains `plugin.json` and its declared capability payload:

```text
Resources/plugins/podman/
  plugin.json
  catalog.json
  bin/provider

Resources/plugins/apple-vm/
  plugin.json
  bin/provider
  bin/PotMacOSHelper
  bootstrap/
    pot-tool-executor
    provision.sh
    ...
```

Runtime starts only host-supported Provider capabilities for their handshake.
Discovery starts no engine or guest and downloads nothing. Metadata queries use
immutable handshake fields: the Provider identity and actual host OS, default
CPU/memory/disk resources, workspace template, guest workspace, cleanup policy,
host mapping support and optional desktop capabilities. The handshake identity
must match the capability declaration. No Provider ID is special in core.

## Process protocol

A Go Provider capability implements `sandbox.Provider` and optionally
`sandbox.DesktopProvider`, then calls `sandboxprovider.Serve(factory)`. The factory
receives only the Runtime execution root and trusted Skill roots; it registers
an adapter without starting guests. Other languages can implement the same
protocol.

The host creates a private temporary directory and writes one JSON bootstrap
line to the child's stdin: protocol `version: 1`, `config`, private `socket` path
and random bearer `token`. The child creates a mode-0600 Unix socket and writes
one JSON handshake line to stdout. Both bootstrap and handshake are bounded to
64 KiB. Stdin remains open as the parent-liveness channel. Provider subprocesses
receive a restricted environment, including normal OS paths, locale and proxy
settings, without inherited model credential variables. Plugins retain the
host user's OS privileges and are not a security sandbox.

RPC uses `POST /rpc` over that socket with the private bearer token. Each JSON
request carries `version`, a unique `id`, `method`, and the relevant instance,
resources, prepared bundle, guest operation/input or timeout. Methods are:

- `validate`, `bind`, `lifecycle`, `health`, `request`;
- `desktop_prepare`, `desktop_metrics`, `desktop_browser`;
- `shutdown`.

Lifecycle actions include `status`, `prepare`, `start`, `stop`, `delete` and
`rebuild`; each provider explicitly rejects unsupported actions. A rebuild must
reset provider-owned development state while retaining Task files and return
confirmed stopped state. Core then advances its manager epoch.

Responses echo protocol version and request ID and carry exactly one of `result`
or `error`. Errors preserve the `cleanup_unconfirmed` classification. Messages
are bounded to 9 MiB, including the envelope around an up-to-8-MiB guest response.
Cancellation propagates through the HTTP request context. Connections are fresh
per call, and transport failures never retry an operation or restart a plugin.
Lost, truncated or mismatched acknowledgements retain uncertain cleanup state.
After process loss, restart Runtime and explicitly reconcile quarantined state.

`POST /desktop` uses the same authenticated request envelope with
`method: "desktop_open"` and upgrades to `pot-provider-desktop-v1`. Its body is a
private duplex byte stream; guest endpoints and credentials remain in the
provider process. Sandbox Manager retains tickets, input arbitration and leases.

Orderly shutdown calls the provider's shutdown operation and waits for process
exit. Parent loss closes stdin and asks the child to shut down its resources.
An unresponsive process is killed at the shutdown deadline and returns uncertain
cleanup; terminating the host process is never proof that a guest stopped.

## Prepared resources

Catalog schema 3 declares provider/architecture defaults, immutable resource
records and bundles. Bundles name a provider, architecture, HTTPS release
repository, logical resource roles and opaque JSON metadata. Resources retain
exact size, SHA-256, release tag, filename and extraction format. Core supports
verified raw payloads and OCI archives; providers interpret the resulting paths.
A raw payload appears as `payload` inside its verified resource directory.

The prepared bundle contains `bundle_id`, `provider`, `architecture`, a `paths`
map keyed by logical role and unchanged `metadata`. Cross-provider binding is
rejected. Resource and bundle IDs must be unique across installed catalogs;
third-party packages should use provider-specific IDs. Status exposes
`default_bundles` by provider and identifies each bundle's provider/architecture.
A provider with bundles but no architecture default requires explicit owner
bundle selection; it cannot bypass preparation or consent. See
[resource delivery](sandbox-resources.md) for consent and durable pins.
