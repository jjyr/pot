package core

import (
	"errors"
	"slices"
)

// PluginMCP is an installed Control-local stdio MCP server. Its files and
// credentials never travel in Runtime tool bundles. Configuration is opaque.
type PluginMCP struct {
	Runtime       string                  `json:"runtime"`
	Execution     string                  `json:"execution"`
	Entrypoint    string                  `json:"entrypoint"`
	Files         []string                `json:"files"`
	Credentials   CredentialBindings      `json:"credentials,omitempty"`
	Configuration *PluginMCPConfiguration `json:"configuration,omitempty"`
}
type PluginMCPConfiguration struct {
	List   string `json:"list"`
	Create string `json:"create"`
	Update string `json:"update"`
	Delete string `json:"delete"`
}

func (c PluginMCP) Validate() error {
	if c.Runtime != "node" || c.Execution != "control" || len(c.Files) == 0 || len(c.Files) > 32 || !slices.Contains(c.Files, c.Entrypoint) {
		return errors.New("invalid plugin MCP capability")
	}
	seen := map[string]bool{}
	for _, f := range c.Files {
		if !SafeRelativePath(f) || seen[f] {
			return errors.New("invalid plugin MCP file")
		}
		seen[f] = true
	}
	if c.Configuration != nil {
		for _, name := range []string{c.Configuration.List, c.Configuration.Create, c.Configuration.Update, c.Configuration.Delete} {
			if !PluginToolName(name) {
				return errors.New("invalid MCP configuration tool")
			}
		}
	}
	return c.Credentials.Validate()
}
