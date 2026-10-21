package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/pluginhost"
)

func (s *Service) invokePluginMCP(ctx context.Context, p plugin.Package, name string, args json.RawMessage) ([]pluginhost.MCPTool, pluginhost.MCPResult, error) {
	identity, err := p.MCPIdentity()
	if err != nil {
		return nil, pluginhost.MCPResult{}, err
	}
	checkPackage := func() error {
		fresh, e := plugin.Load(p.Root)
		if e != nil {
			return e
		}
		current, e := fresh.MCPIdentity()
		if e != nil || current != identity {
			return errors.New("plugin_changed")
		}
		return nil
	}
	if err = checkPackage(); err != nil {
		return nil, pluginhost.MCPResult{}, err
	}
	catalog, err := s.Integrations.List(ctx)
	if err != nil {
		return nil, pluginhost.MCPResult{}, err
	}
	before, _ := json.Marshal(catalog)
	accounts := map[string]string{}
	env := map[string]string{}
	for key, binding := range p.Manifest.Contributes.MCP.Credentials {
		for _, item := range catalog {
			if item.ID == binding.Service && item.Account != nil {
				accounts[binding.Service] = item.Account.Identity.ID
			}
		}
		if name != "" && accounts[binding.Service] != "" {
			scope := "plugin-mcp-" + p.Manifest.ID
			value, e := s.Integrations.Credential(ctx, scope, binding.Service, false)
			defer s.Integrations.Forget(scope)
			if e == nil {
				env[key] = value
			}
		}
	}
	worker, err := os.Executable()
	if err != nil {
		return nil, pluginhost.MCPResult{}, err
	}
	host := func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
		// The package is fixed by the supervised connection; no Control/plugin identity
		// comes from MCP arguments. Every assistant may manage every subscription.
		if e := checkPackage(); e != nil {
			return nil, e
		}
		if strings.HasPrefix(method, "pot/background/") && p.Manifest.Contributes.Background == nil {
			return nil, errors.New("plugin_background_unavailable")
		}
		current, e := s.Integrations.List(ctx)
		now, _ := json.Marshal(current)
		if e != nil || string(now) != string(before) {
			return nil, errors.New("integration_changed")
		}
		switch method {
		case "pot/context":
			var empty struct{}
			if e = core.DecodeJSON(raw, &empty); e != nil {
				return nil, e
			}
			defs, e := s.Registry.Definitions(ctx)
			return map[string]any{"accounts": accounts, "assistants": defs}, e
		case "pot/background/list":
			var empty struct{}
			if e = core.DecodeJSON(raw, &empty); e != nil {
				return nil, e
			}
			rows, e := s.Mailbox.Backgrounds(ctx)
			items := []assistant.BackgroundBinding{}
			for _, b := range rows {
				if b.PluginID == p.Manifest.ID {
					items = append(items, b)
				}
			}
			return map[string]any{"items": items}, e
		case "pot/background/write":
			var command struct {
				RequestID string          `json:"request_id"`
				ID        string          `json:"id"`
				Assistant string          `json:"assistant"`
				Enabled   bool            `json:"enabled"`
				Config    json.RawMessage `json:"config"`
				Revision  int64           `json:"revision"`
				InputHash string          `json:"input_hash"`
			}
			if e = core.DecodeJSON(raw, &command); e != nil {
				return nil, e
			}
			if command.Assistant != "" {
				if e = s.validateNotificationTarget(ctx, command.Assistant); e != nil {
					return nil, e
				}
			}
			// Reject attempts to rebind an existing instance owned by another package.
			rows, e := s.Mailbox.Backgrounds(ctx)
			if e != nil {
				return nil, e
			}
			for _, b := range rows {
				if b.ID == command.ID && b.PluginID != p.Manifest.ID {
					return nil, errors.New("plugin_instance_mismatch")
				}
			}
			b := assistant.BackgroundBinding{ID: command.ID, PluginID: p.Manifest.ID, Enabled: command.Enabled, Assistant: command.Assistant, Config: command.Config, Revision: command.Revision, InputHash: command.InputHash}
			e = s.Mailbox.ConfigureBackgroundRequest(ctx, b, command.RequestID)
			if e == nil {
				s.Changes.Notify("plugin-notifications")
			}
			return map[string]bool{"saved": e == nil}, e
		case "pot/background/receipt":
			var command struct {
				ID        string `json:"id"`
				RequestID string `json:"request_id"`
				InputHash string `json:"input_hash"`
			}
			if e = core.DecodeJSON(raw, &command); e != nil {
				return nil, e
			}
			applied, e := s.Mailbox.BackgroundWriteApplied(ctx, p.Manifest.ID, command.ID, command.RequestID, command.InputHash)
			return map[string]bool{"applied": applied}, e
		case "pot/background/initialize":
			var command struct {
				ID        string          `json:"id"`
				Assistant string          `json:"assistant"`
				Enabled   bool            `json:"enabled"`
				Config    json.RawMessage `json:"config"`
			}
			if e = core.DecodeJSON(raw, &command); e != nil {
				return nil, e
			}
			if command.Assistant != "" {
				if e = s.validateNotificationTarget(ctx, command.Assistant); e != nil {
					return nil, e
				}
			}
			e = s.Mailbox.InitializeBackground(ctx, p.Manifest.ID, &assistant.BackgroundBinding{ID: command.ID, PluginID: p.Manifest.ID, Enabled: command.Enabled, Assistant: command.Assistant, Config: command.Config})
			return map[string]bool{"initialized": e == nil}, e
		}
		return nil, errors.New("unsupported_plugin_host_method")
	}
	tools, result, err := pluginhost.RunMCP(ctx, s.options.Node, p, env, []string{worker}, name, args, host)
	if err != nil {
		return tools, result, err
	}
	if err = checkPackage(); err != nil {
		return nil, pluginhost.MCPResult{}, err
	}
	after, err := s.Integrations.List(ctx)
	now, _ := json.Marshal(after)
	if err != nil || string(now) != string(before) {
		return nil, pluginhost.MCPResult{}, errors.New("integration_changed")
	}
	return tools, result, nil
}

