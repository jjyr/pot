package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/processscope"
)

type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}
type MCPResult struct {
	Content           []Content       `json:"content"`
	StructuredContent json.RawMessage `json:"structuredContent,omitempty"`
	IsError           bool            `json:"isError,omitempty"`
}
type MCPHost func(context.Context, string, json.RawMessage) (any, error)

// RunMCP performs real MCP initialize, tools/list and optionally tools/call over
// stdio. The optional negotiated pot/background-store extension is bound by the
// caller to this installed package, never to a model-provided identity.
func RunMCP(ctx context.Context, node string, p plugin.Package, env map[string]string, worker []string, name string, args json.RawMessage, host MCPHost) (tools []MCPTool, result MCPResult, resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := p.Manifest.Validate(); err != nil {
		return nil, result, err
	}
	if _, err := p.MCPIdentity(); err != nil {
		return nil, result, err
	}
	c := p.Manifest.Contributes.MCP
	if node == "" {
		node = "node"
	}
	node, err := exec.LookPath(node)
	if err != nil {
		return nil, result, err
	}
	node, _ = filepath.Abs(node)
	entry, err := p.Path(c.Entrypoint)
	if err != nil {
		return nil, result, err
	}
	cmd := exec.CommandContext(ctx, node, entry)
	cmd.Dir = p.Root
	// No loader flags, arbitrary MCP credentials or unrelated service secrets.
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "SYSTEMROOT"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	for key, value := range env {
		if _, ok := c.Credentials[key]; !ok {
			return nil, result, errors.New("undeclared MCP credential")
		}
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, result, err
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, result, err
	}
	scope, err := processscope.Prepare(cmd, worker, processscope.Markers{})
	if err != nil {
		return nil, result, err
	}
	cmd.Cancel = func() error { return scope.Stop(cmd.Process.Pid) }
	cmd.WaitDelay = 3 * time.Second
	if err = cmd.Start(); err != nil {
		scope.Abort()
		return nil, result, err
	}
	if err = scope.Started(); err != nil {
		_ = scope.Stop(cmd.Process.Pid)
		_ = cmd.Wait()
		_ = scope.Finish()
		return nil, result, err
	}
	defer func() {
		input.Close()
		_ = scope.Stop(cmd.Process.Pid)
		_ = cmd.Wait()
		resultErr = errors.Join(resultErr, scope.Finish())
	}()
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 3*1024*1024+1)
	send := func(v any) error {
		raw, e := json.Marshal(v)
		if e != nil {
			return e
		}
		_, e = input.Write(append(raw, '\n'))
		return e
	}
	hostAllowed := false
	calls := 0
	total := 0
	rpc := func(id int, method string, params any) (json.RawMessage, error) {
		if err := send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
			return nil, err
		}
		for scanner.Scan() {
			raw := scanner.Bytes()
			total += len(raw)
			if total > 8*1024*1024 {
				return nil, errors.New("MCP exchange limit")
			}
			for _, secret := range env {
				if containsBackgroundCredential(raw, secret) {
					return nil, errors.New("credential in MCP response")
				}
			}
			var frame struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      json.RawMessage `json:"id"`
				Method  string          `json:"method"`
				Params  json.RawMessage `json:"params"`
				Result  json.RawMessage `json:"result"`
				Error   json.RawMessage `json:"error"`
			}
			if e := core.DecodeJSON(raw, &frame); e != nil || frame.JSONRPC != "2.0" {
				return nil, errors.New("invalid MCP frame")
			}
			if frame.Method != "" {
				if len(frame.ID) == 0 {
					continue
				}
				calls++
				if calls > 128 {
					return nil, errors.New("MCP host request limit")
				}
				var value any
				var e error
				if host == nil || !hostAllowed {
					e = errors.New("plugin_host_unavailable")
				} else {
					value, e = host(ctx, frame.Method, frame.Params)
				}
				reply := map[string]any{"jsonrpc": "2.0", "id": frame.ID}
				if e != nil {
					reply["error"] = map[string]any{"code": -32000, "message": e.Error()}
				} else {
					reply["result"] = value
				}
				if e = send(reply); e != nil {
					return nil, e
				}
				continue
			}
			if string(frame.ID) != string(mustJSON(id)) {
				return nil, errors.New("unexpected MCP response id")
			}
			if len(frame.Error) > 0 {
				return nil, errors.New("MCP request rejected")
			}
			return frame.Result, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, errors.New("MCP connection closed")
	}
	raw, err := rpc(1, "initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{"experimental": map[string]any{"pot/background-store": map[string]any{"version": 1}}}, "clientInfo": map[string]any{"name": "pot", "version": "1"}})
	if err != nil {
		return nil, result, err
	}
	var initialized struct {
		ProtocolVersion string          `json:"protocolVersion"`
		Capabilities    json.RawMessage `json:"capabilities"`
		ServerInfo      json.RawMessage `json:"serverInfo"`
		Instructions    string          `json:"instructions,omitempty"`
	}
	if err = core.DecodeJSON(raw, &initialized); err != nil || initialized.ProtocolVersion != "2025-06-18" {
		return nil, result, errors.New("unsupported MCP initialization")
	}
	if err = send(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err != nil {
		return nil, result, err
	}
	var capabilities struct {
		Experimental map[string]struct {
			Version int `json:"version"`
		} `json:"experimental"`
	}
	if err = json.Unmarshal(initialized.Capabilities, &capabilities); err != nil {
		return nil, result, errors.New("invalid MCP capabilities")
	}
	hostAllowed = capabilities.Experimental["pot/background-store"].Version == 1
	raw, err = rpc(2, "tools/list", map[string]any{})
	if err != nil {
		return nil, result, err
	}
	var catalog struct {
		Tools      []MCPTool `json:"tools"`
		NextCursor string    `json:"nextCursor,omitempty"`
	}
	if err = core.DecodeJSON(raw, &catalog); err != nil || len(catalog.Tools) > 128 {
		return nil, result, errors.New("invalid MCP catalog")
	}
	tools = catalog.Tools
	cursors := map[string]bool{}
	rpcID := 3
	for catalog.NextCursor != "" {
		cursor := catalog.NextCursor
		if len(cursor) > 4096 || cursors[cursor] || len(cursors) >= 128 {
			return nil, result, errors.New("invalid MCP pagination")
		}
		cursors[cursor] = true
		raw, err = rpc(rpcID, "tools/list", map[string]any{"cursor": cursor})
		rpcID++
		if err != nil {
			return nil, result, err
		}
		catalog.Tools = nil
		catalog.NextCursor = ""
		if err = core.DecodeJSON(raw, &catalog); err != nil || len(tools)+len(catalog.Tools) > 128 {
			return nil, result, errors.New("invalid MCP catalog")
		}
		tools = append(tools, catalog.Tools...)
	}
	found := false
	seen := map[string]bool{}
	for _, tool := range tools {
		if !core.PluginToolName(tool.Name) || seen[tool.Name] || len(tool.Description) > 8192 || len(tool.InputSchema) > 65536 || !json.Valid(tool.InputSchema) {
			return nil, result, errors.New("invalid MCP tool")
		}
		seen[tool.Name] = true
		if tool.Name == name {
			found = true
		}
	}
	if name == "" {
		return tools, result, nil
	}
	if !found {
		return nil, result, errors.New("unknown MCP tool")
	}
	raw, err = rpc(rpcID, "tools/call", map[string]any{"name": name, "arguments": args})
	if err != nil {
		return tools, result, err
	}
	if err = core.DecodeJSON(raw, &result); err != nil {
		return tools, result, errors.New("invalid MCP tool result")
	}
	return tools, result, nil
}
func mustJSON(v any) json.RawMessage { raw, _ := json.Marshal(v); return raw }
