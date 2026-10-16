package core

import (
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"strings"
)

type PluginManifest struct {
	SchemaVersion uint32            `json:"schema_version"`
	ID            string            `json:"id"`
	Version       string            `json:"version"`
	Runtime       string            `json:"runtime"`
	Execution     string            `json:"execution"`
	Entrypoint    string            `json:"entrypoint"`
	Files         []string          `json:"files"`
	Requirements  []string          `json:"requirements"`
	Tools         []json.RawMessage `json:"tools"`
}
type PluginBundle struct {
	Manifest PluginManifest    `json:"manifest"`
	Files    map[string]string `json:"files"`
	Digest   string            `json:"digest"`
}
type PluginIdentity struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Digest  string `json:"digest"`
}
type PluginConfig struct {
	Enabled bool          `json:"enabled"`
	Source  *string       `json:"source,omitempty"`
	Bundle  *PluginBundle `json:"bundle,omitempty"`
}

func (p *PluginConfig) UnmarshalJSON(data []byte) error {
	type plain PluginConfig
	value := plain{Enabled: true}
	if err := DecodeJSON(data, &value); err != nil {
		return err
	}
	*p = PluginConfig(value)
	return nil
}
func (b PluginBundle) Identity() PluginIdentity {
	return PluginIdentity{b.Manifest.ID, b.Manifest.Version, b.Digest}
}
func (b PluginBundle) Hash() (string, error) { return HashJSON([]any{b.Manifest, b.Files}) }
func SafeRelativePath(value string) bool {
	if value == "" || strings.ContainsAny(value, "\\\x00") || strings.HasPrefix(value, "/") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return path.Clean(value) == value
}
func PluginToolName(name string) bool {
	if name == "" || len(name) > 64 || strings.HasPrefix(name, "pot_") {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return !slices.Contains([]string{"exec_command", "write_stdin", "apply_patch", "plugin_call", "browser", "exec", "wait", "read_agent_instructions", "update_agent_instructions", "create_task", "list_tasks", "get_task", "guide_task", "interrupt", "cancel_task", "send_message", "wait_task", "list_assistants", "send_assistant_message", "list_schedules", "get_schedule", "manage_schedule", "list_execution_targets"}, name)
}
func (b PluginBundle) Validate() error {
	m := b.Manifest
	if m.SchemaVersion != 1 || m.Runtime != "node" || !slices.Contains([]string{"sandbox", "host_or_sandbox"}, m.Execution) || m.ID == "" || len(m.ID) > 64 || m.Version == "" || len(m.Version) > 64 {
		return fmt.Errorf("invalid plugin contract")
	}
	for _, c := range m.ID {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
			return fmt.Errorf("invalid plugin ID")
		}
	}
	if len(m.Files) > 32 || len(m.Files) != len(b.Files) || !slices.Contains(m.Files, m.Entrypoint) {
		return fmt.Errorf("plugin file set mismatch")
	}
	seen := map[string]bool{}
	size := 0
	for _, p := range m.Files {
		data, exists := b.Files[p]
		if !exists || seen[p] || !SafeRelativePath(p) {
			return fmt.Errorf("invalid plugin file")
		}
		seen[p] = true
		size += len(data)
	}
	if size > 512*1024 {
		return fmt.Errorf("plugin package too large")
	}
	hash, err := b.Hash()
	if err != nil {
		return err
	}
	if hash != b.Digest {
		return fmt.Errorf("plugin digest mismatch")
	}
	if len(m.Tools) == 0 || len(m.Tools) > 16 {
		return fmt.Errorf("invalid plugin tool count")
	}
	names := map[string]bool{}
	for _, data := range m.Tools {
		var tool struct {
			Name        string `json:"name" required:"true"`
			Description string `json:"description" required:"true"`
			InputSchema struct {
				Type                 string                     `json:"type" required:"true"`
				Properties           map[string]json.RawMessage `json:"properties" required:"true"`
				Required             []string                   `json:"required,omitempty"`
				AdditionalProperties bool                       `json:"additionalProperties" required:"true"`
			} `json:"inputSchema" required:"true"`
		}
		if err := DecodeJSON(data, &tool); err != nil {
			return err
		}
		if !PluginToolName(tool.Name) || names[tool.Name] || tool.InputSchema.Type != "object" || tool.InputSchema.AdditionalProperties {
			return fmt.Errorf("invalid plugin tool schema/name")
		}
		names[tool.Name] = true
		for key := range tool.InputSchema.Properties {
			if key == "execution_target" || key == "sandbox_id" {
				return fmt.Errorf("plugin schema conflicts with routing")
			}
		}
		for _, key := range tool.InputSchema.Required {
			if _, exists := tool.InputSchema.Properties[key]; !exists {
				return fmt.Errorf("unknown required plugin argument")
			}
		}
		for _, schema := range tool.InputSchema.Properties {
			var property struct {
				Type        string       `json:"type"`
				Description string       `json:"description,omitempty"`
				MaxLength   *uint64      `json:"maxLength,omitempty"`
				Minimum     *json.Number `json:"minimum,omitempty"`
				Maximum     *json.Number `json:"maximum,omitempty"`
				Enum        []any        `json:"enum,omitempty"`
			}
			if err := DecodeJSON(schema, &property); err != nil {
				return err
			}
			if !slices.Contains([]string{"string", "boolean", "integer", "number"}, property.Type) || property.MaxLength != nil && property.Type != "string" || (property.Minimum != nil || property.Maximum != nil) && !slices.Contains([]string{"integer", "number"}, property.Type) {
				return fmt.Errorf("unsupported plugin property")
			}
		}
	}
	return nil
}
