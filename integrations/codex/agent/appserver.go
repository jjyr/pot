// Package agent adapts Codex App Server sessions to the unified Agent Link host.
package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jjyr/pot/pkg/processscope"
)

const (
	maxResult = 16000
	// Tool results and resumed threads can be much larger than the final reply.
	maxAppServerMessage = 4 * 1024 * 1024
)

// Session binds an owner-selected workspace and permission mode to a persistent
// Codex thread. Restarting App Server resumes that thread; it never starts over
// when a persisted thread cannot be resumed.
type Session struct {
	AssistantID string
	Binary      string
	Workspace   string
	Mode        string
	ThreadID    string
	Worker      []string
}

type TurnResult struct {
	Status           string
	Text             string
	Error            string
	Files            []string
	ThreadID         string
	TurnID           string
	CleanupUncertain bool
}

type turnEvents struct {
	Thread   func(string) error
	Started  func(string) error
	Approval func()
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type appTurn struct {
	input      io.WriteCloser
	writing    sync.Mutex
	mu         sync.Mutex
	seq        int
	pending    map[int]chan rpcMessage
	done       chan struct{}
	readerDone chan struct{}
	once       sync.Once
	result     TurnResult
	workspace  string
	denied     bool
	events     turnEvents
}

func (a *appTurn) send(v any) error {
	a.writing.Lock()
	defer a.writing.Unlock()
	return json.NewEncoder(a.input).Encode(v)
}
func (a *appTurn) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	a.mu.Lock()
	a.seq++
	id := a.seq
	ch := make(chan rpcMessage, 1)
	a.pending[id] = ch
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.pending, id); a.mu.Unlock() }()
	if err := a.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case m := <-ch:
		if len(m.Error) > 0 {
			return nil, errors.New("codex_rpc_rejected_" + strings.ReplaceAll(method, "/", "_"))
		}
		return m.Result, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-a.done:
		return nil, errors.New("codex_session_ended")
	case <-timer.C:
		return nil, errors.New("codex_rpc_timeout")
	}
}
func (a *appTurn) finish(status, diagnostic string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.once.Do(func() { a.result.Status = status; a.result.Error = diagnostic; close(a.done) })
}
func (a *appTurn) interrupt() {
	a.mu.Lock()
	thread, turn := a.result.ThreadID, a.result.TurnID
	a.mu.Unlock()
	if thread != "" && turn != "" {
		_ = a.send(map[string]any{"id": "interrupt", "method": "turn/interrupt", "params": map[string]string{"threadId": thread, "turnId": turn}})
	}
}
func (a *appTurn) read(output io.Reader) {
	defer close(a.readerDone)
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), maxAppServerMessage)
	for scanner.Scan() {
		var m rpcMessage
		if json.Unmarshal(scanner.Bytes(), &m) != nil {
			a.finish("failed", "codex_invalid_jsonl_outcome_unknown")
			return
		}
		if m.Method == "" {
			var id int
			if json.Unmarshal(m.ID, &id) == nil {
				a.mu.Lock()
				ch := a.pending[id]
				a.mu.Unlock()
				if ch != nil {
					select {
					case ch <- m:
					default:
						a.finish("failed", "codex_duplicate_response_outcome_unknown")
						return
					}
				}
			}
			continue
		}
		if len(m.ID) > 0 {
			a.approval(m)
			continue
		}
		a.notification(m)
		select {
		case <-a.done:
			return
		default:
		}
	}
	if scanner.Err() != nil {
		a.finish("failed", "codex_jsonl_read_failed_outcome_unknown")
		return
	}
	a.finish("failed", "codex_process_ended_outcome_unknown")
}
func (a *appTurn) approval(m rpcMessage) {
	a.mu.Lock()
	a.denied = true
	a.mu.Unlock()
	if a.events.Approval != nil {
		a.events.Approval()
	}
	switch m.Method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		_ = a.send(map[string]any{"id": m.ID, "result": map[string]string{"decision": "decline"}})
	case "mcpServer/elicitation/request":
		_ = a.send(map[string]any{"id": m.ID, "result": map[string]string{"action": "cancel"}})
	default:
		_ = a.send(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "Interactive input requires local review"}})
		a.interrupt()
	}
}
func (a *appTurn) notification(m rpcMessage) {
	switch m.Method {
	case "turn/started", "turn/completed", "item/completed":
	default:
		return
	}
	var p struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"turn"`
		Item struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Phase   string `json:"phase"`
			Changes []struct {
				Path string `json:"path"`
			} `json:"changes"`
		} `json:"item"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return
	}
	a.mu.Lock()
	thread := a.result.ThreadID
	a.mu.Unlock()
	if thread != "" && p.ThreadID != "" && thread != p.ThreadID {
		return
	}
	switch m.Method {
	case "turn/started":
		a.mu.Lock()
		a.result.TurnID = p.Turn.ID
		a.mu.Unlock()
		if a.events.Started != nil {
			if a.events.Started(p.Turn.ID) != nil {
				a.finish("failed", "codex_state_persistence_failed_outcome_unknown")
				a.interrupt()
			}
		}
	case "turn/completed":
		status, diagnostic := p.Turn.Status, ""
		switch status {
		case "completed":
		case "interrupted":
			status = "cancelled"
		case "failed":
			diagnostic = "codex_turn_failed"
		default:
			status = "failed"
			diagnostic = "codex_unknown_turn_status"
		}
		a.mu.Lock()
		denied := a.denied
		a.mu.Unlock()
		if denied {
			status = "failed"
			diagnostic = "codex_approval_requires_local_review"
		}
		a.finish(status, diagnostic)
	case "item/completed":
		a.mu.Lock()
		defer a.mu.Unlock()
		// Phase is optional in the App Server protocol. Explicit intermediate
		// messages never become the final reply or consume its bounded space.
		if p.Item.Type == "agentMessage" && (p.Item.Phase == "" || p.Item.Phase == "final_answer") {
			a.result.Text = trimBytes(a.result.Text+p.Item.Text+"\n", maxResult)
		}
		if p.Item.Type == "fileChange" {
			for _, c := range p.Item.Changes {
				path := c.Path
				if !filepath.IsAbs(path) {
					path = filepath.Join(a.workspace, path)
				}
				rel, err := filepath.Rel(a.workspace, path)
				if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && len(a.result.Files) < 16 && len(rel) <= 256 {
					a.result.Files = append(a.result.Files, filepath.ToSlash(rel))
				}
			}
		}
	}
}
func trimBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}

