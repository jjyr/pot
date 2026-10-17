# Plugin packages

A Plugin is one locally installed package with a `plugin.json` manifest. A package
can contribute tools, Sandbox Providers, resource catalogs, or any combination.
Package identity, version, discovery and confined file access are shared. Each
capability retains its own execution contract and authorization boundary.

```text
Resources/plugins/<package>/plugin.json + capability files
                         |
              pkg/plugin: load and validate
                         |
          +--------------+----------------+
          |              |                |
        tools     sandbox_providers    resources
          |              |                |
   tool-only snapshot  Runtime host    verified delivery
          |              |                |
   Node tool session  Provider RPC <--- prepared paths
          |              |
    Host / Sandbox   Podman / Apple VM
```

## Manifest

Schema 2 replaces the old tool-only manifest. There is no separate Provider
manifest or installation root. For example, the Podman package declares:

```json
{
  "schema_version": 2,
  "id": "podman",
  "version": "1.0.0",
  "contributes": {
    "sandbox_providers": [{
      "id": "podman",
      "host_os": ["darwin", "linux"],
      "executable": "bin/provider",
      "default_shared": true
    }],
    "resources": [{"provider": "podman", "catalog": "catalog.json"}]
  }
}
```

A tool capability is declared under `contributes.tools` with `runtime`,
`execution`, `entrypoint`, `files`, `requirements` and `tools`. Optional
`credentials` bindings name connected services to inject into the tool
process; `session_mode: "call"` closes the process after each invocation. See
[Credentials](credentials.md) for login, single-account ownership and caching. The current driver
requires Node, supports `sandbox` or `host_or_sandbox`, and validates flat scalar
and bounded string-array input schemas. See the [computer-use manifest](../plugins/computer-use/plugin.json)
and [tool execution contract](computer-use.md#plugin-contract-and-lifecycle).

A package may declare multiple `sandbox_providers`. Each entry has a distinct
Provider ID, supported host OS list, executable, optional `args` and
`default_shared`. Package IDs identify installation units; Provider IDs identify
execution backends; tool names identify operations. These identities are not
interchangeable. For example, package `apple-vm` contributes Provider
`macos.apple-virtualization`. Unsupported host capabilities are skipped, without
disabling other capabilities in the package.

Each `resources` entry associates a catalog path with a Provider ID. A catalog-only
image package can supply additional bundles for an installed Provider, without
shipping an executable. Missing referenced Providers, duplicate Provider IDs,
conflicting catalog identities and ambiguous shared defaults fail explicitly.
Catalog contents and verified preparation are described in
[Sandbox resources](sandbox-resources.md).

Unknown capabilities and old manifest schemas fail validation. MCP could be a
separate capability in a future design; it is not implemented or required by
this package format or its current capability drivers.

## Loading and authority

`pkg/plugin` owns the common loader and directory discovery. Manifests are bounded
to 64 KiB. File references must be confined relative paths without symlinks;
executables must be regular executable files. Loading a manifest neither starts
a process nor prepares a guest or download. Installation is trusted local owner
configuration; plugins execute with the owner's OS privileges.

Runtime uses `PluginRoot`, defaulting to `ResourceDirectory/plugins`, for locally
installed packages. `pkg/sandboxprovider.Load` consumes only the discovered Sandbox
and resource capabilities and performs bounded Provider handshakes. The Provider
process protocol belongs to the [Sandbox capability](sandbox-provider-protocol.md),
not to the common package framework. `pkg/pluginhost` remains the Node tool-session
host. There is no common wire protocol imposed on these capability drivers.

Profile `agent.plugins` settings select only tools. A Profile source override can
load a tool capability but never registers a local Provider or resource catalog.
`ToolBundle` projects the tool declaration and its explicitly listed UTF-8 files
into a digest-pinned snapshot. Provider declarations, catalogs, native binaries
and Helpers are excluded. Receiving executors reject bundles containing local
capabilities even if their digest is valid. Tool snapshots retain the 32-file /
512-KiB limit. Task synchronization carries only these snapshots; the destination
Runtime retains ownership of its own installed Providers and resources.

Provider processes live with Runtime. Tool processes live with their authorized
execution target and session. Enabling a tool does not enable a Provider; closing
a tool session does not stop the Sandbox. Approval, leases, receipts and recovery
remain in their existing managers.

## Packaging and upgrades

Bundled source packages live under `plugins/`. Podman owns image recipes, build
and export tooling; Apple VM owns bootstrap and signed Helper sources. Runtime
packages include only required capability payloads, not source build trees or
image bytes. `mise run build:plugins` packages tools and host-supported Providers
through the same build entry point. Commands belong to [Development](development.md).

Stop Runtime before replacing or removing packages. Retain catalog records for
existing Sandbox pins when publishing new defaults. Package replacement does not
rewrite existing Sandbox resource identities. Live reload, automatic installation,
remote MCP connections and a plugin marketplace are not implemented.
