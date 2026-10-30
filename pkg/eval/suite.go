package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

const MemoryFinal = `{"items":{"apple":7,"pear":4,"plum":6},"tag":"blue"}`
const TotalFinal = `{"count":6,"total":21,"max":6}`

func SmallScenario(id string, turns []string, tools bool) Scenario {
	settings := core.DefaultAgentSettings()
	settings.Plugins = map[string]core.PluginConfig{}
	prompt := "Complete the task. Return only the requested output."
	settings.SystemPrompt = &prompt
	settings.AutoCompact = false
	settings.RequestTimeoutMS = 30000
	settings.BuiltinTools = []string{}
	if tools {
		settings.BuiltinTools = []string{"exec_command"}
	}
	settings.Shell.DefaultOutputBytes = 512
	settings.Shell.MaxOutputBytes = 512
	return Scenario{ID: id, Profile: core.Profile{ID: "eval", Revision: 1, Name: "Evaluation", Provider: "mock", Model: "deepseek-v4-flash", AuthRef: "provider:mock", Params: core.Parameters{MaxOutputTokens: 128, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}, Protocol: "openai_chat", Turns: turns, Responses: []ScriptedResponse{}, Fixtures: map[string]string{}, ExpectedFiles: map[string]string{}, DeadlineMS: 60000}
}
func assertion(text string, isJSON bool) OutputAssertion {
	if isJSON {
		return OutputAssertion{Kind: "json", Expected: json.RawMessage(text)}
	}
	raw, _ := json.Marshal(text)
	return OutputAssertion{Kind: "text", Expected: raw}
}

type fixtureCall struct {
	ID, Name  string
	Arguments any
}

func textResponse(text string) ScriptedResponse { return toolResponse(text) }
func toolResponse(text string, calls ...fixtureCall) ScriptedResponse {
	native := []any{}
	for i, c := range calls {
		args, _ := json.Marshal(c.Arguments)
		native = append(native, map[string]any{"index": i, "id": c.ID, "type": "function", "function": map[string]any{"name": c.Name, "arguments": string(args)}})
	}
	stop := "stop"
	if len(calls) > 0 {
		stop = "tool_calls"
	}
	raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": text, "tool_calls": native}, "finish_reason": stop}}, "usage": map[string]any{"prompt_tokens": 10, "completion_tokens": 5, "prompt_tokens_details": map[string]any{"cached_tokens": 0}}})
	return ScriptedResponse{Stage: "agent", Frames: []json.RawMessage{raw}}
}
func MemoryScenario() Scenario {
	s := SmallScenario("memory-five-v1", []string{"Remember items apple=2, pear=4, and tag=red. Reply exactly ok.", "Add item plum=6. Reply exactly ok.", "Replace apple's value with 7. Reply exactly ok.", "Replace tag with blue. Reply exactly ok.", "Return only JSON with keys items (all current item values) and tag (current value)."}, false)
	s.Profile.Params.MaxOutputTokens = 64
	for i := 0; i < 4; i++ {
		s.TurnAssertions = append(s.TurnAssertions, assertion("ok", false))
		s.Responses = append(s.Responses, textResponse("ok"))
	}
	s.TurnAssertions = append(s.TurnAssertions, assertion(MemoryFinal, true))
	s.Responses = append(s.Responses, textResponse(MemoryFinal))
	return s
}
func CacheScenarios() []Scenario {
	result := []Scenario{}
	for i, name := range []string{"a1", "a2", "a3", "tail", "head"} {
		text := "apple"
		if i == 3 {
			text = "pear"
		}
		s := SmallScenario("cache-"+name+"-v1", []string{"Reply exactly " + text + "."}, true)
		s.Profile.Params.MaxOutputTokens = 64
		if i == 4 {
			prompt := "Answer the task. Return only the requested output."
			s.Profile.Params.Agent.SystemPrompt = &prompt
		}
		s.TurnAssertions = []OutputAssertion{assertion(text, false)}
		s.Responses = []ScriptedResponse{textResponse(text)}
		result = append(result, s)
	}
	return result
}

type nodeRunner struct {
	rolePrompt string
	composer   agent.Composer
	connection *DeepSeekConnection
	budgets    []*LiveBudget
}

func (r nodeRunner) node(ctx context.Context, s Scenario, path string, maxRequests uint64, options RunOptions) (Report, error) {
	options.Composer = r.composer
	options.RolePrompt = r.rolePrompt
	if r.connection == nil {
		return Run(ctx, s, path, options)
	}
	s.Responses = []ScriptedResponse{}
	limits := LiveLimits{maxRequests, 30000}
	transport, err := r.connection.configure(&s, limits)
	if err != nil {
		return Report{}, err
	}
	budget, err := NewLiveBudget(limits, time.Duration(s.DeadlineMS)*time.Millisecond)
	if err != nil {
		return Report{}, err
	}
	service := &liveServices{connection: r.connection, directory: path, transport: transport, budget: budget, shared: r.budgets, client: liveHTTPClient(time.Duration(s.DeadlineMS) * time.Millisecond)}
	defer service.Close()
	options.Services = service
	options.Mode = "native_policy_live"
	options.Transport = transport
	return Run(ctx, s, path, options)
}
func sumReports(reports []Report) (Totals, error) {
	if len(reports) == 0 {
		return Totals{}, nil
	}
	sum := Totals{value(0), value(0), value(0), value(0), value(0), 0}
	for _, r := range reports {
		next, err := merge(sum, r.Usage, false)
		if err != nil {
			return Totals{}, err
		}
		next.Measurements = sum.Measurements + r.Usage.Measurements
		sum = next
	}
	return sum, nil
}

type delegation struct {
	mu        sync.Mutex
	runner    nodeRunner
	directory string
	children  map[string]*Report
	lifecycle *os.File
}

func (d *delegation) dispatch(ctx context.Context, _ core.ExecutionContext, call modelprotocol.ToolCall, _ func(core.Update) error) (json.RawMessage, error) {
	var args struct {
		Partition string `json:"partition"`
	}
	if call.Name != "dispatch_partition" {
		return nil, errors.New("unknown evaluation delegation tool")
	}
	if err := core.DecodeJSON(call.Arguments, &args); err != nil {
		return nil, err
	}
	part := args.Partition
	if part != "left" && part != "right" {
		return nil, errors.New("invalid partition")
	}
	d.mu.Lock()
	if report, ok := d.children[part]; ok {
		d.mu.Unlock()
		if report == nil || !report.Success {
			return nil, errors.New("partition already active or failed")
		}
		return json.Marshal(map[string]any{"task_id": part, "result": json.RawMessage(report.TurnAnswers["turn-1"])})
	}
	d.children[part] = nil
	d.mu.Unlock()
	if err := d.record(map[string]any{"kind": "child_started", "parent": "parent", "task_id": part}); err != nil {
		return nil, err
	}
	csv := "amount\n1\n2\n3\n"
	expected := `{"count":3,"total":6,"max":3}`
	if part == "right" {
		csv = "amount\n4\n5\n6\n"
		expected = `{"count":3,"total":15,"max":6}`
	}
	s := SmallScenario("child-"+part+"-v1", []string{"Use exec_command to read part.csv. Compute count, sum of amount, and maximum amount. Return only JSON with integer keys count, total, max."}, true)
	s.Fixtures["part.csv"] = csv
	s.TurnAssertions = []OutputAssertion{assertion(expected, true)}
	s.Responses = []ScriptedResponse{toolResponse("", fixtureCall{"read", "exec_command", map[string]any{"cmd": "cat part.csv", "login": false}}), textResponse(expected)}
	report, err := d.runner.node(ctx, s, filepath.Join(d.directory, part), 4, RunOptions{})
	d.mu.Lock()
	d.children[part] = &report
	d.mu.Unlock()
	recordErr := d.record(map[string]any{"kind": "child_finished", "parent": "parent", "task_id": part, "run_id": report.RunID, "success": err == nil && report.Success})
	if err != nil || recordErr != nil {
		return nil, errors.Join(err, recordErr)
	}
	if !report.Success || report.ToolCount == 0 {
		return nil, errors.New("child assertion or partition read failed")
	}
	return json.Marshal(map[string]any{"task_id": part, "result": json.RawMessage(report.TurnAnswers["turn-1"])})
}
func (d *delegation) record(v any) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err = d.lifecycle.Write(append(raw, '\n')); err != nil {
		return err
	}
	return d.lifecycle.Sync()
}

