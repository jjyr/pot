// Package pluginassets embeds trusted declaration assets only. It contains no
// provider implementation or executable adapter code; those stay behind the
// plugin package loader and Control-local execution boundary.
package pluginassets

import _ "embed"

//go:embed standard-model-providers/plugin.json
var StandardModelProviders []byte
