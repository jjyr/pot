package plugin

import (
	"errors"
	"github.com/jjyr/pot/pkg/core"
)

// Definition is the installed, file-owned package contract. Execution snapshots
// use a separate immutable projection, preserving existing receipt hashes.
type Definition struct {
	SchemaVersion uint32                   `json:"schema_version"`
	Name          string                   `json:"name"`
	DisplayName   string                   `json:"display_name,omitempty"`
	Version       string                   `json:"version"`
	Contributes   core.PluginContributions `json:"contributes"`
}

func DecodeDefinition(raw []byte) (Definition, error) {
	var d Definition
	if err := core.DecodeJSON(raw, &d); err != nil {
		return d, err
	}
	if d.SchemaVersion != 3 {
		return d, errors.New("unsupported installed plugin format")
	}
	if err := d.Snapshot().Validate(); err != nil {
		return d, err
	}
	return d, nil
}
func (d Definition) Snapshot() core.PluginManifest {
	return core.PluginManifest{SchemaVersion: 2, ID: d.Name, Version: d.Version, Contributes: d.Contributes}
}
