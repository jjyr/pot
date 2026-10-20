# Standard model providers

This declarative plugin contributes the standard model connections and Custom API
connection. It is distributed with Pot and enabled through the same Extensions
setting as other plugins.

The `directory_provider: "*"` definition expands supported model-directory entries
into named connection types. Public metadata remains shared in Pot's models.dev
snapshot. Standard HTTP catalog reads and all model inference use Pot's shared
protocol implementations, without starting a plugin process.

The embedded copy of this manifest supplies the same contribution in headless
embeddings that do not configure a plugin resource directory. A physically
installed copy takes precedence. Disabling the package gates both copies.
