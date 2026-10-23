package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/pluginhost"
)

// MCPTool is the persisted catalogue projection; full wire types live in pkg/mcp.
type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}
type MCPServer struct {
	Auth           string    `json:"auth,omitempty"`
	ClientID       string    `json:"client_id,omitempty"`
	Scope          string    `json:"scope,omitempty"`
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Revision       int64     `json:"revision"`
	Enabled        bool      `json:"enabled"`
	Transport      string    `json:"transport"`
	Command        string    `json:"command,omitempty"`
	Args           []string  `json:"args,omitempty"`
	CWD            string    `json:"cwd,omitempty"`
	URL            string    `json:"url,omitempty"`
	SecretRef      string    `json:"secret_ref,omitempty"`
	HasCredentials bool      `json:"has_credentials"`
	Tools          []MCPTool `json:"tools"`
	DisabledTools  []string  `json:"disabled_tools"`
	Status         string    `json:"status"`
}
type MCPCredentials struct {
	Env     map[string]string `json:"env"`
	Headers map[string]string `json:"headers"`
}
type MCPCommand struct {
	Action      string          `json:"action"`
	Server      MCPServer       `json:"server"`
	Credentials *MCPCredentials `json:"credentials"`
}

func (s *Service) mcpServers(_ context.Context) ([]MCPServer, error) {
	f := mcpFile{Version: 1, Servers: []MCPServer{}}
	if err := readConfig(filepath.Join(s.options.Root, "mcp.json"), &f); err != nil {
		return nil, err
	}
	if f.Version != 1 {
		return nil, errors.New("unsupported MCP configuration")
	}
	seen := map[string]bool{}
	for i := range f.Servers {
		v := &f.Servers[i]
		if err := validateMCP(*v); err != nil {
			return nil, err
		}
		if seen[v.ID] {
			return nil, errors.New("duplicate MCP name")
		}
		seen[v.ID] = true
		v.HasCredentials = v.SecretRef != ""
	}
	return f.Servers, nil
}