func runTurn(ctx context.Context, s Session, prompt string, events turnEvents) (result TurnResult) {
	result.Status = "failed"
	if s.Mode != "read-only" && s.Mode != "workspace-write" {
		result.Error = "codex_invalid_local_mode"
		return
	}
	if !filepath.IsAbs(s.Workspace) {
		result.Error = "codex_absolute_workspace_required"
		return
	}
	binary := s.Binary
	if binary == "" {
		binary = "codex"
	}
	binary, err := exec.LookPath(binary)
	if err != nil {
		result.Error = "codex_unavailable"
		return
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		result.Error = "codex_unavailable"
		return
	}
	// The host lifecycle owns this subprocess. Cancellation first interrupts the
	// turn, then drains the supervisor before recording a terminal result.
	processCtx, cancel := context.WithTimeout(context.Background(), 31*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(processCtx, binary, "app-server", "--listen", "stdio://")
	cmd.Dir = s.Workspace
	cmd.Env = append(os.Environ(), "POT_CODEX_ASSISTANT_ID="+s.AssistantID)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		result.Error = "codex_process_start_failed"
		return
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		result.Error = "codex_process_start_failed"
		return
	}
	scope, err := processscope.Prepare(cmd, s.Worker, processscope.Markers{})
	if err != nil {
		result.Error = "codex_supervisor_unavailable"
		return
	}
	cmd.Cancel = func() error { return scope.Stop(cmd.Process.Pid) }
	cmd.WaitDelay = 3 * time.Second
	if err = cmd.Start(); err != nil {
		scope.Abort()
		result.Error = "codex_process_start_failed"
		return
	}
	if err = scope.Started(); err != nil {
		_ = scope.Stop(cmd.Process.Pid)
		_ = cmd.Wait()
		result.Error = "codex_process_start_failed"
		if scope.Finish() != nil {
			result.CleanupUncertain = true
			result.Error = "codex_cleanup_unconfirmed_outcome_unknown"
		}
		return
	}
	a := &appTurn{input: input, pending: map[int]chan rpcMessage{}, done: make(chan struct{}), readerDone: make(chan struct{}), workspace: s.Workspace, events: events}
	a.result.ThreadID = s.ThreadID
	go a.read(output)
	defer func() {
		_ = scope.Stop(cmd.Process.Pid)
		_ = cmd.Wait()
		_ = input.Close()
		<-a.readerDone
		a.mu.Lock()
		result = a.result
		a.mu.Unlock()
		if scope.Finish() != nil {
			result.Status = "failed"
			result.CleanupUncertain = true
			result.Error = "codex_cleanup_unconfirmed_outcome_unknown"
		}
	}()
	if _, err = a.rpc(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "pot_agent_link", "version": "1"},
		"capabilities": map[string]any{
			"experimentalApi": false,
			// item/completed must remain enabled: it carries both tool results
			// and authoritative final answers. Approval requests are not muted.
			"optOutNotificationMethods": []string{
				"item/started",
				"item/agentMessage/delta",
				"item/plan/delta",
				"item/reasoning/summaryTextDelta",
				"item/reasoning/summaryPartAdded",
				"item/reasoning/textDelta",
				"item/commandExecution/outputDelta",
				"item/fileChange/outputDelta",
				"turn/diff/updated",
				"turn/plan/updated",
				"thread/tokenUsage/updated",
			},
		},
	}); err != nil {
		a.finish("failed", "codex_initialize_failed")
		return
	}
	if a.send(map[string]string{"method": "initialized"}) != nil {
		a.finish("failed", "codex_initialize_failed")
		return
	}
	params := map[string]any{"cwd": s.Workspace, "approvalPolicy": "on-request", "approvalsReviewer": "user", "sandbox": s.Mode}
	method := "thread/start"
	if s.ThreadID != "" {
		method = "thread/resume"
		params["threadId"] = s.ThreadID
		params["excludeTurns"] = true
	} else {
		params["ephemeral"] = false
	}
	raw, err := a.rpc(ctx, method, params)
	if err != nil {
		a.finish("failed", "codex_thread_open_failed")
		return
	}
	var thread struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if json.Unmarshal(raw, &thread) != nil || thread.Thread.ID == "" || s.ThreadID != "" && s.ThreadID != thread.Thread.ID {
		a.finish("failed", "codex_thread_identity_mismatch")
		return
	}
	a.mu.Lock()
	a.result.ThreadID = thread.Thread.ID
	a.mu.Unlock()
	// Commit session identity before dispatching any potentially effective turn.
	if events.Thread != nil {
		if events.Thread(thread.Thread.ID) != nil {
			a.finish("failed", "codex_thread_persistence_failed")
			return
		}
	}
	raw, err = a.rpc(ctx, "turn/start", map[string]any{"threadId": thread.Thread.ID, "input": []any{map[string]any{"type": "text", "text": prompt, "text_elements": []any{}}}})
	if err != nil {
		a.finish("failed", "codex_turn_start_failed_outcome_unknown")
		return
	}
	var turn struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(raw, &turn) != nil || turn.Turn.ID == "" {
		a.finish("failed", "codex_invalid_turn_outcome_unknown")
		return
	}
	a.mu.Lock()
	a.result.TurnID = turn.Turn.ID
	a.mu.Unlock()
	timer := time.NewTimer(30 * time.Minute)
	defer timer.Stop()
	select {
	case <-a.done:
	case <-ctx.Done():
		a.interrupt()
		select {
		case <-a.done:
		case <-time.After(5 * time.Second):
			a.finish("failed", "codex_interrupt_unconfirmed_outcome_unknown")
		}
	case <-timer.C:
		a.interrupt()
		a.finish("failed", "codex_deadline_outcome_unknown")
	}
	return
}
