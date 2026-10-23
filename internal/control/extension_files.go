package control

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/instance"
)

type extensionSetting struct {
	Enabled  bool   `json:"enabled"`
	Revision int64  `json:"revision"`
	Source   string `json:"source,omitempty"`
}
type extensionFile struct {
	Version int                         `json:"version"`
	Plugins map[string]extensionSetting `json:"plugins"`
	Skills  map[string]extensionSetting `json:"skills"`
}
type mcpFile struct {
	Version int         `json:"version"`
	Servers []MCPServer `json:"servers"`
}

func readConfig(path string, value any) error {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	return core.DecodeJSON(data, value)
}
func readExtensionFile(root string) (extensionFile, error) {
	f := extensionFile{Version: 1, Plugins: map[string]extensionSetting{}, Skills: map[string]extensionSetting{}}
	err := readConfig(filepath.Join(root, "extensions.json"), &f)
	if err == nil && (f.Version != 1 || f.Plugins == nil || f.Skills == nil) {
		err = errors.New("invalid extension configuration")
	}
	return f, err
}
func (f extensionFile) settings(kind string) map[string]extensionSetting {
	if kind == "plugins" {
		return f.Plugins
	}
	return f.Skills
}
func (s *Service) saveExtensionSetting(kind, id string, value *extensionSetting) error {
	f, err := readExtensionFile(s.options.Root)
	if err != nil {
		return err
	}
	if value == nil {
		delete(f.settings(kind), id)
	} else {
		f.settings(kind)[id] = *value
	}
	return instance.WriteJSON(filepath.Join(s.options.Root, "extensions.json"), f)
}
func (s *Service) saveMCP(server MCPServer, remove bool) error {
	servers, err := s.mcpServers(context.Background())
	if err != nil {
		return err
	}
	found := false
	result := []MCPServer{}
	for _, old := range servers {
		if old.ID == server.ID {
			found = true
			if !remove {
				result = append(result, server)
			}
		} else {
			result = append(result, old)
		}
	}
	if !found && !remove {
		result = append(result, server)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return instance.WriteJSON(filepath.Join(s.options.Root, "mcp.json"), mcpFile{Version: 1, Servers: result})
}