func validateMCP(server MCPServer) error {
	if server.Auth != "" && server.Auth != "none" && server.Auth != "oauth" {
		return errors.New("invalid MCP authentication")
	}
	if len(server.ClientID) > 2048 || len(server.Scope) > 2048 {
		return errors.New("OAuth configuration limit")
	}
	if !core.Identifier(server.ID) || len(server.ID) > 32 || strings.HasPrefix(server.ID, "mcp-") || strings.TrimSpace(server.Name) == "" || len(server.Name) > 128 {
		return errors.New("invalid MCP identity")
	}
	if server.Transport == "stdio" {
		if server.Command == "" || strings.ContainsAny(server.Command, "\x00\r\n") || len(server.Command) > 4096 || len(server.Args) > 64 {
			return errors.New("invalid MCP command")
		}
		if server.CWD != "" && !filepath.IsAbs(server.CWD) {
			return errors.New("MCP workdir must be absolute")
		}
		total := len(server.Command) + len(server.CWD)
		for _, arg := range server.Args {
			total += len(arg)
			if total > 64*1024 {
				return errors.New("MCP command size limit")
			}
			if len(arg) > 8192 || strings.ContainsRune(arg, 0) {
				return errors.New("invalid MCP argument")
			}
		}
	} else if server.Transport == "http" {
		u, err := url.Parse(server.URL)
		if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" || len(server.URL) > 4096 || u.Scheme != "https" && u.Scheme != "http" {
			return errors.New("invalid MCP endpoint")
		}
	} else {
		return errors.New("invalid MCP transport")
	}
	return nil
}
func validateMCPCredentials(c MCPCredentials) error {
	if len(c.Env) > 32 || len(c.Headers) > 32 {
		return errors.New("MCP credential limit")
	}
	for name, value := range c.Env {
		if !core.CredentialEnvironmentName(name) || len(value) > 16384 {
			return errors.New("invalid MCP environment")
		}
	}
	for name, value := range c.Headers {
		if name == "" || len(name) > 128 || strings.ContainsAny(name+value, "\r\n\x00") || strings.ContainsAny(name, ": ") || len(value) > 16384 {
			return errors.New("invalid MCP header")
		}
		if slices.Contains([]string{"host", "content-length", "content-type", "accept", "mcp-session-id", "mcp-protocol-version"}, strings.ToLower(name)) {
			return errors.New("reserved MCP header")
		}
	}
	return nil
}
func (s *Service) ManageMCP(ctx context.Context, command MCPCommand) (any, error) {
	var result any
	err := s.Machines.OwnerAction(func() error {
		s.extensionsMu.Lock()
		defer s.extensionsMu.Unlock()
		servers, err := s.mcpServers(ctx)
		if err != nil {
			return err
		}
		var old *MCPServer
		for _, server := range servers {
			if server.ID == command.Server.ID {
				copy := server
				old = &copy
			}
		}
		server := command.Server
		if command.Action != "save" && old == nil {
			return errors.New("MCP unavailable")
		}
		if old != nil && old.Revision != server.Revision {
			return errors.New("MCP configuration changed")
		}
		if old == nil && server.Revision != 0 {
			return errors.New("MCP configuration changed")
		}
		if command.Action == "save" {
			packages, err := s.installedPlugins()
			if err != nil {
				return err
			}
			for _, installed := range packages {
				if slices.Contains(pluginModelToolNames(installed), "mcp_"+strings.ReplaceAll(server.ID, "-", "_")) {
					return errors.New("MCP tool name already registered")
				}
			}
		}
		if command.Action == "save" && old == nil {
			for _, existing := range servers {
				if strings.ReplaceAll(existing.ID, "-", "_") == strings.ReplaceAll(server.ID, "-", "_") {
					return errors.New("MCP tool namespace already exists")
				}
			}
		}
		if command.Action == "remove" {
			err = s.saveMCP(*old, true)
			result = map[string]bool{"saved": err == nil}
			return err
		}
		if command.Action == "test" {
			tools, err := s.discoverMCP(ctx, *old)
			if err != nil {
				old.Status = "unavailable"
				saveErr := s.saveMCP(*old, false)
				s.Changes.Notify("extensions")
				return errors.Join(err, saveErr)
			}
			old.Tools = tools
			old.DisabledTools = slices.DeleteFunc(old.DisabledTools, func(name string) bool {
				return !slices.ContainsFunc(tools, func(tool MCPTool) bool { return tool.Name == name })
			})
			old.Status = "ready"
			server = *old
		} else if command.Action == "toggle" || command.Action == "tools" {
			enabled, disabled := server.Enabled, server.DisabledTools
			server = *old
			if command.Action == "toggle" {
				server.Enabled = enabled
			} else {
				seen := map[string]bool{}
				for _, name := range disabled {
					if seen[name] {
						return errors.New("duplicate MCP tool selection")
					}
					seen[name] = true
					if !slices.ContainsFunc(server.Tools, func(tool MCPTool) bool { return tool.Name == name }) {
						return errors.New("unknown MCP tool")
					}
				}
				server.DisabledTools = disabled
			}
		} else if command.Action == "save" {
			if err = validateMCP(server); err != nil {
				return err
			}
			server.Tools = []MCPTool{}
			server.DisabledTools = []string{}
			server.Status = "pending"
			server.SecretRef = ""
			if old != nil {
				server.SecretRef = old.SecretRef
			}
			if command.Credentials != nil {
				if err = validateMCPCredentials(*command.Credentials); err != nil {
					return err
				}
				raw, _ := json.Marshal(command.Credentials)
				refHash, _ := core.HashJSON([]any{server.ID, time.Now().UnixNano()})
				ref := "mcp-" + refHash[:40]
				if err = s.integrationSecrets.Put(ctx, ref, raw); err != nil {
					return err
				}
				server.SecretRef = ref
			}
		} else {
			return errors.New("unknown MCP action")
		}
		server.Revision++
		err = s.saveMCP(server, false)
		server.SecretRef = ""
		server.HasCredentials = command.Credentials != nil || old != nil && old.HasCredentials
		result = server
		return err
	})
	if err == nil {
		s.Changes.Notify("extensions")
		s.Changes.Notify("assistants")
	}
	return result, err
}
func mcpBundle(server MCPServer) (core.PluginBundle, error) {
	selected := []MCPTool{}
	for _, tool := range server.Tools {
		if !slices.Contains(server.DisabledTools, tool.Name) {
			selected = append(selected, tool)
		}
	}
	definition, names := mcpModelTool(server.ID, "MCP server "+server.Name+". Call one of its tools with arguments encoded as a JSON object string. Tools:\n", selected)
	config, _ := json.Marshal(map[string]any{"transport": server.Transport, "command": server.Command, "args": server.Args, "cwd": server.CWD, "url": server.URL, "tools": names})
	tool, _ := json.Marshal(definition)
	bindings := core.CredentialBindings{}
	if server.SecretRef != "" {
		bindings["MCP_CREDENTIALS"] = core.CredentialRef{Service: server.SecretRef}
	}
	b := core.PluginBundle{Manifest: core.PluginManifest{SchemaVersion: 2, ID: "mcp-" + server.ID, Version: fmt.Sprint(server.Revision), Contributes: core.PluginContributions{Tools: &core.PluginTools{Runtime: "node", Execution: "runtime", SessionMode: "call", Entrypoint: "mcp-adapter.mjs", Files: []string{"mcp-adapter.mjs", "mcp-runtime.mjs", "config.json"}, Requirements: []string{"node"}, Credentials: bindings, Tools: []json.RawMessage{tool}}}}, Files: map[string]string{"mcp-adapter.mjs": mcp.NodeAdapter, "mcp-runtime.mjs": mcp.NodeRuntime, "config.json": string(config)}}
	var err error
	b.Digest, err = b.Hash()
	if err != nil {
		return b, err
	}
	return b, b.Validate()
}
func (s *Service) discoverMCP(ctx context.Context, server MCPServer) (tools []MCPTool, resultErr error) {
	bundle, err := mcpBundle(server)
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	if server.SecretRef != "" {
		raw, err := s.integrationSecrets.Get(ctx, server.SecretRef)
		if err != nil {
			return nil, err
		}
		defer clear(raw)
		env["MCP_CREDENTIALS"] = string(raw)
	}
	worker, err := os.Executable()
	if err != nil {
		return nil, err
	}
	host, err := pluginhost.New(pluginhost.Config{Node: s.options.Node, Identity: bundle.Digest, Files: bundle.Files, Entrypoint: "mcp-adapter.mjs", Mode: "text", Environment: env, ProcessWorker: []string{worker}, Workspace: s.options.Workspace})
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, host.Close()) }()
	result, err := host.Execute(ctx, "discover", map[string]any{"discover": true})
	if err != nil {
		return nil, err
	}
	if result.IsError {
		return nil, errors.New("MCP discovery failed")
	}
	if len(result.Content) != 1 {
		return nil, errors.New("invalid MCP catalog")
	}
	if err = json.Unmarshal([]byte(result.Content[0].Text), &tools); err != nil {
		return nil, err
	}
	if len(tools) > 128 {
		return nil, errors.New("MCP catalog limit")
	}
	seen := map[string]bool{}
	total := 0
	for _, tool := range tools {
		total += len(tool.Name) + len(tool.Description) + len(tool.InputSchema)
		if tool.Name == "" || len(tool.Name) > 128 || seen[tool.Name] || len(tool.Description) > 8192 || total > 128*1024 || !json.Valid(tool.InputSchema) {
			return nil, errors.New("invalid MCP tool")
		}
		seen[tool.Name] = true
	}
	return tools, nil
}

// Credential resolves immutable MCP secret references only at the existing
// authorized dispatch boundary. No secret enters a task bundle or receipt.
func (s *Service) Credential(ctx context.Context, scope, service string, refresh bool) (string, error) {
	if strings.HasPrefix(service, "mcp-") {
		raw, err := s.integrationSecrets.Get(ctx, service)
		if err != nil {
			return "", err
		}
		defer clear(raw)
		return string(raw), nil
	}
	return s.Integrations.Credential(ctx, scope, service, refresh)
}
func (s *Service) Forget(scope string) { s.Integrations.Forget(scope) }
