package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/composition"
	"github.com/jjyr/pot/pkg/core"
	m "github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"io"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAllowanceWaitingClearsUnsentModelAndResumesOriginalTurn(t *testing.T) {
	a, f, s, thread, execution := setup(t)
	s.model = func(_ core.ModelRequest, index int) (string, error) {
		if index == 0 {
			return "", &protocol.APIError{Code: "pot_token_limit", Message: "waiting for allowance"}
		}
		return response("after allowance"), nil
	}
	request := core.TurnRequest{RequestID: "turn", Text: "original input"}
	err := a.Execute(context.Background(), execution, thread, request, false, func(core.AgentEvent) error { return nil })
	var api *protocol.APIError
	if !errors.As(err, &api) || api.Code != "pot_token_limit" {
		t.Fatal(err)
	}
	var checkpoint Checkpoint
	_ = json.Unmarshal(f.body, &checkpoint)
	if checkpoint.ModelRequestID != nil || checkpoint.Started != nil {
		t.Fatal("unsent allowance rejection fenced retry", checkpoint)
	}
	if err := a.Execute(context.Background(), execution, thread, request, true, func(core.AgentEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(f.body, &checkpoint)
	inputs := 0
	for _, message := range checkpoint.Messages {
		if message.Text == "original input" {
			inputs++
		}
	}
	if inputs != 1 || s.calls != 2 {
		t.Fatal(inputs, s.calls)
	}
}

type feedback struct {
	mu           sync.Mutex
	body         json.RawMessage
	observations []Observation
	guidance     []SteeringInput
	acked        map[string]bool
	settled      bool
	saveErr      error
}

func (f *feedback) LoadContext(context.Context) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append(json.RawMessage(nil), f.body...), nil
}
func (f *feedback) SaveContext(_ context.Context, raw json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.saveErr != nil {
		return f.saveErr
	}
	f.body = append(f.body[:0], raw...)
	return nil
}
func (f *feedback) RecordTurn(context.Context, string) error     { return nil }
func (f *feedback) InputStreaming(context.Context, string) error { return nil }
func (f *feedback) Ready(ctx context.Context) error              { return ctx.Err() }
func (f *feedback) Steering(_ context.Context, final bool) ([]SteeringInput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !final {
		return nil, nil
	}
	result := []SteeringInput{}
	for _, item := range f.guidance {
		if !f.acked[item.ID] {
			result = append(result, item)
		}
	}
	return result, nil
}
func (f *feedback) AcknowledgeSteering(_ context.Context, ids []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	var state Checkpoint
	if err := json.Unmarshal(f.body, &state); err != nil {
		return err
	}
	if f.acked == nil {
		f.acked = map[string]bool{}
	}
	for _, id := range ids {
		found := false
		for _, stored := range state.SteeringIDs {
			if stored == id {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("steering acknowledged before persistence")
		}
		f.acked[id] = true
	}
	return nil
}
func (f *feedback) Observe(_ context.Context, event Observation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observations = append(f.observations, event)
	return nil
}
func (f *feedback) Settle(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settled = true
	return nil
}

type services struct {
	feedback    *feedback
	calls       int
	model       func(core.ModelRequest, int) (string, error)
	compactions []json.RawMessage
}

func TestAgentContinuesBeyond128Rounds(t *testing.T) {
	a, f, s, thread, execution := setup(t)
	thread.Config.Params.Agent.AutoCompact = false
	thread.Config.ContextWindow = 1000000
	thread.Config.Params.Agent.ContextWindow = 1000000
	if err := thread.Config.Rehash(); err != nil {
		t.Fatal(err)
	}
	a.Tools = []m.ToolDefinition{{Name: "probe", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	calls := 0
	a.Dispatch = func(context.Context, core.ExecutionContext, m.ToolCall, func(core.Update) error) (json.RawMessage, error) {
		calls++
		return json.RawMessage(`{"ok":true}`), nil
	}
	s.model = func(_ core.ModelRequest, index int) (string, error) {
		if index == 130 {
			return response("done"), nil
		}
		return response("", m.ToolCall{ID: fmt.Sprintf("probe-%d", index), Name: "probe", Arguments: json.RawMessage(`{}`)}), nil
	}
	// Allow the complete multi-step workflow to finish under the race detector.
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := a.Execute(ctx, execution, thread, core.TurnRequest{RequestID: "turn", Text: "finish the work"}, false, func(core.AgentEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var checkpoint Checkpoint
	if err := json.Unmarshal(f.body, &checkpoint); err != nil || checkpoint.CompletedRequest != "turn" || s.calls != 131 || calls != 130 {
		t.Fatal("long task did not finish normally", err, checkpoint.CompletedRequest, s.calls, calls)
	}
}

func (s *services) Model(_ context.Context, request core.ModelRequest) (io.ReadCloser, error) {
	s.feedback.mu.Lock()
	var state Checkpoint
	err := json.Unmarshal(s.feedback.body, &state)
	s.feedback.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if state.ModelRequestID == nil || *state.ModelRequestID != request.ID {
		return nil, fmt.Errorf("model started before stable identity persisted")
	}
	index := s.calls
	s.calls++
	text, err := s.model(request, index)
	if err != nil {
		return nil, err
	}
	return io.NopCloser(strings.NewReader(text)), nil
}
func (s *services) Compaction(_ context.Context, raw json.RawMessage) error {
	s.compactions = append(s.compactions, append(json.RawMessage(nil), raw...))
	return nil
}
func response(text string, calls ...m.ToolCall) string {
	delta := map[string]any{"content": text}
	reason := "stop"
	if len(calls) > 0 {
		reason = "tool_calls"
		wire := []any{}
		for i, call := range calls {
			wire = append(wire, map[string]any{"index": i, "id": call.ID, "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
		}
		delta["tool_calls"] = wire
	}
	data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": reason}}, "usage": map[string]any{"prompt_tokens": 33, "completion_tokens": 10}})
	return "data: " + string(data) + "\n\ndata: [DONE]\n\n"
}
func setup(t *testing.T) (*Agent, *feedback, *services, core.Thread, core.ExecutionContext) {
	t.Helper()
	f := &feedback{}
	s := &services{feedback: f}
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	settings.BuiltinTools = []string{}
	profile := core.Profile{ID: "profile", Revision: 1, Name: "test", Provider: "mock", Model: "model", AuthRef: "provider:mock", Params: core.Parameters{MaxOutputTokens: 100, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}
	spec, err := profile.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	a := &Agent{Services: s, Feedback: f, Cleanup: func(context.Context, core.ExecutionContext, bool) error { return nil }, Composer: composition.Runner{}}
	return a, f, s, core.Thread{Owner: "owner", Generation: 3, Config: spec}, core.ExecutionContext{Owner: "owner", Role: core.Main}
}
func noTools(_ core.ModelRequest, _ int) (string, error) { return response("done"), nil }

func TestModelOnlyWithoutComposer(t *testing.T) {
	a, _, s, thread, execution := setup(t)
	a.Composer = nil
	s.model = noTools
	var text strings.Builder
	if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "model-only", Text: "hello"}, false, func(event core.AgentEvent) error {
		if event.Update.Type == "text_delta" {
			text.WriteString(event.Update.Text)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if s.calls != 1 || text.String() != "done" {
		t.Fatalf("model calls = %d, text = %q; want 1, done", s.calls, text.String())
	}
}

func TestToolsCheckpointBeforeEffectsAndNeverRepeatReceipt(t *testing.T) {
	a, f, s, thread, execution := setup(t)
	a.Tools = []m.ToolDefinition{{Name: "probe", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	calls := 0
	a.Dispatch = func(_ context.Context, _ core.ExecutionContext, call m.ToolCall, _ func(core.Update) error) (json.RawMessage, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		var state Checkpoint
		_ = json.Unmarshal(f.body, &state)
		if state.Started == nil || *state.Started != call.ID || len(state.Pending) == 0 {
			return nil, fmt.Errorf("effect before checkpoint")
		}
		calls++
		return json.RawMessage(`{"result":"one effect"}`), nil
	}
	s.model = func(_ core.ModelRequest, index int) (string, error) {
		if index < 2 {
			return response("", m.ToolCall{ID: "same", Name: "probe", Arguments: json.RawMessage(`{}`)}), nil
		}
		return response("done"), nil
	}
	events := []core.AgentEvent{}
	err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "do work"}, false, func(event core.AgentEvent) error { events = append(events, event); return nil })
	if err != nil || calls != 1 {
		t.Fatal(calls, err)
	}
	for i, event := range events {
		if event.Seq != uint64(i+1) || event.Generation != 3 || event.TurnID != "turn" {
			t.Fatal(event)
		}
	}
	if events[len(events)-1].Update.Outcome.Kind != "success" || !f.settled {
		t.Fatal(events)
	}
	before := s.calls
	if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "do work"}, true, func(core.AgentEvent) error { return nil }); err != nil || s.calls != before {
		t.Fatal("completed turn resampled", err, s.calls)
	}
}
func TestUnknownStartedOrModelCheckpointNeverReplays(t *testing.T) {
	for _, model := range []bool{false, true} {
		a, f, s, thread, execution := setup(t)
		s.model = noTools
		id := "call"
		state := Checkpoint{Type: "context", Version: 2, Messages: []m.Message{}, Pending: []m.ToolCall{}, Receipts: []ToolReceipt{}, SteeringIDs: []string{}, LastRequest: "turn"}
		if model {
			state.ModelRequestID = &id
		} else {
			state.Started = &id
			state.Pending = []m.ToolCall{{ID: id, Name: "probe", Arguments: json.RawMessage(`{}`)}}
		}
		f.body, _ = json.Marshal(state)
		var final core.Outcome
		err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn"}, true, func(event core.AgentEvent) error {
			if event.Update.Type == "turn_finished" {
				final = *event.Update.Outcome
			}
			return nil
		})
		if !errors.Is(err, ErrUncertainEffects) || s.calls != 0 || final.Kind != "unknown" {
			t.Fatal(err, s.calls, final)
		}
	}
}
func TestPersistenceFailurePreventsNetwork(t *testing.T) {
	a, f, s, thread, execution := setup(t)
	s.model = noTools
	f.saveErr = errors.New("disk full")
	err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(core.AgentEvent) error { return nil })
	if !errors.Is(err, f.saveErr) || s.calls != 0 {
		t.Fatal(err, s.calls)
	}
}

func TestToolFailureKeepsExecutionDiagnosticsInNextModelRequest(t *testing.T) {
	a, _, s, thread, execution := setup(t)
	a.Tools = []m.ToolDefinition{{Name: "probe", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	a.Dispatch = func(context.Context, core.ExecutionContext, m.ToolCall, func(core.Update) error) (json.RawMessage, error) {
		return json.RawMessage(`{"execution_mode":"sandbox","execution_status":"preparation_failed","detail":"workload did not start"}`), fmt.Errorf("sandbox unavailable")
	}
	s.model = func(request core.ModelRequest, index int) (string, error) {
		if index == 0 {
			return response("", m.ToolCall{ID: "failed-probe", Name: "probe", Arguments: json.RawMessage(`{}`)}), nil
		}
		for _, field := range []string{"preparation_failed", "sandbox", "workload did not start", "sandbox unavailable"} {
			if !strings.Contains(string(request.Body), field) {
				return "", fmt.Errorf("execution diagnostic lost: %s", field)
			}
		}
		return response("reported failure"), nil
	}
	if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "failure", Text: "run the probe once"}, false, func(core.AgentEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if s.calls != 2 {
		t.Fatal("failure was replayed", s.calls)
	}
}

func TestBrowserUnavailableResultReachesNextModelRequest(t *testing.T) {
	a, _, s, thread, execution := setup(t)
	a.Tools = []m.ToolDefinition{{Name: "probe", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	a.Dispatch = func(context.Context, core.ExecutionContext, m.ToolCall, func(core.Update) error) (json.RawMessage, error) {
		return json.RawMessage(`{"isError":true,"code":"browser_unavailable","execution_status":"not_started","error":"Chromium/Chrome unavailable. No browser operation or supplied JavaScript was started.","content":[]}`), nil
	}
	s.model = func(request core.ModelRequest, index int) (string, error) {
		if index == 0 {
			return response("", m.ToolCall{ID: "browser-probe", Name: "probe", Arguments: json.RawMessage(`{}`)}), nil
		}
		for _, field := range []string{"browser_unavailable", "not_started", "Chromium/Chrome unavailable", "No browser operation or supplied JavaScript was started"} {
			if !strings.Contains(string(request.Body), field) {
				return "", fmt.Errorf("browser diagnostic lost before model: %s", field)
			}
		}
		return response("browser unavailable"), nil
	}
	failed := false
	if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "browser-failure", Text: "inspect a web page"}, false, func(event core.AgentEvent) error {
		if event.Update.Type == "tool_finished" {
			failed = event.Update.Failed
		}
		return nil
	}); err != nil || !failed || s.calls != 2 {
		t.Fatal("browser failure not delivered to Agent", err, failed, s.calls)
	}
}
func TestCancellationDrainsBeforeTerminalAndRetainsUncertainCheckpoint(t *testing.T) {
	a, f, s, thread, execution := setup(t)
	a.Tools = []m.ToolDefinition{{Name: "probe", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	started := make(chan struct{})
	a.Dispatch = func(ctx context.Context, _ core.ExecutionContext, _ m.ToolCall, _ func(core.Update) error) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	s.model = func(core.ModelRequest, int) (string, error) {
		return response("", m.ToolCall{ID: "call", Name: "probe", Arguments: json.RawMessage(`{}`)}), nil
	}
	var cleaned atomic.Bool
	a.Cleanup = func(_ context.Context, _ core.ExecutionContext, abort bool) error {
		if !abort {
			t.Error("cancel used normal cleanup")
		}
		cleaned.Store(true)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- a.Execute(ctx, execution, thread, core.TurnRequest{RequestID: "turn", Text: "work"}, false, func(event core.AgentEvent) error {
			if event.Update.Type == "turn_finished" && (!cleaned.Load() || !f.settled) {
				return fmt.Errorf("terminal before cleanup/settlement")
			}
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("tool not started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation stuck")
	}
	var state Checkpoint
	_ = json.Unmarshal(f.body, &state)
	if state.Started == nil {
		t.Fatal("unknown effect forgotten")
	}
}
func TestSteeringIsSavedBeforeAcknowledgmentAndFinalResponse(t *testing.T) {
	a, f, s, thread, execution := setup(t)
	f.guidance = []SteeringInput{{ID: "guidance", Text: "new direction"}}
	s.model = func(request core.ModelRequest, index int) (string, error) {
		if index == 1 && !strings.Contains(string(request.Body), "new direction") {
			return "", fmt.Errorf("guidance not in next request")
		}
		return response("done"), nil
	}
	var applied uint64
	if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "hello"}, false, func(event core.AgentEvent) error {
		if event.Update.Type == "input_applied" {
			var checkpoint Checkpoint
			if err := json.Unmarshal(f.body, &checkpoint); err != nil || !slices.Contains(checkpoint.SteeringIDs, event.Update.ID) || f.acked[event.Update.ID] {
				t.Fatal("input bookmark must follow persistence and precede acknowledgment", event, err)
			}
			applied = event.Seq
		}
		if event.Update.Type == "message_started" && s.calls == 1 && applied == 0 {
			t.Fatal("continuation started before input bookmark")
		}
		return nil
	}); err != nil || s.calls != 2 || !f.acked["guidance"] || applied == 0 {
		t.Fatal(err, s.calls, f.acked)
	}
}
func TestCompositionWaitStreamsWithoutRepeatingAndUsesAuthorizedDispatch(t *testing.T) {
	a, _, s, thread, execution := setup(t)
	thread.Config.Params.Agent.BuiltinTools = []string{"exec", "wait"}
	if err := thread.Config.Rehash(); err != nil {
		t.Fatal(err)
	}
	a.Tools = []m.ToolDefinition{{Name: "probe", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	release := make(chan struct{})
	a.Dispatch = func(ctx context.Context, _ core.ExecutionContext, call m.ToolCall, _ func(core.Update) error) (json.RawMessage, error) {
		if !strings.Contains(call.ID, "exec-call:js:") {
			return nil, fmt.Errorf("composition child identity missing")
		}
		select {
		case <-release:
			return json.RawMessage(`"after"`), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	outputs := ""
	s.model = func(request core.ModelRequest, index int) (string, error) {
		switch index {
		case 0:
			return response("", m.ToolCall{ID: "exec-call", Name: "exec", Arguments: json.RawMessage(`{"code":"text('before'); text(await tools.probe({}));","yield_time_ms":1}`)}), nil
		case 1:
			var body map[string]any
			_ = json.Unmarshal(request.Body, &body)
			var result map[string]any
			for _, item := range body["messages"].([]any) {
				message := item.(map[string]any)
				if message["role"] == "tool" {
					_ = json.Unmarshal([]byte(message["content"].(string)), &result)
				}
			}
			outputs += result["output"].(string)
			id, ok := result["exec_id"].(string)
			if !ok {
				return "", fmt.Errorf("composition completed before blocked tool")
			}
			close(release)
			args, _ := json.Marshal(map[string]any{"exec_id": id, "yield_time_ms": 1000})
			return response("", m.ToolCall{ID: "wait-call", Name: "wait", Arguments: args}), nil
		default:
			var body map[string]any
			_ = json.Unmarshal(request.Body, &body)
			messages := body["messages"].([]any)
			last := messages[len(messages)-1].(map[string]any)
			var result map[string]any
			_ = json.Unmarshal([]byte(last["content"].(string)), &result)
			outputs += result["output"].(string)
			if id, ok := result["exec_id"].(string); ok {
				args, _ := json.Marshal(map[string]any{"exec_id": id, "yield_time_ms": 1000})
				return response("", m.ToolCall{ID: fmt.Sprintf("wait-call-%d", index), Name: "wait", Arguments: args}), nil
			}
			return response("done"), nil
		}
	}
	childStarted := false
	if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "compose"}, false, func(event core.AgentEvent) error {
		if event.Update.Type == "tool_started" && event.Update.Name == "probe" {
			childStarted = true
			if event.Update.ParentID != "exec-call" || event.Update.AtMS == 0 {
				return fmt.Errorf("missing composition parent or timestamp")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !childStarted || outputs != "before\nafter\n" {
		t.Fatal(outputs)
	}
}
func TestResolvedReferencesCountTowardContextLimit(t *testing.T) {
	a, _, s, thread, execution := setup(t)
	limit := uint64(512)
	thread.Config.Params.Agent.MaxInputTokens = &limit
	thread.Config.Params.Agent.AutoCompact = false
	if err := thread.Config.Rehash(); err != nil {
		t.Fatal(err)
	}
	a.ResolveReferences = func(_ context.Context, messages []m.Message) ([]m.Message, error) {
		out := append([]m.Message(nil), messages...)
		for i, message := range out {
			if len(message.References) > 0 {
				out[i].ReferenceContents = []m.ReferenceContent{{Role: "assistant", Context: strings.Repeat("source words ", 600)}}
			}
		}
		return out, nil
	}
	err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "Why?", Inputs: []core.ConversationInput{{ID: "input", Text: "Why?", References: []core.MessageReference{{MessageID: "source"}}}}}, false, func(core.AgentEvent) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "context exceeds configured model limits") || s.calls != 0 {
		t.Fatal("expanded references bypassed the model input cap", err, s.calls)
	}
}

func TestCompactionPersistsReducedClassifiedHistory(t *testing.T) {
	a, f, s, thread, execution := setup(t)
	a.ResolveReferences = func(_ context.Context, messages []m.Message) ([]m.Message, error) {
		out := append([]m.Message(nil), messages...)
		for i, message := range out {
			for _, ref := range message.References {
				out[i].ReferenceContents = []m.ReferenceContent{{Role: "assistant", Quote: ref.Quote, Context: "Original source with selected words."}}
			}
		}
		return out, nil
	}
	threshold := uint64(100)
	thread.Config.Params.Agent.CompactAt = &threshold
	if err := thread.Config.Rehash(); err != nil {
		t.Fatal(err)
	}
	state := Checkpoint{Type: "context", Version: 2, Messages: []m.Message{}, Pending: []m.ToolCall{}, Receipts: []ToolReceipt{}, SteeringIDs: []string{}}
	for i := range 4 {
		state.Messages = append(state.Messages, m.TextMessage("user", strings.Repeat(fmt.Sprint(i), 1000)), m.TextMessage("assistant", strings.Repeat("answer", 1000)))
	}
	f.body, _ = json.Marshal(state)
	s.model = func(request core.ModelRequest, index int) (string, error) {
		if !strings.Contains(string(request.Body), "Original source with selected words.") || strings.Contains(string(request.Body), "batch-first") {
			return "", fmt.Errorf("compaction omitted resolved reference content or leaked an internal ID")
		}
		if index == 0 {
			if request.CompactionID == nil || !strings.Contains(string(request.Body), "pot_compact_request") {
				return "", fmt.Errorf("invalid summary request")
			}
			return response("short factual summary"), nil
		}
		return response("continued"), nil
	}
	if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "turn", Text: "latest goal", Inputs: []core.ConversationInput{
		{ID: "batch-first", Text: "latest goal", References: []core.MessageReference{{MessageID: "older", Quote: "selected words"}}},
		{ID: "batch-second", Text: "later constraint"},
	}}, false, func(core.AgentEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if len(s.compactions) != 6 || s.calls != 2 {
		t.Fatal(len(s.compactions), s.calls)
	}
	var result Checkpoint
	_ = json.Unmarshal(f.body, &result)
	found := false
	retained := map[string]m.Message{}
	for _, message := range result.Messages {
		if message.Role == "user" && message.Kind.Type == "conversation" {
			retained[message.ID] = message
		}
		if message.Kind.Type == "summary" {
			found = true
		}
		if strings.Contains(message.Text, strings.Repeat("answer", 100)) {
			t.Fatal("history not compacted")
		}
	}
	if len(retained) != 2 || retained["batch-first"].Text != "latest goal" || len(retained["batch-first"].References) != 1 || retained["batch-second"].Text != "later constraint" {
		t.Fatal("compaction lost a batch member or reference", retained)
	}
	if !found || result.ModelRequestID != nil {
		t.Fatal(result)
	}
}

func TestNotificationRetainsConfiguredToolsAndRefreshesContext(t *testing.T) {
	a, _, service, thread, execution := setup(t)
	execution.Role = core.Summary
	execution.Instructions = "fresh-notification-instructions"
	a.Tools = []m.ToolDefinition{
		{Name: "exec_command", InputSchema: json.RawMessage(`{"type":"object"}`)},
		{Name: "update_agent_profile", InputSchema: json.RawMessage(`{"type":"object"}`)},
	}
	thread.Config.Params.Agent.BuiltinTools = []string{"exec_command", "exec", "wait"}
	if err := thread.Config.Rehash(); err != nil {
		t.Fatal(err)
	}
	for _, allowed := range []*[]string{nil, {"update_agent_profile"}} {
		settings := thread.Config.Params.Agent
		settings.AllowedTools = allowed
		main, err := a.available(core.Main, settings, core.DefaultModelDescriptor())
		if err != nil {
			t.Fatal(err)
		}
		notification, err := a.available(core.Summary, settings, core.DefaultModelDescriptor())
		if err != nil {
			t.Fatal(err)
		}
		left, _ := json.Marshal(main)
		right, _ := json.Marshal(notification)
		if string(left) != string(right) {
			t.Fatal("notification changed configured tools")
		}
	}
	var skills, effects int
	a.Skills = func(context.Context, core.ExecutionContext) (SkillCatalog, error) {
		skills++
		return SkillCatalog{}, nil
	}
	a.Dispatch = func(_ context.Context, ctx core.ExecutionContext, call m.ToolCall, _ func(core.Update) error) (json.RawMessage, error) {
		if ctx.Role != core.Summary || call.Name != "update_agent_profile" {
			t.Fatal("notification lost its execution binding")
		}
		effects++
		return json.RawMessage(`{"has_avatar":true}`), nil
	}
	service.model = func(request core.ModelRequest, call int) (string, error) {
		if !strings.Contains(string(request.Body), "fresh-notification-instructions") {
			t.Error("notification retained stale instructions")
		}
		if call == 0 {
			return response("", m.ToolCall{ID: "profile", Name: "update_agent_profile", Arguments: json.RawMessage(`{}`)}), nil
		}
		return response("done"), nil
	}
	if err := a.Execute(context.Background(), execution, thread, core.TurnRequest{RequestID: "pot-result-1", Text: "Task completed"}, false, func(core.AgentEvent) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if skills != 1 || effects != 1 {
		t.Fatal("notification skipped skills or follow-up operation", skills, effects)
	}
}
