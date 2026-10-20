# Standard model providers

This bundled, declarative plugin contributes standard providers and Custom API
through the same [model connection contract](../../docs/plugins.md#model-connection-contributions)
as optional provider plugins.

`directory_provider: "*"` expands supported models.dev entries into named types.
Catalog reads and inference reuse Pot's shared protocol implementations without
starting a plugin process. Public metadata remains shared; see
[model connections and metadata](../../docs/model-directory.md).

Hosts without a packaged plugin directory use the embedded copy of this manifest.
Installed copies take precedence. Both obey the same Extensions enablement setting.
