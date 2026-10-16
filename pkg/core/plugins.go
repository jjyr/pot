package core

import (
	"encoding/json"
	"fmt"
	"math"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// ValidateInput applies the deliberately small scalar-schema plugin contract.
// It rejects undeclared fields before the retained Node host is invoked.
func (b PluginBundle) ValidateInput(name string, input map[string]any) error {
	if input == nil {
		return fmt.Errorf("plugin input must be an object")
	}
	var schema map[string]any
	for _, raw := range b.Manifest.Tools {
		var tool map[string]any
		if err := DecodeJSON(raw, &tool); err != nil {
			return err
		}
		if tool["name"] == name {
			schema, _ = tool["inputSchema"].(map[string]any)
			break
		}
	}
	if schema == nil {
		return fmt.Errorf("unknown plugin tool")
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return fmt.Errorf("invalid plugin schema")
	}
	for _, key := range asArray(schema["required"]) {
		name, ok := key.(string)
		if !ok {
			return fmt.Errorf("invalid required plugin argument")
		}
		if _, present := input[name]; !present {
			return fmt.Errorf("missing plugin argument %s", key)
		}
	}
	for key, value := range input {
		property, exists := properties[key].(map[string]any)
		if !exists {
			return fmt.Errorf("unknown plugin argument %s", key)
		}
		numeric, number := pluginNumber(value)
		switch property["type"] {
		case "string":
			if _, ok := value.(string); !ok {
				return fmt.Errorf("invalid plugin string %s", key)
			}
		case "boolean":
			if _, ok := value.(bool); !ok {
				return fmt.Errorf("invalid plugin boolean %s", key)
			}
		case "integer":
			if !number || math.Trunc(numeric) != numeric || numeric < math.MinInt64 || numeric >= -float64(math.MinInt64) {
				return fmt.Errorf("invalid plugin integer %s", key)
			}
		case "number":
			if !number {
				return fmt.Errorf("invalid plugin number %s", key)
			}
		default:
			return fmt.Errorf("unsupported plugin argument type")
		}
		if bound, ok := pluginNumber(property["maxLength"]); ok {
			text, isString := value.(string)
			if !isString || float64(len(text)) > bound {
				return fmt.Errorf("plugin string too long")
			}
		}
		if bound, ok := pluginNumber(property["minimum"]); ok && (!number || numeric < bound) {
			return fmt.Errorf("plugin argument below minimum")
		}
		if bound, ok := pluginNumber(property["maximum"]); ok && (!number || numeric > bound) {
			return fmt.Errorf("plugin argument above maximum")
		}
		if choices, ok := property["enum"].([]any); ok {
			found := false
			for _, choice := range choices {
				a, aok := pluginNumber(choice)
				if aok && number && a == numeric || reflect.DeepEqual(choice, value) {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("invalid plugin enum value")
			}
		}
	}
	return nil
}
func asArray(value any) []any { values, _ := value.([]any); return values }
func pluginNumber(value any) (float64, bool) {
	var n float64
	switch v := value.(type) {
	case json.Number:
		var err error
		n, err = strconv.ParseFloat(v.String(), 64)
		if err != nil {
			return 0, false
		}
	case float64:
		n = v
	case float32:
		n = float64(v)
	case int:
		n = float64(v)
	case int64:
		n = float64(v)
	case uint64:
		n = float64(v)
	case int32:
		n = float64(v)
	case uint32:
		n = float64(v)
	default:
		return 0, false
	}
	return n, !math.IsNaN(n) && !math.IsInf(n, 0)
}

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
