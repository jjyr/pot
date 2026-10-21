# MCP protocol component

`pkg/mcp` owns MCP sessions, protocol types, version negotiation, tool discovery,
call results, cancellation and transport errors. It has no dependency on Pot's
Control, plugin manifests, credentials, storage or subscription rules. Its Go API
uses the official Go SDK; the Node runtime uses the official JavaScript SDK.
Neither caller implements another JSON-RPC framing or MCP handshake loop.

```text
Control plugin bridge -> pluginhost (supervised Node process) -> pkg/mcp Go client
                                   |                              | MCP stdio
                                   +-> shared Node SDK server <----+
                                            |
                                     plugin tool handlers

Independent MCP settings -> pinned adapter + shared Node SDK -> selected Runtime
                                                               | stdio / HTTP
                                                        independent MCP server

Codex Agent Link host -> pkg/mcp server API -> official Go SDK -> MCP stdio
```

The two SDKs serve different language boundaries. Independent MCP calls keep
running on the selected execution machine under the existing tool authorization,
sandbox, credentials and cleanup supervision. Plugin MCP calls run on the owning
Control, including when a remote Task invokes them. Codex's App Server JSONL
protocol and Agent Link transport remain separate from MCP.

## Go sessions

`ConnectStdio` accepts caller-owned read/write streams and `Options`; `Connect`
accepts a standard SDK transport, including `StreamableClientTransport`. The
caller starts and supervises processes, injects credentials, supplies HTTP policy
and bounds the invocation with a context. The protocol package never selects a
machine or launches an executable.

`Session.ListTools` handles pagination, repeated cursors, duplicate names and
catalogue bounds. `Session.CallTool` accepts an object encoded as JSON and returns
the SDK's complete `CallToolResult`, preserving structured content, metadata and
standard content variants. Tool errors (`isError`) remain distinct from JSON-RPC
errors and transport failures. Calls are never automatically retried. Go sessions
disable the SDK's automatic multi-round-trip tool handling. SDK transport retry
policy remains explicit at construction; invocation HTTP callers should disable
reconnect retries when they do not support resumable streams.

Clients propose `2025-11-25` and accept supported older versions through the SDK
handshake. Servers negotiate their supported versions rather than rejecting
another supported client's initial proposal. The protocol package reuses SDK
server types and registration through `NewServer` and `AddTool`; business handlers
remain with their owning component.

The SDK's exported `Connection` interface permits a small stdio decorator for
explicitly registered incoming extension methods. This is needed because the
Go SDK does not expose client-side custom receiving-method registration. The
SDK still owns framing, request IDs, standard methods and cancellation. Extension
methods are unavailable before initialization. Unknown methods follow the SDK's
normal method-not-found response. Custom incoming-method interception is not
supported on Streamable HTTP; the unwrapped SDK transport retains its session and
protocol-version hooks there.

Protocol bounds are 128 tools, 128 pages, a 128 KiB catalogue, 64 KiB input schemas,
8 KiB descriptions and 3 MiB stdio frames. Go extension/inspection sessions also
bound the incoming exchange to 8 MiB. Hosts may inspect incoming messages before
any handler or persistence, for example to reject credential disclosure.
Host-specific checks never log those bytes.

## Node assets and packaging

The canonical Node sources are `pkg/mcp/node/runtime.mjs` and `adapter.mjs`.
`node/package-lock.json` pins the official SDK and build dependencies. The
reproducible `assets/node-runtime.mjs` bundle and `assets/NOTICE.txt` are embedded
by Go. Rebuild after changing sources or dependencies:

```sh
mise exec -- npm ci --ignore-scripts --prefix pkg/mcp/node
mise exec -- npm run build --prefix pkg/mcp/node
```

`npm run check --prefix pkg/mcp/node` verifies that both generated assets match
the locked sources without rewriting them.

Runtime tool snapshots include `mcp-adapter.mjs`, `mcp-runtime.mjs` and the
credential-free `config.json`, all under the existing digest/file/size bounds.
They remain self-contained when copied to another Runtime. The thin adapter
translates the flat model tool contract; the shared SDK asset handles protocol IO.
The adapter preserves structured content and metadata as additional tool output.
No npm install, network module resolution or SDK download occurs during calls.

For installed Control plugins, `pluginhost` writes the same embedded runtime to a
private support directory and supplies its file URL as `POT_MCP_RUNTIME`. The
plugin's stdio entrypoint registers its tools and business handler with `serve`.
The host supervises and removes that directory with the invocation. An uncertain
process cleanup retains its support files under the existing cleanup fence.
The shared runtime's third-party notices are included in packaged dependency
notices by `scripts/go-notices`.

## Pot host extension

`pot/background-store` version 1 is a Pot plugin extension, not standard MCP.
`pluginhost` opts into it and independently checks the server's negotiated
capability. Control implements `pot/context`, `pot/background/list`,
`pot/background/initialize`, `pot/background/write` and `pot/background/receipt`.
The bridge binds the installed package, verifies package/account stability and
stores opaque plugin configuration with CAS and request receipts. The protocol
component treats extension names and values as caller-supplied data.

Plugin manifests (`core.PluginMCP`), file identity (`pkg/plugin`), process and
credential handling (`pkg/pluginhost`), saved independent-server configuration,
OAuth and model-tool projection (`internal/control`) remain outside the protocol
package. GitHub subscription CRUD, default initialization, filtering and shared
scan aggregation remain in the GitHub plugin. The host's inbox/checkpoint
transaction remains unchanged. See [plugins](plugins.md#github-notifications) and
[independent MCP connections](extensions.md#independent-mcp-servers).