type TreeReport struct {
	Parent   Report             `json:"parent"`
	Children map[string]*Report `json:"children"`
	Complete bool               `json:"tree_complete"`
	Usage    Totals             `json:"tree_usage"`
	Limits   map[string]any     `json:"tree_limits"`
	Success  bool               `json:"success"`
}

func runTree(ctx context.Context, r nodeRunner, path string) (TreeReport, error) {
	var result TreeReport
	if err := os.Mkdir(path, 0700); err != nil {
		return result, err
	}
	budget, _ := NewLiveBudget(LiveLimits{8, 30000}, 120*time.Second)
	r.budgets = append([]*LiveBudget{budget}, r.budgets...)
	file, err := os.OpenFile(filepath.Join(path, "lifecycle.jsonl"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, err
	}
	defer file.Close()
	d := &delegation{runner: r, directory: path, children: map[string]*Report{}, lifecycle: file}
	s := SmallScenario("split-parent-v1", []string{"Dispatch left and right using dispatch_partition exactly once each. Combine the two child results. Return only JSON with integer keys count, total, max. Do not compute results without dispatching both tasks."}, false)
	s.TurnAssertions = []OutputAssertion{assertion(TotalFinal, true)}
	s.Responses = []ScriptedResponse{toolResponse("", fixtureCall{"left", "dispatch_partition", map[string]any{"partition": "left"}}, fixtureCall{"right", "dispatch_partition", map[string]any{"partition": "right"}}), textResponse(TotalFinal)}
	tool := modelprotocol.ToolDefinition{Name: "dispatch_partition", Description: "Run one child Agent on the named data partition and return its verified JSON statistics. Dispatch left and right once each, then combine their results.", InputSchema: json.RawMessage(`{"type":"object","properties":{"partition":{"type":"string","enum":["left","right"]}},"required":["partition"],"additionalProperties":false}`)}
	result.Parent, err = r.node(ctx, s, filepath.Join(path, "parent"), 4, RunOptions{Tools: []modelprotocol.ToolDefinition{tool}, Dispatch: d.dispatch})
	if err != nil {
		return result, err
	}
	d.mu.Lock()
	result.Children = d.children
	d.mu.Unlock()
	result.Complete = len(result.Children) == 2
	reports := []Report{result.Parent}
	result.Success = result.Parent.Success && result.Parent.ToolCount == 2
	for _, child := range result.Children {
		if child == nil {
			result.Complete = false
			result.Success = false
		} else {
			reports = append(reports, *child)
			result.Success = result.Success && child.Success && child.ToolCount > 0
		}
	}
	result.Success = result.Success && result.Complete
	if result.Complete {
		result.Usage, err = sumReports(reports)
		if err != nil {
			return result, err
		}
	}
	result.Limits = budget.Snapshot()
	return result, nil
}
func RunTree(ctx context.Context, path string, connection *DeepSeekConnection, composer agent.Composer, rolePrompt string) (TreeReport, error) {
	result, err := runTree(ctx, nodeRunner{rolePrompt: rolePrompt, composer: composer, connection: connection}, path)
	if err == nil {
		err = WriteJSON(filepath.Join(path, "summary.json"), result)
	}
	return result, err
}
func RunSuite(ctx context.Context, path string, connection *DeepSeekConnection, composer agent.Composer, rolePrompt string) (map[string]any, error) {
	if err := os.Mkdir(path, 0700); err != nil {
		return nil, err
	}
	budget, _ := NewLiveBudget(LiveLimits{22, 50000}, 300*time.Second)
	r := nodeRunner{rolePrompt: rolePrompt, composer: composer, connection: connection, budgets: []*LiveBudget{budget}}
	mode := "deterministic"
	if connection != nil {
		mode = "live"
	}
	if err := WriteJSON(filepath.Join(path, "suite-manifest.json"), map[string]any{"schema_version": Schema, "suite": "small-budget-v1", "mode": mode, "trials": 1, "limits": budget.Snapshot(), "execution_target": "fixture-host", "tree_policy": "directed serial depth=1 children=2", "empirical_efficiency_claim": false}); err != nil {
		return nil, err
	}
	memory, err := r.node(ctx, MemoryScenario(), filepath.Join(path, "memory"), 5, RunOptions{})
	if err != nil {
		return nil, err
	}
	caches := []Report{}
	for i, s := range CacheScenarios() {
		if connection != nil && i > 0 {
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		report, err := r.node(ctx, s, filepath.Join(path, fmt.Sprintf("cache-%d", i)), 1, RunOptions{})
		if err != nil {
			return nil, err
		}
		caches = append(caches, report)
	}
	identities := []string{}
	for i := range caches {
		var requests []core.ModelRequest
		if err = readJSON(filepath.Join(path, fmt.Sprintf("cache-%d", i), "requests.json"), &requests); err != nil {
			return nil, err
		}
		if len(requests) != 1 {
			return nil, errors.New("cache scenario did not have one native request")
		}
		hash, err := core.HashJSON(requests[0].Body)
		if err != nil {
			return nil, err
		}
		identities = append(identities, hash)
	}
	if identities[0] != identities[1] || identities[1] != identities[2] || identities[0] == identities[3] || identities[0] == identities[4] {
		return nil, errors.New("cache repeat/prefix/suffix request identity mismatch")
	}
	single := SmallScenario("single-worker-v1", []string{"Use exec_command to read left.csv and right.csv. Compute the combined count, sum of amount, and maximum amount. Return only JSON with integer keys count, total, max."}, true)
	single.Fixtures = map[string]string{"left.csv": "amount\n1\n2\n3\n", "right.csv": "amount\n4\n5\n6\n"}
	single.TurnAssertions = []OutputAssertion{assertion(TotalFinal, true)}
	single.Responses = []ScriptedResponse{toolResponse("", fixtureCall{"read", "exec_command", map[string]any{"cmd": "cat left.csv right.csv", "login": false}}), textResponse(TotalFinal)}
	baseline, err := r.node(ctx, single, filepath.Join(path, "single-worker"), 4, RunOptions{})
	if err != nil {
		return nil, err
	}
	tree, err := runTree(ctx, r, filepath.Join(path, "split"))
	if err != nil {
		return nil, err
	}
	faults, err := RunFaultSuite(ctx, filepath.Join(path, "faults"))
	if err != nil {
		return nil, err
	}
	success := memory.Success && memory.RequestCount == 5 && memory.ToolCount == 0 && baseline.Success && baseline.ToolCount > 0 && tree.Success && faults["success"] == true
	for _, cache := range caches {
		success = success && cache.Success && cache.RequestCount == 1 && cache.ToolCount == 0
	}
	summary := map[string]any{"schema_version": Schema, "suite": "small-budget-v1", "mode": mode, "success": success, "memory": memory, "cache": caches, "cache_request_hashes": identities, "cache_condition": "uncontrolled; first request is not guaranteed cold", "delegation": map[string]any{"single": baseline, "tree": tree}, "faults": faults, "budget": budget.Snapshot(), "empirical_efficiency_claim": false}
	if err = WriteJSON(filepath.Join(path, "summary.json"), summary); err != nil {
		return nil, err
	}
	return summary, nil
}
func fixtureStream(response ScriptedResponse) io.ReadCloser {
	var body bytes.Buffer
	for _, frame := range response.Frames {
		var compact bytes.Buffer
		json.Compact(&compact, frame)
		body.WriteString("data: ")
		body.Write(compact.Bytes())
		body.WriteString("\n\n")
	}
	return io.NopCloser(bytes.NewReader(body.Bytes()))
}
