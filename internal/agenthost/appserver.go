// Package agent adapts Codex App Server sessions to the unified Agent Link host.
package agenthost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jjyr/pot/pkg/core"
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
	Adapter, Model, Effort, HostRoot string
	JobID, RunID                     string
	AssistantID                      string
	Binary                           string
	Workspace                        string
	Mode                             string
	ThreadID                         string
	Worker                           []string
	HostListen                       string
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
	Thread  func(string) error
	Started func(string) error
	Review  func(context.Context, string, json.RawMessage) (string, error)
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

type appTurn struct {
	input            io.WriteCloser
	writing          sync.Mutex
	mu               sync.Mutex
	seq              int
	pending          map[int]chan rpcMessage
	pendingMethods   map[int]string
	starting         int
	startBuffer      []rpcMessage
	startBufferBytes int
	done             chan struct{}
	readerDone       chan struct{}
	once             sync.Once
	result           TurnResult
	workspace        string
	denied           bool
	approvalError    string
	ctx              context.Context
	reviews          map[string]context.CancelFunc
	reviewWG         sync.WaitGroup
	fileChanges      map[string]json.RawMessage
	events           turnEvents
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
	if a.pendingMethods == nil {
		a.pendingMethods = map[int]string{}
	}
	a.pendingMethods[id] = method
	if method == "turn/start" {
		a.starting = id
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.pending, id)
		delete(a.pendingMethods, id)
		if a.starting == id {
			a.starting = 0
			a.startBuffer = nil
			a.startBufferBytes = 0
		}
		a.mu.Unlock()
	}()
	if err := a.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case m := <-ch:
		if len(m.Error) > 0 && string(m.Error) != "null" {
			code := "codex_rpc_rejected_" + strings.ReplaceAll(method, "/", "_")
			var diagnostic struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			}
			if json.Unmarshal(m.Error, &diagnostic) == nil && diagnostic.Message != "" {
				return nil, fmt.Errorf("%s (%d): %s", code, diagnostic.Code, trimBytes(diagnostic.Message, 2048))
			}
			return nil, errors.New(code)
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
				method := a.pendingMethods[id]
				delete(a.pendingMethods, id)
				a.mu.Unlock()
				if ch != nil && method == "turn/start" {
					a.bindStartResponse(id, m)
				}
				if a.isDone() {
					return
				}
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
		if !a.bufferBeforeStart(m) {
			if len(m.ID) > 0 {
				a.approval(m)
			} else {
				a.notification(m)
			}
		}
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
	if a.isDone() {
		return
	}
	if a.events.Review != nil && (m.Method == "item/commandExecution/requestApproval" || m.Method == "item/fileChange/requestApproval") {
		var binding struct {
			ThreadID string `json:"threadId"`
			TurnID   string `json:"turnId"`
		}
		a.mu.Lock()
		valid := validEventJSON(m.Params) && json.Unmarshal(m.Params, &binding) == nil && binding.ThreadID != "" && binding.ThreadID == a.result.ThreadID && binding.TurnID != "" && binding.TurnID == a.result.TurnID
		a.mu.Unlock()
		if !valid {
			a.finish("failed", "codex_approval_binding_mismatch")
			a.interrupt()
			return
		}
		var params map[string]json.RawMessage
		_ = json.Unmarshal(m.Params, &params)
		if available := params["availableDecisions"]; len(available) > 0 && string(available) != "null" {
			var decisions []json.RawMessage
			_ = json.Unmarshal(available, &decisions)
			accept := false
			for _, decision := range decisions {
				if string(decision) == `"accept"` {
					accept = true
				}
			}
			if !accept {
				a.finish("failed", codexLocalReviewDiagnostic(m.Method, "accept_unavailable"))
				a.interrupt()
				return
			}
		}
		if m.Method == "item/fileChange/requestApproval" {
			var id string
			_ = json.Unmarshal(params["itemId"], &id)
			a.mu.Lock()
			proposed := a.fileChanges[id]
			a.mu.Unlock()
			if len(proposed) == 0 {
				a.finish("failed", codexLocalReviewDiagnostic(m.Method, "file_changes_unavailable"))
				a.interrupt()
				return
			}
			params["proposedChanges"] = proposed
			m.Params, _ = json.Marshal(params)
		}
		ctx, cancel := context.WithCancel(a.ctx)
		a.mu.Lock()
		a.reviews[string(m.ID)] = cancel
		a.mu.Unlock()
		a.reviewWG.Add(1)
		go func() {
			defer a.reviewWG.Done()
			defer cancel()
			go func() {
				select {
				case <-a.done:
					cancel()
				case <-ctx.Done():
				}
			}()
			action, err := a.events.Review(ctx, m.Method, m.Params)
			a.mu.Lock()
			delete(a.reviews, string(m.ID))
			if ctx.Err() != nil || a.doneLocked() {
				a.mu.Unlock()
				return
			}
			decision := "accept"
			if err != nil || action != "allow_once" {
				decision = "decline"
				a.denied = true
				a.approvalError = "codex_approval_denied"
				if err != nil {
					a.approvalError = codexLocalReviewDiagnostic(m.Method, "review_rejected")
				}
			}
			a.mu.Unlock()
			if ctx.Err() == nil {
				_ = a.send(map[string]any{"id": m.ID, "result": map[string]string{"decision": decision}})
			}
		}()
		return
	}
	a.mu.Lock()
	a.denied = true
	a.approvalError = codexLocalReviewDiagnostic(m.Method, "unsupported_interaction")
	if m.Method == "mcpServer/elicitation/request" {
		a.approvalError += codexElicitationShape(m.Params, a.result.ThreadID, a.result.TurnID)
	}
	a.mu.Unlock()
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

