package eval

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type runFeedback struct {
	mu          sync.Mutex
	file        *os.File
	runID, turn string
	started     time.Time
	seq         uint64
	state       json.RawMessage
	bytes       int
}

func (f *runFeedback) record(kind string, data any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	f.seq++
	e := Event{Schema, f.runID, f.seq, uint64(time.Since(f.started).Milliseconds()), f.turn, Payload{kind, raw}}
	raw, err = json.Marshal(e)
	if err != nil {
		return err
	}
	f.bytes += len(raw)
	if f.bytes > 64*1024*1024 || f.seq > 100000 {
		return fmt.Errorf("evaluation event limit exceeded")
	}
	if _, err = f.file.Write(append(raw, '\n')); err != nil {
		return err
	}
	return f.file.Sync()
}
func (f *runFeedback) LoadContext(context.Context) (json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append(json.RawMessage(nil), f.state...), nil
}
func (f *runFeedback) SaveContext(_ context.Context, raw json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = append(json.RawMessage(nil), raw...)
	return nil
}
func (f *runFeedback) RecordTurn(_ context.Context, id string) error {
	f.mu.Lock()
	f.turn = id
	f.mu.Unlock()
	return nil
}
func (f *runFeedback) InputStreaming(context.Context, string) error { return nil }
func (f *runFeedback) Ready(ctx context.Context) error              { return ctx.Err() }
func (f *runFeedback) Steering(context.Context, bool) ([]agent.SteeringInput, error) {
	return []agent.SteeringInput{}, nil
}
func (f *runFeedback) AcknowledgeSteering(context.Context, []string) error { return nil }
func (f *runFeedback) Observe(_ context.Context, o agent.Observation) error {
	return f.record("observation", o)
}
func (f *runFeedback) Settle(context.Context) error { return nil }

type captureServices struct {
	inner     agent.Services
	feedback  *runFeedback
	mu        sync.Mutex
	requests  []core.ModelRequest
	responses []ScriptedResponse
	scripted  bool
}

func (s *captureServices) Model(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
	s.mu.Lock()
	s.requests = append(s.requests, r)
	var response ScriptedResponse
	if s.scripted {
		if len(s.responses) == 0 {
			s.mu.Unlock()
			return nil, &core.ModelRequestError{Cause: fmt.Errorf("scripted model exhausted"), Disposition: "unsent"}
		}
		response = s.responses[0]
		s.responses = s.responses[1:]
	}
	s.mu.Unlock()
	if !s.scripted {
		stream, err := s.inner.Model(ctx, r)
		if err != nil {
			return nil, err
		}
		return &observedStream{ReadCloser: stream, feedback: s.feedback, request: r}, nil
	}
	stage := "agent"
	if r.CompactionID != nil {
		stage = "compaction"
	}
	if response.Stage != stage {
		return nil, &core.ModelRequestError{Cause: fmt.Errorf("expected %s request, got %s", response.Stage, stage), Disposition: "unsent"}
	}
	for _, fragment := range response.BodyContains {
		if !strings.Contains(string(r.Body), fragment) {
			return nil, &core.ModelRequestError{Cause: fmt.Errorf("request fragment missing: %s", fragment), Disposition: "unsent"}
		}
	}
	var body bytes.Buffer
	observer := modelprotocol.UsageObserver{}
	for i, frame := range response.Frames {
		if !json.Valid(frame) {
			return nil, fmt.Errorf("invalid native SSE frame")
		}
		body.WriteString("data: ")
		var compact bytes.Buffer
		if err := json.Compact(&compact, frame); err != nil {
			return nil, err
		}
		body.Write(compact.Bytes())
		body.WriteString("\n\n")
		usage, err := observer.Observe(modelprotocol.Protocol(r.Protocol), frame)
		if err != nil {
			return nil, err
		}
		if usage != nil {
			report := nativeReport(r.ID, fmt.Sprintf("native-%d", i), usage, frame, modelprotocol.Protocol(r.Protocol))
			if err = s.feedback.record("usage", report); err != nil {
				return nil, err
			}
		}
	}
	return io.NopCloser(bytes.NewReader(body.Bytes())), nil
}
func (s *captureServices) Compaction(ctx context.Context, raw json.RawMessage) error {
	var data core.CompactionReport
	if err := core.DecodeJSON(raw, &data); err != nil {
		return err
	}
	if data.Action != "evidence" {
		if err := s.feedback.record("observation", agent.Observation{Kind: "compaction_lifecycle", ID: data.ID, Stage: data.Action, Tokens: data.Tokens}); err != nil {
			return err
		}
	}
	if s.inner != nil {
		return s.inner.Compaction(ctx, raw)
	}
	return nil
}
func nativeReport(id, event string, u *modelprotocol.UsageReport, frame json.RawMessage, p modelprotocol.Protocol) UsageReport {
	r := UsageReport{MeasurementID: id, SourceEventID: event, Scope: "request", Source: "native", Counter: "snapshot", Complete: u.InputTokens != nil && u.OutputTokens != nil, InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens}
	var raw map[string]any
	if core.DecodeJSON(frame, &raw) == nil {
		if usage, ok := raw["usage"].(map[string]any); ok {
			if details, ok := usage["completion_tokens_details"].(map[string]any); ok {
				r.ReasoningTokens = number(details["reasoning_tokens"])
			}
			if p == modelprotocol.AnthropicMessages {
				r.CacheWriteTokens = number(usage["cache_creation_input_tokens"])
			}
		}
	}
	return r
}
func number(v any) *uint64 {
	if n, ok := v.(json.Number); ok {
		var u uint64
		if json.Unmarshal([]byte(n.String()), &u) == nil {
			return &u
		}
	}
	return nil
}

