package agenthost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/processscope"
)

func runNativeTurn(ctx context.Context, s Session, prompt string, events turnEvents) TurnResult {
	if s.Adapter == "antigravity" {
		return runAgyTurn(ctx, s, prompt, events)
	}
	return runTurn(ctx, s, prompt, events)
}

type boundedLog struct {
	sync.Mutex
	bytes.Buffer
}

func (b *boundedLog) Write(p []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	if b.Len() < 8192 {
		b.Buffer.Write(p[:min(len(p), 8192-b.Len())])
	}
	return len(p), nil
}

// The streaming CLI emits init before accepting work. Persist this identity before
// writing the first user event. Each invocation executes exactly one queued input.
func runAgyTurn(ctx context.Context, s Session, prompt string, events turnEvents) (result TurnResult) {
	result = TurnResult{Status: "failed", ThreadID: s.ThreadID}
	if !filepath.IsAbs(s.Workspace) || (s.Mode != "read-only" && s.Mode != "workspace-write") || s.Model == "" {
		result.Error = "antigravity_invalid_execution_configuration"
		return
	}
	binary, err := exec.LookPath(s.Binary)
	if err != nil {
		result.Error = "antigravity_unavailable"
		return
	}
	if err = verifyAgyPlugin(ctx, binary); err != nil {
		result.Error = err.Error()
		return
	}
	args := []string{"--input-format", "stream-json", "--output-format", "stream-json", "--disable-slash-commands", "--model", s.Model, "--print-timeout", "30m"}
	if s.Effort != "" {
		args = append(args, "--effort", s.Effort)
	}
	if s.ThreadID != "" {
		args = append(args, "--conversation", s.ThreadID)
	}
	processCtx, stop := context.WithTimeout(context.Background(), 31*time.Minute)
	defer stop()
	cmd := exec.CommandContext(processCtx, binary, args...)
	cmd.Dir = s.Workspace
	cmd.Env = nativeEnvironment(os.Environ(), s)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	logs := &boundedLog{}
	cmd.Stderr = logs
	input, err := cmd.StdinPipe()
	if err != nil {
		result.Error = "antigravity_process_start_failed"
		return
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		result.Error = "antigravity_process_start_failed"
		return
	}
	scope, err := processscope.Prepare(cmd, s.Worker, processscope.Markers{})
	if err != nil {
		result.Error = "antigravity_supervisor_unavailable"
		return
	}
	cmd.Cancel = func() error { return scope.Stop(cmd.Process.Pid) }
	cmd.WaitDelay = 3 * time.Second
	if err = cmd.Start(); err != nil {
		scope.Abort()
		result.Error = "antigravity_process_start_failed"
		return
	}
	if err = scope.Started(); err != nil {
		_ = scope.Stop(cmd.Process.Pid)
		_ = cmd.Wait()
		result.Error = "antigravity_process_start_failed"
		result.CleanupUncertain = scope.Finish() != nil
		return
	}
	type line struct {
		raw []byte
		err error
	}
	lines := make(chan line, 1)
	readerDone := make(chan struct{})
	readCtx, readCancel := context.WithCancel(context.Background())
	go func() {
		defer close(readerDone)
		scan := bufio.NewScanner(output)
		scan.Buffer(make([]byte, 4096), maxAppServerMessage)
		for scan.Scan() {
			select {
			case lines <- line{raw: append([]byte{}, scan.Bytes()...)}:
			case <-readCtx.Done():
				return
			}
		}
		select {
		case lines <- line{err: errors.Join(io.EOF, scan.Err())}:
		case <-readCtx.Done():
		}
	}()
	defer func() {
		readCancel()
		_ = input.Close()
		_ = scope.Stop(cmd.Process.Pid)
		_ = cmd.Wait()
		<-readerDone
		if scope.Finish() != nil {
			result.Status = "failed"
			result.CleanupUncertain = true
			result.Error = "antigravity_cleanup_unconfirmed_outcome_unknown"
		}
	}()
	timeout := time.NewTimer(30 * time.Second)
	defer timeout.Stop()
	initialized := false
	sent := false
	for {
		select {
		case <-ctx.Done():
			result.Status = "cancelled"
			result.Error = "antigravity_interrupted"
			return
		case <-timeout.C:
			result.Error = "antigravity_timeout_outcome_unknown"
			return
		case next := <-lines:
			if next.err != nil {
				result.Error = "antigravity_stream_ended_outcome_unknown"
				return
			}
			var e struct {
				Event        string `json:"event"`
				Conversation string `json:"conversation_id"`
				Init         struct {
					Model string `json:"model"`
				} `json:"init"`
				Result struct {
					Conversation string `json:"conversation_id"`
					Status       string `json:"status"`
					Response     string `json:"response"`
					Error        string `json:"error"`
				} `json:"result"`
			}
			if json.Unmarshal(next.raw, &e) != nil {
				result.Error = "antigravity_invalid_json_outcome_unknown"
				return
			}
			switch e.Event {
			case "init":
				if initialized || e.Conversation == "" || s.ThreadID != "" && e.Conversation != s.ThreadID || e.Init.Model != "" && e.Init.Model != s.Model {
					result.Error = "antigravity_session_identity_mismatch"
					return
				}
				if events.Thread != nil {
					if err = events.Thread(e.Conversation); err != nil {
						result.Error = "antigravity_session_persistence_failed"
						return
					}
				}
				result.ThreadID = e.Conversation
				// AGY exposes a conversation, not a native turn ID. RunID remains
				// the Host execution fence and must not masquerade as a native ID.
				result.TurnID = ""
				if events.Started != nil {
					if err = events.Started(""); err != nil {
						result.Error = "antigravity_state_persistence_failed"
						return
					}
				}
				initialized = true
				timeout.Reset(30 * time.Minute)
				if err = json.NewEncoder(input).Encode(map[string]any{"event": "user", "message": map[string]string{"content": prompt}}); err != nil {
					result.Error = "antigravity_input_failed_outcome_unknown"
					return
				}
				sent = true
				_ = input.Close()
			case "result":
				if !sent && e.Result.Status == "ERROR" && e.Result.Conversation == "" {
					result.Error = "antigravity_start_failed: " + trimBytes(e.Result.Error, 2048)
					return
				}
				if !sent || e.Result.Conversation != result.ThreadID {
					result.Error = "antigravity_result_identity_mismatch"
					return
				}
				result.Text = trimBytes(e.Result.Response, maxResult)
				switch e.Result.Status {
				case "SUCCESS":
					result.Status = "completed"
				case "CANCELED", "INTERRUPTED":
					result.Status = "cancelled"
				default:
					result.Status = "failed"
					result.Error = "antigravity_turn_failed: " + trimBytes(e.Result.Error, 2048)
				}
				return
			}
		}
	}
}