// Keep diagnostic protocol IDs, never interactive parameters or arbitrary
// server-provided method strings. This records which native gate rejected the
// turn without exposing elicitation text, command arguments or credentials.
func codexLocalReviewDiagnostic(method, reason string) string {
	switch method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval",
		"mcpServer/elicitation/request", "item/tool/requestUserInput",
		"item/permissions/requestApproval", "item/tool/call", "account/chatgptAuthTokens/refresh":
	default:
		method = "unrecognized"
	}
	return "codex_approval_requires_local_review: " + method + ": " + reason
}

// Keep only known enum values and field presence/association. Neither a native
// approval nor a server elicitation can be inferred from this method alone.
func codexElicitationShape(raw json.RawMessage, thread, turn string) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return "; shape=invalid"
	}
	mode := func(value json.RawMessage) string {
		if len(value) == 0 {
			return "absent"
		}
		var name string
		if json.Unmarshal(value, &name) != nil {
			return "invalid"
		}
		switch name {
		case "form", "openai/form", "openaiForm", "url":
			return name
		}
		return "unrecognized"
	}
	association := func(key, expected string) string {
		value, ok := fields[key]
		if !ok {
			return "absent"
		}
		if string(value) == "null" {
			return "null"
		}
		var name string
		if json.Unmarshal(value, &name) != nil {
			return "invalid"
		}
		if expected == "" {
			return "unbound"
		}
		if name == expected {
			return "match"
		}
		return "mismatch"
	}
	var request map[string]json.RawMessage
	_ = json.Unmarshal(fields["request"], &request)
	return fmt.Sprintf("; mode=%s; request_mode=%s; thread=%s; turn=%s; schema_present=%t; request_present=%t; item_present=%t", mode(fields["mode"]), mode(request["mode"]), association("threadId", thread), association("turnId", turn), len(fields["requestedSchema"]) != 0, len(fields["request"]) != 0, len(fields["itemId"]) != 0)
}

