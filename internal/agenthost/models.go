package agenthost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/processscope"
)

func (b *backend) models(ctx context.Context) ([]agentlink.ExecutionModel, error) {
	if b.runtime().Kind == "antigravity" {
		return b.agyModels(ctx)
	}
	return b.codexModels(ctx)
}

// model/list performs metadata discovery only: no thread/start or turn/start.
// The receiver's own credentials/configuration remain inside its App Server.
func (b *backend) codexModels(ctx context.Context) (models []agentlink.ExecutionModel, err error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.binary, "app-server", "--listen", "stdio://")
	cmd.Dir = b.root
	cmd.Stderr = io.Discard
	input, e := cmd.StdinPipe()
	if e != nil {
		return nil, e
	}
	defer input.Close()
	output, e := cmd.StdoutPipe()
	if e != nil {
		return nil, e
	}
	scope, e := processscope.Prepare(cmd, b.worker, processscope.Markers{})
	if e != nil {
		return nil, e
	}
	cmd.Cancel = func() error { return scope.Stop(cmd.Process.Pid) }
	cmd.WaitDelay = 3 * time.Second
	if e = cmd.Start(); e != nil {
		scope.Abort()
		return nil, e
	}
	defer func() {
		_ = scope.Stop(cmd.Process.Pid)
		_ = cmd.Wait()
		if cleanup := scope.Finish(); cleanup != nil {
			err = cleanup
		}
	}()
	if e = scope.Started(); e != nil {
		return nil, e
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), maxAppServerMessage)
	seq := 0
	rpc := func(method string, params any) (json.RawMessage, error) {
		seq++
		if e := json.NewEncoder(input).Encode(map[string]any{"id": seq, "method": method, "params": params}); e != nil {
			return nil, e
		}
		for scanner.Scan() {
			var m rpcMessage
			if json.Unmarshal(scanner.Bytes(), &m) != nil {
				return nil, agentlink.ErrInvalid
			}
			var id int
			if json.Unmarshal(m.ID, &id) == nil && id == seq {
				if len(m.Error) > 0 && string(m.Error) != "null" {
					return nil, agentlink.ErrModelUnavailable
				}
				return m.Result, nil
			}
			if len(m.ID) > 0 {
				return nil, agentlink.ErrUnsupported
			}
		}
		return nil, agentlink.ErrModelUnavailable
	}
	if _, e = rpc("initialize", map[string]any{"clientInfo": map[string]string{"name": "pot_management", "version": "1"}}); e != nil {
		return nil, e
	}
	if e = json.NewEncoder(input).Encode(map[string]string{"method": "initialized"}); e != nil {
		return nil, e
	}
	models = []agentlink.ExecutionModel{}
	cursor := ""
	seen := map[string]bool{}
	for page := 0; page < 32; page++ {
		params := map[string]any{"limit": 100}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, e := rpc("model/list", params)
		if e != nil {
			return nil, e
		}
		var result struct {
			Data []struct {
				ID      string `json:"id"`
				Model   string `json:"model"`
				Name    string `json:"displayName"`
				Efforts []struct {
					Effort string `json:"reasoningEffort"`
				} `json:"supportedReasoningEfforts"`
				Default string `json:"defaultReasoningEffort"`
				Hidden  bool   `json:"hidden"`
			} `json:"data"`
			Next string `json:"nextCursor"`
		}
		if json.Unmarshal(raw, &result) != nil {
			return nil, agentlink.ErrInvalid
		}
		for _, m := range result.Data {
			if m.Hidden {
				continue
			}
			id := m.Model
			if id == "" {
				id = m.ID
			}
			if id == "" || len(id) > 512 || seen[id] {
				continue
			}
			seen[id] = true
			value := agentlink.ExecutionModel{ID: id, Name: m.Name, DefaultEffort: m.Default}
			if value.Name == "" {
				value.Name = id
			}
			for _, effort := range m.Efforts {
				switch effort.Effort {
				case "none", "minimal", "low", "medium", "high", "xhigh", "max":
					value.Efforts = append(value.Efforts, effort.Effort)
				}
			}
			models = append(models, value)
		}
		if result.Next == "" {
			if len(models) == 0 {
				return nil, agentlink.ErrModelUnavailable
			}
			return models, nil
		}
		if result.Next == cursor {
			return nil, agentlink.ErrInvalid
		}
		cursor = result.Next
	}
	return nil, errors.New("codex_model_catalog_limit")
}
