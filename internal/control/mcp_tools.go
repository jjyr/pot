package control

import (
	"encoding/json"
	"strings"

	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/plugin"
)

// Both MCP connection kinds use the same model-facing adapter. This projection
// belongs to the product tool interface, not to the MCP wire protocol package.
func mcpModelTool(id, description string, tools []MCPTool) (modelprotocol.ToolDefinition, []string) {
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name)
		description += tool.Name + ": " + tool.Description + " Schema: " + string(tool.InputSchema) + "\n"
	}
	schema, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"tool": map[string]any{"type": "string", "enum": names}, "arguments": map[string]any{"type": "string", "description": "JSON object containing the selected tool's arguments", "maxLength": 131072}}, "required": []string{"tool", "arguments"}, "additionalProperties": false})
	return modelprotocol.ToolDefinition{Name: "mcp_" + strings.ReplaceAll(id, "-", "_"), Description: description, InputSchema: schema}, names
}
func pluginModelToolNames(p plugin.Package) []string {
	names := []string{}
	if tools := p.Manifest.Contributes.Tools; tools != nil {
		for _, raw := range tools.Tools {
			var tool struct{ Name string }
			_ = json.Unmarshal(raw, &tool)
			names = append(names, tool.Name)
		}
	}
	if p.Manifest.Contributes.MCP != nil {
		names = append(names, "mcp_"+strings.ReplaceAll(p.Manifest.ID, "-", "_"))
	}
	return names
}
