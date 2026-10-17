package resource

import (
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
)

// LoadPlugin uses the common package loader and snapshots only tool capability.
// Execution revalidates the exact tool file set and digest.
func LoadPlugin(directory string) (core.PluginBundle, error) {
	p, err := plugin.Load(directory)
	if err != nil {
		return core.PluginBundle{}, err
	}
	return p.ToolBundle()
}