// Models runs metadata discovery, never inference. Labels and opaque slugs are
// receiver facts and need no product translation. Discovery failure isn't empty.
func (b *backend) agyModels(ctx context.Context) ([]agentlink.ExecutionModel, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.binary, "models")
	var out, diagnostic boundedLog
	cmd.Stdout = &out
	cmd.Stderr = &diagnostic
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("antigravity_models_failed: %w", err)
	}
	models := []agentlink.ExecutionModel{}
	for _, line := range strings.Split(out.String(), "\n") {
		id, name, ok := strings.Cut(line, "\t")
		if ok && id != "" && len(id) <= 512 && strings.TrimSpace(id) == id && !strings.ContainsAny(id, "\r\n\x00") && strings.TrimSpace(name) != "" {
			models = append(models, agentlink.ExecutionModel{ID: id, Name: strings.TrimSpace(name)})
		}
	}
	if len(models) == 0 {
		return nil, errors.New("antigravity_models_unavailable")
	}
	return models, nil
}

// Read-only guarantees depend on the execution support actually being installed and
// enabled. Refuse admission rather than quietly running without that boundary.
func verifyAgyPlugin(ctx context.Context, binary string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "plugin", "list", "--json")
	var out, log boundedLog
	cmd.Stdout = &out
	cmd.Stderr = &log
	if cmd.Run() != nil {
		return errors.New("antigravity_plugin_unavailable")
	}
	var plugins []struct {
		Name    string   `json:"name"`
		Enabled bool     `json:"enabled"`
		Hooks   []string `json:"hooks"`
		MCP     []string `json:"mcpServers"`
	}
	if json.Unmarshal(out.Bytes(), &plugins) != nil {
		return errors.New("antigravity_plugin_unavailable")
	}
	for _, p := range plugins {
		if p.Name == "pot-receiver" && p.Enabled && slices.Contains(p.Hooks, "pot-execution-scope") && slices.Contains(p.MCP, "pot_receiver") {
			return nil
		}
	}
	return errors.New("antigravity_receiver_support_required")
}