func (a *appTurn) notification(m rpcMessage) {
	if a.isDone() {
		return
	}
	switch m.Method {
	case "item/started", "item/completed", "turn/started", "turn/completed":
		thread, turn, ok := eventBinding(m)
		a.mu.Lock()
		matches := ok && thread == a.result.ThreadID && turn == a.result.TurnID && !a.doneLocked()
		a.mu.Unlock()
		if !matches {
			return
		}
	case "serverRequest/resolved":
		if !validEventJSON(m.Params) {
			return
		}
	}
	if m.Method == "item/started" {
		var p struct {
			ThreadID string          `json:"threadId"`
			Item     json.RawMessage `json:"item"`
		}
		var item struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		}
		if json.Unmarshal(m.Params, &p) == nil && json.Unmarshal(p.Item, &item) == nil && item.Type == "fileChange" && len(p.Item) <= maxBody {
			a.mu.Lock()
			if p.ThreadID != "" && p.ThreadID == a.result.ThreadID && !a.doneLocked() {
				if a.fileChanges == nil {
					a.fileChanges = map[string]json.RawMessage{}
				}
				if len(a.fileChanges) < 32 {
					a.fileChanges[item.ID] = p.Item
				}
			}
			a.mu.Unlock()
		}
		return
	}
	if m.Method == "serverRequest/resolved" {
		var p struct {
			ThreadID  string          `json:"threadId"`
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(m.Params, &p) == nil {
			a.mu.Lock()
			if p.ThreadID != "" && p.ThreadID == a.result.ThreadID && !a.doneLocked() {
				if cancel := a.reviews[string(p.RequestID)]; cancel != nil {
					cancel()
				}
			}
			a.mu.Unlock()
		}
		return
	}
	switch m.Method {
	case "turn/started", "turn/completed", "item/completed":
	default:
		return
	}
	var p struct {
		ThreadID string `json:"threadId"`
		Turn     struct {
			ID     string          `json:"id"`
			Status string          `json:"status"`
			Error  json.RawMessage `json:"error"`
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
	switch m.Method {
	case "turn/started":
		// Only the matching turn/start response establishes and persists identity.
		return
	case "turn/completed":
		status, diagnostic := p.Turn.Status, ""
		switch status {
		case "completed":
		case "interrupted":
			status = "cancelled"
		case "failed":
			diagnostic = codexTurnFailure(p.Turn.Error)
		default:
			status = "failed"
			diagnostic = "codex_unknown_turn_status"
		}
		a.mu.Lock()
		denied := a.denied
		approvalError := a.approvalError
		a.mu.Unlock()
		if denied {
			status = "failed"
			diagnostic = approvalError
		}
		a.finish(status, diagnostic)
	case "item/completed":
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.doneLocked() {
			return
		}
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
	executionConfig, err := codexExecutionConfig(s)
	if err != nil {
		result.Error = err.Error()
		return
	}
	binary := s.Binary
	if binary == "" {
		binary = "codex"
	}
	binary, err = exec.LookPath(binary)
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
	cmd.Env = nativeEnvironment(os.Environ(), s)
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
	reviewCtx, cancelReviews := context.WithCancel(ctx)
	a := &appTurn{input: input, pending: map[int]chan rpcMessage{}, done: make(chan struct{}), readerDone: make(chan struct{}), workspace: s.Workspace, events: events, ctx: reviewCtx, reviews: map[string]context.CancelFunc{}}
	a.result.ThreadID = s.ThreadID
	go a.read(output)
	defer func() {
		cancelReviews()
		_ = scope.Stop(cmd.Process.Pid)
		_ = cmd.Wait()
		_ = input.Close()
		<-a.readerDone
		a.reviewWG.Wait()
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
				"item/autoApprovalReview/started",
				"item/autoApprovalReview/completed",
			},
		},
	}); err != nil {
		a.finish("failed", "codex_initialize_failed: "+err.Error())
		return
	}
	if a.send(map[string]string{"method": "initialized"}) != nil {
		a.finish("failed", "codex_initialize_failed")
		return
	}
	// Preserve the receiving Codex configuration, including its automatic
	// reviewer. Only server requests for a human decision enter Agent Link.
	params := map[string]any{"cwd": s.Workspace, "sandbox": s.Mode}
	if executionConfig != nil {
		params["config"] = executionConfig
	}
	if s.Model != "" {
		params["model"] = s.Model
	}
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
		code := "codex_thread_open_failed"
		if strings.Contains(err.Error(), "already has an active writer") {
			code = "codex_thread_in_use"
		}
		a.finish("failed", code+": "+err.Error())
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
	turnParams := map[string]any{"threadId": thread.Thread.ID, "input": []any{map[string]any{"type": "text", "text": prompt, "text_elements": []any{}}}}
	if s.Effort != "" {
		turnParams["effort"] = s.Effort
	}
	_, err = a.rpc(ctx, "turn/start", turnParams)
	if err != nil {
		a.finish("failed", "codex_turn_start_failed_outcome_unknown: "+err.Error())
		return
	}
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

// doneLocked must be called while holding mu when guarding result mutations.
func (a *appTurn) doneLocked() bool {
	select {
	case <-a.done:
		return true
	default:
		return false
	}
}
func (a *appTurn) isDone() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.doneLocked()
}

