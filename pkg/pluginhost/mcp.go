package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/jjyr/pot/pkg/mcp"
	"github.com/jjyr/pot/pkg/plugin"
	"github.com/jjyr/pot/pkg/processscope"
)

type MCPHost func(context.Context, string, json.RawMessage) (any, error)

// RunMCP performs real MCP initialize, tools/list and optionally tools/call over
// stdio. The optional negotiated pot/background-store extension is bound by the
// caller to this installed package, never to a model-provided identity.
func RunMCP(ctx context.Context, node string, p plugin.Package, env map[string]string, worker []string, name string, args json.RawMessage, host MCPHost) (tools []*mcp.Tool, result *mcp.CallToolResult, resultErr error) {
	result = &mcp.CallToolResult{}
	// SDK decoding failures can quote malformed wire fields before Inspect runs.
	// Keep those diagnostics behind the same credential boundary as valid frames.
	defer func() {
		if resultErr != nil {
			for _, secret := range env {
				if containsBackgroundCredential([]byte(resultErr.Error()), secret) {
					resultErr = errors.New("credential in MCP response")
					break
				}
			}
		}
	}()
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
	directory, err := os.MkdirTemp("", "pot-mcp-runtime-")
	if err != nil {
		return nil, result, err
	}
	cleanRuntime := true
	defer func() {
		if cleanRuntime {
			_ = os.RemoveAll(directory)
		}
	}()
	runtimePath := filepath.Join(directory, "mcp-runtime.mjs")
	if err = os.WriteFile(runtimePath, []byte(mcp.NodeRuntime), 0600); err != nil {
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
	cmd.Env = append(cmd.Env, "POT_MCP_RUNTIME="+(&url.URL{Scheme: "file", Path: runtimePath}).String())
	cmd.SysProcAttr = processAttributes()
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
		cleanupErr := scope.Finish()
		cleanRuntime = cleanupErr == nil
		resultErr = errors.Join(resultErr, cleanupErr)
	}()

	var hostEnabled atomic.Bool
	options := mcp.Options{Experimental: map[string]any{"pot/background-store": map[string]any{"version": 1}}, Methods: []string{"pot/context", "pot/background/list", "pot/background/initialize", "pot/background/write", "pot/background/receipt"}, Inspect: func(raw []byte) error {
		for _, secret := range env {
			if containsPluginCredential(raw, secret) {
				return errors.New("credential in MCP response")
			}
		}
		return nil
	}}
	options.Handle = func(_ context.Context, method string, raw json.RawMessage) (any, error) {
		if host == nil || !hostEnabled.Load() {
			return nil, errors.New("plugin_host_unavailable")
		}
		return host(ctx, method, raw)
	}
	session, err := mcp.ConnectStdio(ctx, output, input, options)
	if err != nil {
		return nil, result, err
	}
	defer session.Close()
	if session.InitializeResult().Capabilities == nil || session.InitializeResult().Capabilities.Tools == nil {
		return nil, result, errors.New("MCP tools unavailable")
	}
	// The host extension is optional and checked independently from standard MCP.
	if value, ok := session.InitializeResult().Capabilities.Experimental["pot/background-store"].(map[string]any); ok {
		hostEnabled.Store(value["version"] == float64(1))
	}
	tools, err = session.ListTools(ctx)
	if err != nil {
		return nil, result, err
	}
	if name == "" {
		return tools, result, nil
	}
	found := false
	for _, tool := range tools {
		if tool.Name == name {
			found = true
		}
	}
	if !found {
		return nil, result, errors.New("unknown MCP tool")
	}
	called, err := session.CallTool(ctx, name, args)
	if err != nil {
		return tools, result, err
	}
	return tools, called, nil
}
