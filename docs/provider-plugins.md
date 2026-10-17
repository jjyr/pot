# Sandbox Provider plugins

Sandbox Providers are trusted host executables installed independently of Pot's
core. They create and connect execution environments. Node tool plugins such as
`computer-use` execute tools inside an already authorized Host or Sandbox; the
two plugin contracts and installation directories are separate.

## Ownership

```text
Control: policy, approvals, Agent and Task dispatch
   |
Sandbox Manager: definitions, selection, scopes, leases, receipts, recovery
   |                       |
Provider Plugin Host       Resource Manager: consent, verification, cache, pins
   |                       |
   +--- versioned private RPC ---+
               |
       installed provider package
       implementation + resource catalog + guest/bootstrap definitions
               |
       engine / VM / guest broker / executor / tool plugins / desktop
```

`pkg/providerplugin` discovers packages, checks the protocol handshake and owns
provider processes and private Unix sockets. `pkg/sandbox` retains authorization
boundaries, durable state and lifecycle admission. `pkg/resourcecontrol` and
`pkg/sandboxresources` prepare provider-declared resources without interpreting
image identity, guest OS or bootstrap metadata. Provider code owns import,
boot, guest authentication, workspace layout, rebuild and desktop transport.

The bundled entry points are `providers/podman/cmd` and
`providers/apple-vm/cmd`. Their platform implementation is shared privately under
`providers/internal/local`; core packages cannot link that implementation.
Podman owns `providers/podman/catalog.json`, `image/`, `podman_build.py`,
`build-image`, `export-resources` and `verify-resources`. Apple VM owns its
`bootstrap/` and signed Swift `helper/` sources. Image bytes remain separately
distributed and are not included in plugin or App packages.

## Installed package

Runtime reads `ResourceDirectory/providers/` at startup. Only owner-installed
local packages are loaded; model arguments and connected Workers cannot add
providers. A Podman package contains:

```text
Resources/providers/podman/
  provider.json
  catalog.json
  bin/provider

Resources/providers/apple-vm/
  provider.json
  bin/provider
  bin/PotMacOSHelper
  bootstrap/
    pot-tool-executor
    provision.sh
    ...
```

The manifest is a bounded UTF-8 JSON file:

```json
{
  "schema_version": 1,
  "id": "podman",
  "version": "1.0.0",
  "host_os": ["darwin", "linux"],
  "executable": "bin/provider",
  "catalog": "catalog.json",
  "default_shared": true
}
```

`args` is an optional argument array; `catalog` is optional for providers such as
owner-enrolled macOS VMs. Paths must be confined relative paths without symlinks;
the executable must be a regular executable file. Unsupported host OS packages
are skipped. Duplicate IDs, conflicting catalog identities, incompatible
protocol versions and ambiguous shared defaults fail startup explicitly. At
most one installed manifest can set `default_shared`; it must advertise shared
execution. The embedding API can explicitly override the selected default.

Discovery starts host plugin processes for their handshake, without preparing an
engine, downloading resources or booting a guest. Descriptor and workspace
queries then use immutable handshake metadata. The handshake must identify the
manifest's provider and actual host OS, declare default CPU/memory/disk resources,
a workspace template inside the execution root, an absolute guest workspace,
cleanup policy and optional desktop capabilities. Host mapping support is an
explicit provider capability. No provider ID is special in Sandbox Manager.

## Process protocol

A Go provider implements `sandbox.Provider` and optionally
`sandbox.DesktopProvider`, then calls `providerplugin.Serve(factory)`. The factory
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

## Resource catalogs and upgrades

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

Stop Runtime before replacing or removing installed packages. Packages are
loaded once; live replacement/hot reload and automatic plugin downloads are not
implemented. Retain catalog records for existing Sandbox pins when publishing a
new plugin default. Existing pins retain their bundle; only new definitions use
the new default. A plugin/catalog can be repackaged without rebuilding Pot core.
Build and packaging commands belong to [Development](development.md).