// Raw maps retain provider-owned extensions while rejecting ambiguous fields.
func validEventJSON(raw json.RawMessage) bool {
	var fields map[string]json.RawMessage
	return core.DecodeJSON(raw, &fields) == nil
}
func eventBinding(m rpcMessage) (thread, turn string, ok bool) {
	if !validEventJSON(m.Params) {
		return
	}
	var p struct {
		ThreadID string `json:"threadId"`
		TurnID   string `json:"turnId"`
		Turn     struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if json.Unmarshal(m.Params, &p) != nil {
		return
	}
	turn = p.TurnID
	if m.Method == "turn/started" || m.Method == "turn/completed" {
		turn = p.Turn.ID
	}
	return p.ThreadID, turn, p.ThreadID != "" && turn != ""
}

// Bind in the reader before it consumes the next JSONL message. Delivering a
// response to rpc's buffered channel alone does not synchronize its caller.
func (a *appTurn) bindStartResponse(requestID int, m rpcMessage) {
	var response struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	rejected := len(m.Error) > 0 && string(m.Error) != "null"
	valid := !rejected && validEventJSON(m.Result) && json.Unmarshal(m.Result, &response) == nil && response.Turn.ID != ""
	a.mu.Lock()
	if a.doneLocked() || a.starting != requestID || a.pending[requestID] == nil {
		a.mu.Unlock()
		return
	}
	buffered := a.startBuffer
	a.starting = 0
	a.startBuffer = nil
	a.startBufferBytes = 0
	if rejected {
		a.mu.Unlock()
		return
	}
	if !valid {
		a.mu.Unlock()
		a.finish("failed", "codex_invalid_turn_outcome_unknown")
		return
	}

	id := response.Turn.ID
	if a.doneLocked() {
		a.mu.Unlock()
		return
	}
	if a.result.TurnID != "" {
		same := a.result.TurnID == id
		a.mu.Unlock()
		if !same {
			a.finish("failed", "codex_invalid_turn_outcome_unknown")
		}
		return
	}
	a.result.TurnID = id
	a.mu.Unlock()
	if a.events.Started != nil && a.events.Started(id) != nil {
		a.finish("failed", "codex_state_persistence_failed_outcome_unknown")
		a.interrupt()
		return
	}
	for _, message := range buffered {
		if a.isDone() {
			return
		}
		if len(message.ID) > 0 {
			a.approval(message)
		} else {
			a.notification(message)
		}
	}
}

// Events can precede the start response. Retain only handled events until that
// authoritative identity arrives, bounded in both count and bytes.
func (a *appTurn) bufferBeforeStart(m rpcMessage) bool {
	a.mu.Lock()
	if a.doneLocked() || a.starting == 0 || a.result.TurnID != "" {
		a.mu.Unlock()
		return false
	}
	thread := a.result.ThreadID
	a.mu.Unlock()
	if len(m.ID) == 0 {
		switch m.Method {
		case "turn/started", "turn/completed", "item/started", "item/completed":
			eventThread, _, ok := eventBinding(m)
			if !ok || eventThread != thread {
				return true
			}
		case "serverRequest/resolved":
			var p struct {
				ThreadID string `json:"threadId"`
			}
			if !validEventJSON(m.Params) || json.Unmarshal(m.Params, &p) != nil || p.ThreadID == "" || p.ThreadID != thread {
				return true
			}
		default:
			return false
		}
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return true
	}
	a.mu.Lock()
	if a.doneLocked() || a.starting == 0 || a.result.TurnID != "" {
		a.mu.Unlock()
		return true
	}
	if len(a.startBuffer) >= 64 || a.startBufferBytes+len(raw) > maxAppServerMessage {
		a.startBuffer = nil
		a.startBufferBytes = 0
		a.mu.Unlock()
		a.finish("failed", "codex_turn_event_buffer_exceeded_outcome_unknown")
		return true
	}
	a.startBuffer = append(a.startBuffer, m)
	a.startBufferBytes += len(raw)
	a.mu.Unlock()
	return true
}