type RunOptions struct {
	RolePrompt string // Caller-owned product policy; empty for standalone generic evaluations.
	Composer   agent.Composer
	Services   agent.Services
	Mode       string
	Transport  json.RawMessage
	Tools      []modelprotocol.ToolDefinition
	Dispatch   agent.Dispatch
	Configure  func(*agent.Agent)
}

func WriteJSON(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0600)
}
func ReadScenario(path string) (Scenario, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Scenario{}, err
	}
	if len(raw) > 16*1024*1024 {
		return Scenario{}, fmt.Errorf("scenario exceeds limit")
	}
	var s Scenario
	err = core.DecodeJSON(raw, &s)
	return s, err
}
func resolve(s Scenario) (core.ResolvedSpec, error) {
	if s.ID == "" || len(s.ID) > 128 || len(s.Turns) == 0 || len(s.Turns) > 100 || s.DeadlineMS < 1 || s.DeadlineMS > 3600000 || len(s.TurnAssertions) != 0 && len(s.TurnAssertions) != len(s.Turns) {
		return core.ResolvedSpec{}, fmt.Errorf("invalid scenario identity/turns/deadline/assertions")
	}
	if err := modelprotocol.Protocol(s.Protocol).Validate(); err != nil || s.Protocol == "unknown" {
		return core.ResolvedSpec{}, fmt.Errorf("invalid scenario protocol")
	}
	for _, collection := range []map[string]string{s.Fixtures, s.ExpectedFiles} {
		for name, content := range collection {
			if !core.SafeRelativePath(name) || len(content) > 4*1024*1024 {
				return core.ResolvedSpec{}, fmt.Errorf("invalid fixture path/content")
			}
		}
	}
	spec, err := s.Profile.Resolve()
	if err != nil {
		return spec, err
	}
	spec.GatewayProtocol = s.Protocol
	if err = spec.Rehash(); err != nil {
		return spec, err
	}
	return spec, spec.Validate()
}
func Run(ctx context.Context, s Scenario, directory string, options RunOptions) (Report, error) {
	spec, err := resolve(s)
	if err != nil {
		return Report{}, err
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return Report{}, err
	}
	if err = os.Mkdir(directory, 0700); err != nil {
		return Report{}, err
	}
	workspace := filepath.Join(directory, "workspace")
	if err = os.Mkdir(workspace, 0700); err != nil {
		return Report{}, err
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return Report{}, err
	}
	for name, content := range s.Fixtures {
		if err = root.MkdirAll(filepath.Dir(name), 0700); err != nil {
			root.Close()
			return Report{}, err
		}
		if err = root.WriteFile(name, []byte(content), 0600); err != nil {
			root.Close()
			return Report{}, err
		}
	}
	root.Close()
	runID := newID()
	hash, err := core.HashJSON(s)
	if err != nil {
		return Report{}, err
	}
	mode := options.Mode
	if mode == "" {
		mode = "deterministic_contract"
	}
	if mode != "deterministic_contract" && mode != "native_policy_live" {
		return Report{}, fmt.Errorf("unsupported evaluation mode")
	}
	var revision *string
	if Revision != "" {
		revision = &Revision
	}
	manifest := Manifest{Schema, runID, s.ID, hash, mode, "pot-agent-go", runtime.Version(), revision, spec, s.DeadlineMS, options.Transport, "fixture-host"}
	if err = WriteJSON(filepath.Join(directory, "manifest.json"), manifest); err != nil {
		return Report{}, err
	}
	if err = WriteJSON(filepath.Join(directory, "scenario.json"), s); err != nil {
		return Report{}, err
	}
	file, err := os.OpenFile(filepath.Join(directory, "events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return Report{}, err
	}
	feedback := &runFeedback{file: file, runID: runID, started: time.Now()}
	services := &captureServices{inner: options.Services, feedback: feedback, requests: []core.ModelRequest{}, scripted: options.Services == nil, responses: append([]ScriptedResponse{}, s.Responses...)}
	engine := executor.New(executor.Options{Home: filepath.Join(directory, "home")})
	defer engine.Close()
	definitions := append([]modelprotocol.ToolDefinition{}, options.Tools...)
	for _, raw := range core.ExecutionTools(spec.Params.Agent.Shell) {
		var definition modelprotocol.ToolDefinition
		if err = core.DecodeJSON(raw, &definition); err != nil {
			return Report{}, err
		}
		definitions = append(definitions, definition)
	}
	loop := &agent.Agent{Services: services, Composer: options.Composer, Feedback: feedback, Tools: definitions, Dispatch: func(ctx context.Context, e core.ExecutionContext, call modelprotocol.ToolCall, emit func(core.Update) error) (json.RawMessage, error) {
		if call.Name != "exec_command" && call.Name != "write_stdin" && call.Name != "apply_patch" {
			if options.Dispatch != nil {
				return options.Dispatch(ctx, e, call, emit)
			}
			return nil, fmt.Errorf("fixture tool %s unavailable", call.Name)
		}
		var args map[string]any
		if err := core.DecodeJSON(call.Arguments, &args); err != nil {
			return nil, err
		}
		result, err := engine.Execute(ctx, executor.Request{Mode: "host", ID: call.ID, Operation: call.Name, CWD: e.Workspace, Policy: spec.Params.Agent.Shell, Args: args}, func(event executor.Event) error {
			return emit(core.Update{Type: "tool_output", ID: event.ToolOutput.ID, Stream: event.ToolOutput.Stream, Text: event.ToolOutput.Text})
		})
		if result == nil {
			return nil, err
		}
		data, e2 := json.Marshal(result)
		return data, errors.Join(err, e2)
	}, Cleanup: func(context.Context, core.ExecutionContext, bool) error { return engine.Drain() }}
	if options.Configure != nil {
		options.Configure(loop)
	}
	runctx, cancel := context.WithTimeout(ctx, time.Duration(s.DeadlineMS)*time.Millisecond)
	defer cancel()
	executionErrors := []string{}
	execution := core.ExecutionContext{Owner: "eval", Role: core.Worker, RolePrompt: options.RolePrompt, Data: directory, Workspace: workspace, SkillResources: core.SkillResources{Roots: []core.SkillRoot{}}}
	thread := core.Thread{Owner: "eval", Generation: 1, Config: spec}
	for i, text := range s.Turns {
		turn := fmt.Sprintf("turn-%d", i+1)
		feedback.RecordTurn(runctx, turn)
		err = loop.Execute(runctx, execution, thread, core.TurnRequest{RequestID: turn, Text: text}, false, func(event core.AgentEvent) error { return feedback.record("agent", event) })
		if err != nil {
			executionErrors = append(executionErrors, err.Error())
			break
		}
	}
	if runctx.Err() != nil {
		executionErrors = append(executionErrors, "Run deadline/cancellation exceeded")
	}
	services.mu.Lock()
	requests := append([]core.ModelRequest{}, services.requests...)
	remaining := len(services.responses)
	services.mu.Unlock()
	if services.scripted && remaining != 0 {
		executionErrors = append(executionErrors, fmt.Sprintf("%d scripted responses unused", remaining))
	}
	if err = file.Close(); err != nil {
		executionErrors = append(executionErrors, err.Error())
	}
	if err = WriteJSON(filepath.Join(directory, "execution.json"), executionErrors); err != nil {
		return Report{}, err
	}
	if err = WriteJSON(filepath.Join(directory, "requests.json"), requests); err != nil {
		return Report{}, err
	}
	state, _ := feedback.LoadContext(context.Background())
	if len(state) > 0 {
		if err = WriteJSON(filepath.Join(directory, "context.json"), state); err != nil {
			return Report{}, err
		}
	}
	report, err := Validate(directory)
	if err != nil {
		return report, err
	}
	if err = WriteJSON(filepath.Join(directory, "report.json"), report); err != nil {
		return report, err
	}
	return report, nil
}
func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", b[:])
}
