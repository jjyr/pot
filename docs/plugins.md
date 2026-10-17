# Tool plugin packages

A plugin is a trusted locally installed directory with a schema-2 `plugin.json`
manifest. `pkg/plugin` validates identity and confined file access without
starting processes. Only `contributes.tools` is supported; removed execution
Provider/resource capabilities and unknown fields fail explicitly.

```text
Resources/plugins/<package>/plugin.json + declared files
                        |
                pkg/plugin loader
                        |
              digest-pinned tool bundle
                        |
          Control execution authorization
                        |
        destination Go executor -> Node tool process
```

## Manifest and execution

`contributes.tools` declares `runtime: "node"`, `execution: "runtime"`,
`entrypoint`, `files`, `requirements` and `tools`. Optional `credentials` name
connected services injected into the tool process; `session_mode: "call"` closes
the process after each invocation. See [Credentials](credentials.md) and the
[bundled manifest](../plugins/computer-use/plugin.json).

Input schemas support flat scalar values and bounded string arrays. Paths are
confined relative package paths without symlinks. Tool snapshots contain only
the declared UTF-8 files, limited to 32 files / 512 KiB, and are digest-pinned.
Executors reject extra payloads and unknown capabilities. Tool installation does
not grant execution authority; each call uses [machine authorization](permissions.md).

## Discovery, synchronization and packaging

Runtime loads packages from `PluginRoot`, defaulting to
`ResourceDirectory/plugins`. Installed tools are enabled by default; Provider
connection forms only configure models and credentials. Main turns and prepared
Tasks retain their resource snapshots. Before a Task first prepares, the original
Runner synchronizes selected current tool files and Skills through Control's
run-bound authenticated interface. Project instructions are read on the destination.

`pkg/pluginhost` owns bounded Node process sessions. Tool processes follow their
execution scope and bundle digest. Cancellation, ownership checks, operation
receipts and uncertainty fences apply to plugin calls as to built-in tools.

`mise run build:plugins` packages only the manifests and declared tool files.
Packages contain no VM engines, images or guest bootstrap. Stop Runtime before
replacing a complete package. Live reload, automatic installation, remote MCP
connections and a plugin marketplace remain outside this contract.