func (s *Service) pluginMCPTools(ctx context.Context, dispatch map[string]agent.Dispatch) ([]modelprotocol.ToolDefinition, error) {
	packages, err := s.installedPlugins()
	if err != nil {
		return nil, err
	}
	out := []modelprotocol.ToolDefinition{}
	for _, p := range packages {
		if p.Manifest.Contributes.MCP == nil {
			continue
		}
		enabled, e := s.enabled(ctx, "plugins", p.Manifest.ID)
		if e != nil {
			return nil, e
		}
		if !enabled {
			continue
		}
		identity, e := p.MCPIdentity()
		if e != nil {
			return nil, e
		}
		tools, _, e := s.invokePluginMCP(ctx, p, "", nil)
		if e != nil {
			return nil, e
		}
		names := []string{}
		description := "Call this installed plugin's MCP server. Supply a tool name and its arguments as a JSON object string. Tools:\n"
		for _, tool := range tools {
			names = append(names, tool.Name)
			description += tool.Name + ": " + tool.Description + " Schema: " + string(tool.InputSchema) + "\n"
		}
		schema, _ := json.Marshal(map[string]any{"type": "object", "properties": map[string]any{"tool": map[string]any{"type": "string", "enum": names}, "arguments": map[string]any{"type": "string", "maxLength": 131072}}, "required": []string{"tool", "arguments"}, "additionalProperties": false})
		name := "mcp_" + strings.ReplaceAll(p.Manifest.ID, "-", "_")
		if dispatch[name] != nil {
			return nil, errors.New("MCP tool namespace collision")
		}
		out = append(out, modelprotocol.ToolDefinition{Name: name, Description: description, InputSchema: schema})
		dispatch[name] = func(ctx context.Context, execution core.ExecutionContext, call modelprotocol.ToolCall, emit func(core.Update) error) (json.RawMessage, error) {
			fresh, e := plugin.Load(p.Root)
			if e != nil {
				return nil, e
			}
			current, e := fresh.MCPIdentity()
			if e != nil || current != identity {
				return nil, errors.New("plugin_changed")
			}
			enabled, e := s.enabled(ctx, "plugins", p.Manifest.ID)
			if e != nil || !enabled {
				return nil, errors.New("plugin_disabled")
			}
			var input struct {
				Tool      string `json:"tool"`
				Arguments string `json:"arguments"`
			}
			if e = core.DecodeJSON(call.Arguments, &input); e != nil {
				return nil, e
			}
			var object map[string]json.RawMessage
			if e = core.DecodeJSON([]byte(input.Arguments), &object); e != nil {
				return nil, e
			}
			_, result, e := s.invokePluginMCP(ctx, p, input.Tool, json.RawMessage(input.Arguments))
			if e != nil {
				return nil, e
			}
			return json.Marshal(result)
		}
	}
	return out, nil
}
