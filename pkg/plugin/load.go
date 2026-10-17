package plugin

import (
	"github.com/jjyr/pot/pkg/core"
)

// LoadPlugin uses the common package loader and snapshots only tool capability.
// Execution revalidates the exact tool file set and digest.
func LoadPlugin(directory string) (core.PluginBundle, error) {
	p, err := Load(directory)
	if err != nil {
		return core.PluginBundle{}, err
	}
	return p.ToolBundle()
}
