package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

type routingTask struct {
	Info   protocol.TaskInfo
	User   string
	Brief  string
	Result string
}

type routingCase struct {
	ID         string
	Input      string
	Tasks      []routingTask
	WantCreate int
	WantSend   []string
	RequireGet string
}

type routingCall struct {
	Name   string
	Args   json.RawMessage
	Error  string
	Result json.RawMessage
}

func routingCases() []routingCase {
	task := func(id, user, brief string, phase protocol.TaskPhase, archived bool, result string) routingTask {
		return routingTask{Info: protocol.TaskInfo{ID: id, Title: strings.SplitN(brief, "\n", 2)[0], Phase: phase, Archived: archived, RunID: "run-" + id, AssistantID: "eval", MachineID: "eval-node", UpdatedAtMS: 1000, Address: &protocol.AgentRef{NodeID: "eval-node", Kind: "task", AgentID: id}}, User: user, Brief: brief, Result: result}
	}
	scroll := task("task-a", "请修复 Pot 仓库聊天窗口的滚动跳动。", "稳定聊天阅读位置\n修复 Pot 聊天窗口消息组件高度变化时的滚动跳动，并验证长消息和底部跟随。", protocol.TaskRunning, false, "")
	avatar := task("task-a", "请实现 Pot 助手设置页的头像裁切。", "助手头像裁切\n在 Pot 助手设置页增加头像裁切预览、保存和恢复默认头像。", protocol.TaskRunning, false, "")
	model := task("task-a", "请在 Pot 助手设置页增加默认 Task 模型选项。", "默认 Task 模型\n支持保存默认 Task 模型，不选则跟随助手；启动 Task 时显式指定模型仍然优先。完成前后端并验证。", protocol.TaskRunning, false, "")
	pdf := task("task-a", "请实现聊天记录导出 PDF。", "聊天记录 PDF 导出\n导出完整聊天记录，保留表格、代码块和附件引用，并验证内容。", protocol.TaskComplete, true, "已实现聊天记录 PDF 导出并验证表格与代码块。")
	vague := task("task-a", "帮我处理设置页问题。", "设置页调整\n完成讨论过的设置页问题并验证。", protocol.TaskComplete, false, "已完成助手设置页头像裁切：裁切预览、保存和恢复默认头像均可用。")
	older := scroll
	newer := model
	newer.Info.ID = "task-b"
	newer.Info.RunID = "run-task-b"
	newer.Info.UpdatedAtMS = 2000
	newer.Info.Address = &protocol.AgentRef{NodeID: "eval-node", Kind: "task", AgentID: "task-b"}
	done := scroll
	done.Info.Phase = protocol.TaskComplete
	done.Result = "聊天高度变化时阅读锚点已稳定。"
	return []routingCase{
		{ID: "same-repo-new-feature", Input: "Pot 仓库里助手的模型列表还只能填名字，请做成能从 models.dev 缓存选择，也能自由输入的控件。", Tasks: []routingTask{scroll}, WantCreate: 1},
		{ID: "same-page-new-deliverable", Input: "助手设置页还需要一个默认 Task 模型选项，保存后创建 Task 时使用它；不选就跟随助手模型。", Tasks: []routingTask{avatar}, WantCreate: 1},
		{ID: "same-goal-constraint", Input: "补充一点：只有原来就在底部时才跟随新消息；用户翻阅历史时必须保持阅读位置。", Tasks: []routingTask{scroll}, WantSend: []string{"task-a"}},
		{ID: "archived-goal-bug", Input: "导出的 PDF 里表格超过一页就被截断了，请把这个问题修好并补上覆盖。", Tasks: []routingTask{pdf}, WantSend: []string{"task-a"}},
		{ID: "same-goal-cross-layer", Input: "后端也要落实模型优先级：启动时显式指定 > 保存的 Task 默认值 > 助手当前模型；补齐这部分测试。", Tasks: []routingTask{model}, WantSend: []string{"task-a"}},
		{ID: "mixed-goals", Input: "滚动修复还要覆盖超长链接；另外助手模型列表请改成能选缓存模型、也能自由输入的控件。", Tasks: []routingTask{scroll}, WantCreate: 1, WantSend: []string{"task-a"}},
		{ID: "explicit-new", Input: "请新建一个任务，给聊天滚动建立性能基准，覆盖长列表与长链接。", Tasks: []routingTask{scroll}, WantCreate: 1},
		{ID: "explicit-merge", Input: "把 Provider 选择控件也合并到刚才的默认 Task 模型任务里一起实现。", Tasks: []routingTask{model}, WantSend: []string{"task-a"}},
		{ID: "insufficient-metadata", Input: "助手设置页要增加默认 Task 模型的选择并保存，不选则跟随助手。", Tasks: []routingTask{vague}, WantCreate: 1, RequireGet: "task-a"},
		{ID: "older-matching-goal", Input: "聊天长消息展开后阅读位置仍然会漂移，请修好这个情况。", Tasks: []routingTask{older, newer}, WantSend: []string{"task-a"}},
		{ID: "two-new-goals", Input: "实现聊天消息全文搜索，包括索引、结果跳转和测试；账单页面还要能导出 CSV，包含日期、模型和费用列。", Tasks: []routingTask{avatar}, WantCreate: 2},
		{ID: "completed-unrelated", Input: "请实现聊天记录导出 PDF，保留表格和代码块，并验证多页内容。", Tasks: []routingTask{done}, WantCreate: 1},
	}
}

func routingCounts(tasks []routingTask, query string) protocol.TaskCounts {
	var c protocol.TaskCounts
	q := strings.ToLower(query)
	for _, t := range tasks {
		hay := strings.ToLower(t.Info.ID + " " + t.Info.Title + " " + t.Brief)
		if q != "" && !strings.Contains(hay, q) {
			continue
		}
		if t.Info.Archived {
			c.Archived++
			continue
		}
		switch t.Info.Phase {
		case protocol.TaskQueued, protocol.TaskWaiting, protocol.TaskRunning, protocol.TaskWaitingApproval:
			c.Active++
		case protocol.TaskFailed, protocol.TaskInterrupted, protocol.TaskComplete, protocol.TaskCancelled:
			c.Attention++
		}
		switch t.Info.Phase {
		case protocol.TaskComplete:
			c.Complete++
		case protocol.TaskFailed:
			c.Failed++
		case protocol.TaskInterrupted:
			c.Interrupted++
		case protocol.TaskWaitingApproval:
			c.WaitingApproval++
		}
	}
	return c
}

func routingFixtureHash(rc routingCase, cp agent.Checkpoint) (string, error) {
	payload := struct {
		Version    string
		Case       routingCase
		Checkpoint agent.Checkpoint
	}{Version: "task-goal-routing-fixture-v3", Case: rc, Checkpoint: cp}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func routingDispatch(tasks []routingTask, calls *[]routingCall) agent.Dispatch {
	var mu sync.Mutex
	index := map[string]routingTask{}
	for _, t := range tasks {
		index[t.Info.ID] = t
	}
	return func(ctx context.Context, ec core.ExecutionContext, tc modelprotocol.ToolCall, upd func(core.Update) error) (json.RawMessage, error) {
		mu.Lock()
		rec := routingCall{Name: tc.Name, Args: append(json.RawMessage(nil), tc.Arguments...)}
		defer func() { *calls = append(*calls, rec); mu.Unlock() }()
		fail := func(msg string) (json.RawMessage, error) { rec.Error = msg; return nil, errors.New(msg) }
		if err := ctx.Err(); err != nil {
			return fail(err.Error())
		}
		switch tc.Name {
		case "create_task":
			var in protocol.CreateTask
			var fields map[string]json.RawMessage
			if err := core.DecodeJSON(tc.Arguments, &fields); err != nil {
				return fail(err.Error())
			}
			if fields == nil {
				return fail("create_task bad json")
			}
			requestID := tc.ID
			if requestID == "" {
				requestID = "eval-create"
			}
			encodedID, err := json.Marshal(requestID)
			if err != nil {
				return fail(err.Error())
			}
			fields["request_id"] = encodedID
			injected, err := json.Marshal(fields)
			if err != nil {
				return fail(err.Error())
			}
			if err := core.DecodeJSON(injected, &in); err != nil {
				return fail(err.Error())
			}
			if in.Target != nil && *in.Target != (protocol.AgentRef{NodeID: "eval-node", Kind: "assistant", AgentID: "eval"}) {
				return fail("create_task target outside fixture assistant")
			}
			parts := strings.SplitN(in.Text, "\n", 2)
			if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
				return fail("create_task needs title\\nbody")
			}
			info := protocol.TaskInfo{ID: fmt.Sprintf("task-%d", len(index)+1), Title: parts[0], Phase: protocol.TaskQueued, Address: &protocol.AgentRef{NodeID: "eval-node", Kind: "task", AgentID: fmt.Sprintf("task-%d", len(index)+1)}}
			index[info.ID] = routingTask{Info: info, Brief: in.Text}
			tasks = append(tasks, index[info.ID])
			b, _ := json.Marshal(info)
			rec.Result = b
			return b, nil
		case "list_tasks":
			var in struct {
				Scope  string `json:"scope"`
				Phase  string `json:"phase"`
				Q      string `json:"q"`
				Offset uint64 `json:"offset"`
				Limit  uint64 `json:"limit"`
			}
			if err := json.Unmarshal(tc.Arguments, &in); err != nil {
				return fail("list_tasks bad json")
			}
			items := make([]protocol.TaskInfo, 0, len(tasks))
			for _, t := range tasks {
				info := t.Info
				if in.Phase != "" && string(info.Phase) != in.Phase {
					continue
				}
				if in.Q != "" && !strings.Contains(strings.ToLower(info.ID+" "+info.Title+" "+t.Brief), strings.ToLower(in.Q)) {
					continue
				}
				scope := in.Scope
				if scope == "" {
					scope = "active"
				}
				if in.Phase == "" && scope != "all" && info.Scope() != scope {
					continue
				}
				items = append(items, info)
			}
			sort.Slice(items, func(i, j int) bool {
				if items[i].UpdatedAtMS != items[j].UpdatedAtMS {
					return items[i].UpdatedAtMS > items[j].UpdatedAtMS
				}
				return items[i].ID < items[j].ID
			})
			limit := in.Limit
			if limit > 50 {
				return fail("list_tasks limit exceeds 50")
			}
			if limit == 0 {
				limit = 10
			}
			off := in.Offset
			if off > uint64(len(items)) {
				off = uint64(len(items))
			}
			end := off + limit
			if end > uint64(len(items)) {
				end = uint64(len(items))
			}
			page := protocol.TaskPage{Items: items[off:end], Total: uint64(len(items)), Offset: off, Counts: routingCounts(tasks, in.Q)}
			if end < uint64(len(items)) {
				n := end
				page.NextOffset = &n
			}
			b, _ := json.Marshal(page)
			rec.Result = b
			return b, nil
		case "get_task":
			var in struct {
				TaskID string `json:"task_id"`
			}
			if err := json.Unmarshal(tc.Arguments, &in); err != nil {
				return fail("get_task bad json")
			}
			t, ok := index[in.TaskID]
			if !ok {
				return fail("get_task unknown id")
			}
			out := map[string]any{"task": t.Info, "error": ""}
			if t.Result != "" {
				out["result"] = protocol.TaskResultEvent{TaskID: t.Info.ID, RunID: t.Info.RunID, Title: t.Info.Title, Text: t.Result, Phase: t.Info.Phase}
			} else {
				out["result"] = nil
			}
			b, _ := json.Marshal(out)
			rec.Result = b
			return b, nil
		case "send_message":
			var in struct {
				To   protocol.AgentRef `json:"to"`
				Text string            `json:"text"`
			}
			if err := json.Unmarshal(tc.Arguments, &in); err != nil {
				return fail("send_message bad json")
			}
			if in.To.NodeID == "" || in.To.Kind != "task" || in.To.AgentID == "" {
				return fail("send_message invalid address")
			}
			if task, ok := index[in.To.AgentID]; !ok || task.Info.Address == nil || in.To != *task.Info.Address {
				return fail("send_message unknown task")
			}
			if strings.TrimSpace(in.Text) == "" {
				return fail("send_message empty text")
			}
			b, _ := json.Marshal(map[string]any{"accepted": true, "input_state": "pending"})
			rec.Result = b
			return b, nil
		default:
			return fail("unknown tool " + tc.Name)
		}
	}
}

func routingVerify(calls []routingCall, c routingCase) []string {
	var errs []string
	creates := 0
	var sends []string
	mutated := false
	sawGetOK := false
	for _, call := range calls {
		if call.Error != "" {
			errs = append(errs, "error "+call.Name+": "+call.Error)
			continue
		}
		switch call.Name {
		case "create_task":
			creates++
			mutated = true
		case "send_message":
			var in struct {
				To struct {
					AgentID string `json:"agent_id"`
				} `json:"to"`
			}
			_ = json.Unmarshal(call.Args, &in)
			sends = append(sends, in.To.AgentID)
			mutated = true
		case "list_tasks":
		case "get_task":
			var in struct {
				TaskID string `json:"task_id"`
			}
			_ = json.Unmarshal(call.Args, &in)
			if c.RequireGet != "" && in.TaskID == c.RequireGet {
				if mutated {
					errs = append(errs, "get_task after mutation")
				}
				sawGetOK = true
			}
		default:
			errs = append(errs, "unknown tool "+call.Name)
		}
	}
	if creates != c.WantCreate {
		errs = append(errs, fmt.Sprintf("create count %d want %d", creates, c.WantCreate))
	}
	ws := append([]string(nil), c.WantSend...)
	sort.Strings(ws)
	ss := append([]string(nil), sends...)
	sort.Strings(ss)
	if strings.Join(ws, ",") != strings.Join(ss, ",") {
		errs = append(errs, fmt.Sprintf("sends %v want %v", ss, ws))
	}
	if c.RequireGet != "" && !sawGetOK {
		errs = append(errs, "missing get_task "+c.RequireGet)
	}
	return errs
}

func routingCheckpoint(tasks []routingTask) (agent.Checkpoint, error) {
	cp := agent.Checkpoint{Type: "context", Version: 2, Messages: []modelprotocol.Message{modelprotocol.TextMessage("user", "项目是 Pot，源码目录 /workspace/pot；沿用仓库当前技术栈和配置。")}, Pending: []modelprotocol.ToolCall{}, Receipts: []agent.ToolReceipt{}, SteeringIDs: []string{}}
	for _, t := range tasks {
		cp.Messages = append(cp.Messages, modelprotocol.TextMessage("user", t.User))
		args, err := json.Marshal(map[string]any{"text": t.Brief})
		if err != nil {
			return cp, err
		}
		call := modelprotocol.ToolCall{ID: "c-" + t.Info.ID, Name: "create_task", Arguments: args}
		message := modelprotocol.TextMessage("assistant", "")
		message.Calls = []modelprotocol.ToolCall{call}
		cp.Messages = append(cp.Messages, message)
		receiptInfo := t.Info
		receiptInfo.Phase = protocol.TaskQueued
		receiptInfo.Archived = false
		receipt, err := json.Marshal(receiptInfo)
		if err != nil {
			return cp, err
		}
		message = modelprotocol.TextMessage("tool", string(receipt))
		message.CallID = call.ID
		cp.Messages = append(cp.Messages, message)
		cp.Messages = append(cp.Messages, modelprotocol.TextMessage("assistant", ""))
	}
	// Current catalogue metadata is an observed historical tool result, not an
	// invented runtime section. Case 9 deliberately keeps its result text hidden.
	listCall := modelprotocol.ToolCall{ID: "catalogue-before-input", Name: "list_tasks", Arguments: json.RawMessage(`{"scope":"all","q":"task-"}`)}
	message := modelprotocol.TextMessage("assistant", "")
	message.Calls = []modelprotocol.ToolCall{listCall}
	cp.Messages = append(cp.Messages, message)
	items := make([]protocol.TaskInfo, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, task.Info)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].UpdatedAtMS > items[j].UpdatedAtMS })
	raw, err := json.Marshal(protocol.TaskPage{Items: items, Total: uint64(len(items)), Counts: routingCounts(tasks, "task-")})
	if err != nil {
		return cp, err
	}
	message = modelprotocol.TextMessage("tool", string(raw))
	message.CallID = listCall.ID
	cp.Messages = append(cp.Messages, message)
	return cp, nil
}

func routingSchema() ([]modelprotocol.ToolDefinition, error) {
	raws := core.ProductTools()
	defs := make([]modelprotocol.ToolDefinition, 0, len(raws))
	for _, raw := range raws {
		var d modelprotocol.ToolDefinition
		if err := json.Unmarshal(raw, &d); err != nil {
			return nil, err
		}
		switch d.Name {
		case "create_task", "list_tasks", "get_task", "send_message":
			defs = append(defs, d)
		}
	}
	return defs, nil
}

// routingModel injects an approved transport; model and tool execution stay separate.
type routingModel struct {
	Name        string
	Profile     core.Profile
	Protocol    string
	OutputCap   uint64
	NewServices func(string) (agent.Services, func(), error)
}
type routingCappedServices struct {
	inner         agent.Services
	local, global *LiveBudget
	output        uint64
	mu            sync.Mutex
	seen          map[[32]byte]bool
}

func (s *routingCappedServices) Model(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
	if r.CompactionID != nil {
		return nil, errors.New("routing eval forbids compaction")
	}
	var limits struct {
		MaxTokens     uint64 `json:"max_tokens"`
		MaxOutput     uint64 `json:"max_output_tokens"`
		MaxCompletion uint64 `json:"max_completion_tokens"`
	}
	if err := json.Unmarshal(r.Body, &limits); err != nil {
		return nil, err
	}
	cap := max(limits.MaxTokens, limits.MaxOutput, limits.MaxCompletion)
	if cap == 0 || cap > s.output {
		return nil, errors.New("routing output cap missing or exceeded")
	}
	fingerprint := sha256.Sum256(r.Body)
	s.mu.Lock()
	if s.seen == nil {
		s.seen = map[[32]byte]bool{}
	}
	repeated := s.seen[fingerprint]
	s.seen[fingerprint] = true
	s.mu.Unlock()
	if repeated {
		return nil, errors.New("routing eval forbids retrying the same model request")
	}
	units := uint64(len(r.Body)) + cap
	if err := s.local.reserve(units); err != nil {
		return nil, err
	}
	if s.global != nil {
		if err := s.global.reserve(units); err != nil {
			return nil, fmt.Errorf("routing global budget: %w", err)
		}
	}
	stream, err := s.inner.Model(ctx, r)
	if err != nil {
		// The production loop retries typed transient errors. This bounded
		// experiment records the failure and never repeats a paid request.
		var requestError *core.ModelRequestError
		if errors.As(err, &requestError) {
			return nil, &core.ModelRequestError{Disposition: requestError.Disposition, Cause: errors.New(requestError.Cause.Error())}
		}
		return nil, errors.New(err.Error())
	}
	return &routingNoRetryStream{ReadCloser: stream}, nil
}

type routingNoRetryStream struct{ io.ReadCloser }

func (s *routingNoRetryStream) Read(b []byte) (int, error) {
	n, err := s.ReadCloser.Read(b)
	if err != nil && err != io.EOF {
		err = errors.New(err.Error())
	}
	return n, err
}
func (routingCappedServices) Compaction(context.Context, json.RawMessage) error {
	return errors.New("routing eval forbids compaction")
}

func routingDeepSeekModel(c *DeepSeekConnection) routingModel {
	s := SmallScenario("routing", []string{"routing"}, false)
	s.Profile.Params.MaxOutputTokens = 4096
	none := "none"
	s.Profile.Params.ReasoningEffort = &none
	limits := LiveLimits{6, 400000}
	transport, err := c.configure(&s, limits)
	return routingModel{Name: "deepseek-flash", Profile: s.Profile, Protocol: s.Protocol, OutputCap: 4096, NewServices: func(dir string) (agent.Services, func(), error) {
		if err != nil {
			return nil, nil, err
		}
		budget, e := NewLiveBudget(limits, 150*time.Second)
		if e != nil {
			return nil, nil, e
		}
		svc := &liveServices{connection: c, directory: dir, transport: transport, budget: budget, client: liveHTTPClient(150 * time.Second)}
		return svc, svc.Close, nil
	}}
}

func routingRun(ctx context.Context, prompt string, rc routingCase, dir string, model routingModel, global *LiveBudget, scripted []ScriptedResponse) (map[string]any, error) {
	report := map[string]any{"case_id": rc.ID, "model": model.Name, "routing_pass": false, "semantic_review_required": true, "tool_scope": "production create_task/list_tasks/get_task/send_message schemas; memory-only task stubs"}
	if err := os.Mkdir(dir, 0700); err != nil {
		return report, err
	}
	s := SmallScenario(rc.ID, []string{rc.Input}, false)
	s.Profile = model.Profile
	s.Protocol = model.Protocol
	s.DeadlineMS = 150000
	s.Profile.Params.Agent.SystemPrompt = nil
	s.Profile.Params.Agent.BuiltinTools = []string{}
	s.Profile.Params.Agent.AllowedTools = nil
	s.Profile.Params.Agent.Plugins = map[string]core.PluginConfig{}
	s.Profile.Params.Agent.AutoCompact = false
	s.Profile.Params.Agent.ModelCatalog = nil
	s.Profile.Params.Agent.RequestTimeoutMS = 60000
	s.Profile.Params.MaxOutputTokens = model.OutputCap
	if s.Profile.ModelSettings != nil {
		settings := *s.Profile.ModelSettings
		cap := model.OutputCap
		settings.MaxOutputTokens = &cap
		s.Profile.ModelSettings = &settings
	}
	resolved, err := resolve(s)
	if err != nil {
		return report, err
	}
	defs, err := routingSchema()
	if err != nil {
		return report, err
	}
	cp, err := routingCheckpoint(rc.Tasks)
	if err != nil {
		return report, err
	}
	cpb, err := json.Marshal(cp)
	if err != nil {
		return report, err
	}
	fixtureHash, err := routingFixtureHash(rc, cp)
	if err != nil {
		return report, err
	}
	report["fixture_version"] = "task-goal-routing-fixture-v3"
	report["fixture_sha256"] = fixtureHash
	checkpointHash := sha256.Sum256(cpb)
	report["initial_checkpoint_sha256"] = hex.EncodeToString(checkpointHash[:])
	schema, err := json.Marshal(defs)
	if err != nil {
		return report, err
	}
	ph, sh := sha256.Sum256([]byte(prompt)), sha256.Sum256(schema)
	report["prompt_sha256"], report["schema_sha256"] = hex.EncodeToString(ph[:]), hex.EncodeToString(sh[:])
	for name, value := range map[string]any{"case.json": rc, "checkpoint.initial.json": cp, "tools.json": defs, "spec.json": resolved} {
		if err := WriteJSON(filepath.Join(dir, name), value); err != nil {
			return report, err
		}
	}
	events, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return report, err
	}
	defer events.Close()
	fb := &runFeedback{file: events, runID: rc.ID, turn: "decision", started: time.Now(), state: cpb}
	budget, err := NewLiveBudget(LiveLimits{6, 400000}, 150*time.Second)
	if err != nil {
		return report, err
	}
	capture := &captureServices{feedback: fb, scripted: scripted != nil, responses: scripted}
	if scripted == nil {
		if model.NewServices == nil {
			return report, errors.New("missing live model services")
		}
		svc, closeSvc, e := model.NewServices(dir)
		if e != nil {
			return report, e
		}
		if closeSvc != nil {
			defer closeSvc()
		}
		capture.inner = svc
	}
	var calls []routingCall
	loop := &agent.Agent{Services: &routingCappedServices{inner: capture, local: budget, global: global, output: model.OutputCap}, Feedback: fb, Tools: defs, Dispatch: routingDispatch(rc.Tasks, &calls), Cleanup: func(context.Context, core.ExecutionContext, bool) error { return nil }}
	runctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	executeErr := loop.Execute(runctx, core.ExecutionContext{Owner: "eval", Role: core.Main, MainPrompt: prompt, Data: dir, Workspace: dir}, core.Thread{Owner: "eval", Generation: 1, Config: resolved}, core.TurnRequest{RequestID: "decision", Text: rc.Input}, false, func(e core.AgentEvent) error { return fb.record("agent", e) })
	verify := routingVerify(calls, rc)
	decisionCase := rc
	decisionCase.RequireGet = ""
	decisionErrors := routingVerify(calls, decisionCase)
	inspectionErrors := []string{}
	for _, problem := range verify {
		if strings.HasPrefix(problem, "missing get_task ") || problem == "get_task after mutation" {
			inspectionErrors = append(inspectionErrors, problem)
		}
	}
	report["action_shape_pass"] = len(decisionErrors) == 0
	report["action_shape_errors"] = decisionErrors
	report["inspection_pass"] = len(inspectionErrors) == 0
	report["inspection_errors"] = inspectionErrors
	report["execution_pass"] = executeErr == nil
	if executeErr != nil {
		report["execution_error"] = executeErr.Error()
	}
	if scripted != nil && len(capture.responses) != 0 {
		verify = append(verify, "unused scripted responses")
	}
	report["routing_errors"] = verify
	report["routing_pass"] = executeErr == nil && len(verify) == 0
	report["requests"] = len(capture.requests)
	report["budget"] = budget.Snapshot()
	if global != nil {
		report["shared_budget"] = global.Snapshot()
	}
	final, err := fb.LoadContext(context.Background())
	if err != nil {
		return report, err
	}
	for name, value := range map[string]any{"calls.json": calls, "checkpoint.final.json": final, "requests.json": capture.requests, "report.json": report} {
		if err := WriteJSON(filepath.Join(dir, name), value); err != nil {
			return report, err
		}
	}
	return report, nil
}

func TestTaskGoalRoutingLive(t *testing.T) {
	if os.Getenv("POT_ROUTING_LIVE") != "1" {
		t.Skip("requires POT_ROUTING_LIVE=1")
	}
	output := filepath.Clean(os.Getenv("POT_ROUTING_OUTPUT"))
	if !filepath.IsAbs(output) || output == string(filepath.Separator) {
		t.Fatal("POT_ROUTING_OUTPUT must name a fresh absolute directory")
	}
	if _, err := os.Stat(filepath.Dir(output)); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(output, 0700); err != nil {
		t.Fatal(err)
	}
	prompts := make([]string, 2)
	for i, env := range []string{"POT_ROUTING_BASELINE", "POT_ROUTING_CANDIDATE"} {
		raw, e := os.ReadFile(os.Getenv(env))
		if e != nil {
			t.Fatal(e)
		}
		if len(raw) == 0 {
			t.Fatal("empty prompt")
		}
		prompts[i] = string(raw)
		if e = os.WriteFile(filepath.Join(output, []string{"baseline.txt", "candidate.txt"}[i]), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	var model routingModel
	if os.Getenv("POT_ROUTING_PROVIDER") == "configured" {
		model = routingConfiguredModel(t)
	} else {
		path := os.Getenv("POT_ROUTING_MODELS")
		if path == "" {
			home, e := os.UserHomeDir()
			if e != nil {
				t.Fatal(e)
			}
			path = filepath.Join(home, ".pi", "agent", "models.json")
		}
		connection, e := ReadDeepSeek(path)
		if e != nil {
			t.Fatal(e)
		}
		model = routingDeepSeekModel(connection)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	global, err := NewLiveBudget(LiveLimits{80, 8000000}, 20*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	summary := []map[string]any{}
	failed := false
	stop := false
	pairedHashes := map[string]string{}
	for _, rc := range routingCases() {
		if stop {
			break
		}
		for i, arm := range []string{"baseline", "candidate"} {
			rep, e := routingRun(ctx, prompts[i], rc, filepath.Join(output, rc.ID+"-"+arm), model, global, nil)
			rep["arm"] = arm
			if hash, ok := rep["initial_checkpoint_sha256"].(string); ok {
				if arm == "baseline" {
					pairedHashes[rc.ID] = hash
				} else {
					match := pairedHashes[rc.ID] == hash
					rep["paired_checkpoint_match"] = match
					if !match {
						e = errors.New("paired checkpoints differ")
						stop = true
					}
				}
			}
			if e != nil {
				rep["setup_error"] = e.Error()
				stop = true
			}
			if e != nil || rep["routing_pass"] != true {
				failed = true
			}
			if problem, ok := rep["execution_error"].(string); ok {
				lower := strings.ToLower(problem)
				for _, fatal := range []string{"routing global budget", "http status 401", "http status 403", "http 401", "http 403", "forbids retrying", "unauthorized", "authentication", "credential"} {
					if strings.Contains(lower, fatal) {
						stop = true
					}
				}
			}
			if ctx.Err() != nil {
				stop = true
			}
			summary = append(summary, rep)
			if e = WriteJSON(filepath.Join(output, "summary.json"), map[string]any{"samples": summary, "budget": global.Snapshot(), "semantic_review_required": true, "statistical_claim": false}); e != nil {
				t.Fatal(e)
			}
			t.Logf("%s %s action_shape=%v inspection=%v execution=%v requests=%v", rc.ID, arm, rep["action_shape_pass"], rep["inspection_pass"], rep["execution_pass"], rep["requests"])
			if stop {
				break
			}
		}
	}
	if failed {
		t.Error("one or more routing samples failed; inspect summary and raw calls")
	}
}

func TestTaskGoalRoutingDeterministic(t *testing.T) {
	cases := routingCases()
	if len(cases) != 12 {
		t.Fatal(len(cases))
	}
	rc := cases[2]
	scenario := SmallScenario("routing", []string{rc.Input}, false)
	model := routingModel{Name: "fixture", Profile: scenario.Profile, Protocol: scenario.Protocol, OutputCap: 768}
	script := []ScriptedResponse{toolResponse("", fixtureCall{ID: "send-1", Name: "send_message", Arguments: map[string]any{"to": rc.Tasks[0].Info.Address, "text": "仅在原本位于底部时跟随，翻阅历史保留锚点。"}}), textResponse("")}
	dir := filepath.Join(t.TempDir(), "sample")
	report, err := routingRun(context.Background(), "SENTINEL-PRODUCTION-MAIN", rc, dir, model, nil, script)
	if err != nil || report["routing_pass"] != true {
		t.Fatal(report, err)
	}
	var requests []core.ModelRequest
	if err := readJSON(filepath.Join(dir, "requests.json"), &requests); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 {
		t.Fatal(len(requests))
	}
	var body struct {
		Messages []struct{ Role, Content string }
		Tools    []struct {
			Function struct {
				Name       string
				Parameters json.RawMessage
			}
		}
	}
	if err := json.Unmarshal(requests[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) == 0 || body.Messages[0].Role != "system" || !strings.Contains(body.Messages[0].Content, "SENTINEL-PRODUCTION-MAIN") {
		t.Fatal("production Main prompt missing")
	}
	defs, err := routingSchema()
	if err != nil {
		t.Fatal(err)
	}
	if len(body.Tools) != 4 || len(defs) != 4 {
		t.Fatal("unexpected tool scope")
	}
	for _, tool := range body.Tools {
		found := false
		for _, def := range defs {
			if tool.Function.Name == def.Name {
				left, _ := core.CanonicalJSON(tool.Function.Parameters)
				right, _ := core.CanonicalJSON(def.InputSchema)
				if string(left) != string(right) {
					t.Fatal("schema differs")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("nonproduction/execution tool", tool.Function.Name)
		}
	}
	for _, c := range cases {
		cp, e := routingCheckpoint(c.Tasks)
		if e != nil {
			t.Fatal(e)
		}
		raw, _ := json.Marshal(cp)
		for _, word := range []string{"WantCreate", "WantSend", "RequireGet"} {
			if strings.Contains(string(raw), word) {
				t.Fatal("oracle leaked")
			}
		}
	}
}

func TestTaskGoalRoutingAssertions(t *testing.T) {
	task := routingCases()[0].Tasks[0]
	makeCall := func(name, args string) routingCall { return routingCall{Name: name, Args: json.RawMessage(args)} }
	create := makeCall("create_task", `{"text":"标题\n执行功能"}`)
	get := makeCall("get_task", `{"task_id":"task-a"}`)
	send := makeCall("send_message", `{"to":{"node_id":"eval-node","kind":"task","agent_id":"task-a"},"text":"修复"}`)
	for _, test := range []struct {
		name  string
		calls []routingCall
		c     routingCase
		want  bool
	}{
		{"create-count", []routingCall{create, create}, routingCase{WantCreate: 1}, false},
		{"wrong-action", []routingCall{send}, routingCase{WantCreate: 1}, false},
		{"wrong-id", []routingCall{send}, routingCase{WantSend: []string{"task-b"}}, false},
		{"late-get", []routingCall{create, get}, routingCase{WantCreate: 1, RequireGet: "task-a"}, false},
		{"get-first", []routingCall{get, create}, routingCase{WantCreate: 1, RequireGet: "task-a"}, true},
		{"list-allowed", []routingCall{makeCall("list_tasks", `{}`), send}, routingCase{WantSend: []string{"task-a"}}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := len(routingVerify(test.calls, test.c)) == 0; got != test.want {
				t.Fatal(got)
			}
		})
	}
	var calls []routingCall
	dispatch := routingDispatch([]routingTask{task}, &calls)
	for _, args := range []string{`{"to":{"node_id":"wrong-node","kind":"task","agent_id":"task-a"},"text":"fix"}`, `{"to":{"node_id":"eval-node","kind":"assistant","agent_id":"task-a"},"text":"fix"}`} {
		if _, err := dispatch(context.Background(), core.ExecutionContext{}, modelprotocol.ToolCall{Name: "send_message", Arguments: json.RawMessage(args)}, nil); err == nil {
			t.Fatal("wrong full address accepted")
		}
	}
	if len(calls) != 2 || calls[0].Error == "" || calls[1].Error == "" {
		t.Fatal("failed calls not recorded")
	}
}

type routingFailureServices struct{ calls int }

func (s *routingFailureServices) Model(context.Context, core.ModelRequest) (io.ReadCloser, error) {
	s.calls++
	return nil, &core.ModelRequestError{Disposition: "rejected", Cause: &core.TransientModelError{Cause: errors.New("fixture HTTP 503")}}
}
func (*routingFailureServices) Compaction(context.Context, json.RawMessage) error {
	return errors.New("unexpected compaction")
}
func TestTaskGoalRoutingNoRetryAndCaps(t *testing.T) {
	rc := routingCases()[0]
	scenario := SmallScenario("failure", []string{rc.Input}, false)
	service := &routingFailureServices{}
	model := routingModel{Name: "failure", Profile: scenario.Profile, Protocol: scenario.Protocol, OutputCap: 768, NewServices: func(string) (agent.Services, func(), error) { return service, func() {}, nil }}
	report, err := routingRun(context.Background(), "main", rc, filepath.Join(t.TempDir(), "failed"), model, nil, nil)
	if err != nil || service.calls != 1 || report["execution_pass"] != false {
		t.Fatal(report, err, service.calls)
	}
	budget, err := NewLiveBudget(LiveLimits{4, 400000}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	capper := &routingCappedServices{inner: service, local: budget, output: 768}
	request := core.ModelRequest{Body: json.RawMessage(`{"max_tokens":769}`)}
	before := service.calls
	if _, err = capper.Model(context.Background(), request); err == nil || service.calls != before {
		t.Fatal("cap did not reject before network")
	}
	request.Body = json.RawMessage(`{"max_tokens":768}`)
	_, _ = capper.Model(context.Background(), request)
	if _, err = capper.Model(context.Background(), request); err == nil || service.calls != before+1 {
		t.Fatal("same body retried")
	}
}

func TestTaskGoalRoutingFixtureTruth(t *testing.T) {
	var tasks []routingTask
	for i, phase := range []protocol.TaskPhase{protocol.TaskQueued, protocol.TaskRunning, protocol.TaskWaiting, protocol.TaskWaitingApproval, protocol.TaskComplete, protocol.TaskFailed, protocol.TaskInterrupted, protocol.TaskCancelled, protocol.TaskComplete} {
		tasks = append(tasks, routingTask{Info: protocol.TaskInfo{ID: fmt.Sprintf("task-%d", i), Title: "Group", Phase: phase, Archived: i == 8}, Brief: "title\nbody"})
	}
	want := protocol.TaskCounts{Active: 4, Attention: 4, Archived: 1, Complete: 1, Failed: 1, Interrupted: 1, WaitingApproval: 1}
	if got := routingCounts(tasks, "GROUP"); got != want {
		t.Fatalf("counts %+v want %+v", got, want)
	}
	if got := routingCounts(tasks, "missing"); got != (protocol.TaskCounts{}) {
		t.Fatal(got)
	}
	tasks[0].Brief = "Only searchable via Brief"
	if got := routingCounts(tasks, "searchable"); got.Active != 1 || got.Attention != 0 {
		t.Fatal(got)
	}
	var calls []routingCall
	dispatch := routingDispatch(tasks, &calls)
	raw, err := dispatch(context.Background(), core.ExecutionContext{}, modelprotocol.ToolCall{Name: "list_tasks", Arguments: json.RawMessage(`{"scope":"archived","limit":1}`)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var page protocol.TaskPage
	if err = json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if page.Counts != want || page.Total != 1 || len(page.Items) != 1 || !page.Items[0].Archived {
		t.Fatalf("scope must not filter counts: %+v", page)
	}
	for _, rc := range routingCases() {
		cp, err := routingCheckpoint(rc.Tasks)
		if err != nil {
			t.Fatal(err)
		}
		if cp.Messages[0].Role != "user" || cp.Messages[0].Text != "项目是 Pot，源码目录 /workspace/pot；沿用仓库当前技术栈和配置。" {
			t.Fatal("missing pure user project facts")
		}
		var initial protocol.TaskPage
		if err = json.Unmarshal([]byte(cp.Messages[len(cp.Messages)-1].Text), &initial); err != nil {
			t.Fatal(err)
		}
		if initial.Counts != routingCounts(rc.Tasks, "task-") {
			t.Fatal("initial catalogue counts differ")
		}
		first, err := routingFixtureHash(rc, cp)
		if err != nil {
			t.Fatal(err)
		}
		cp2, _ := routingCheckpoint(rc.Tasks)
		second, _ := routingFixtureHash(rc, cp2)
		if len(first) != 64 || first != second {
			t.Fatal("paired fixture hash differs")
		}
		rc.Input += "changed"
		changed, _ := routingFixtureHash(rc, cp)
		if first == changed {
			t.Fatal("fixture hash omitted current input")
		}
	}
}

func TestTaskGoalRoutingCreateContract(t *testing.T) {
	for _, test := range []struct {
		name, args string
		wantErr    bool
	}{
		{"valid", `{"text":"Title\nBody"}`, false},
		{"unknown-overall-goal", `{"text":"Title\nBody","overall_goal":"x"}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var calls []routingCall
			dispatch := routingDispatch(nil, &calls)
			_, err := dispatch(context.Background(), core.ExecutionContext{}, modelprotocol.ToolCall{Name: "create_task", ID: "fixture-create", Arguments: json.RawMessage(test.args)}, nil)
			if (err != nil) != test.wantErr {
				t.Fatal(err)
			}
			if len(calls) != 1 || (calls[0].Error != "") != test.wantErr {
				t.Fatal("receipt does not match validation", calls)
			}
			if test.wantErr && calls[0].Result != nil {
				t.Fatal("invalid create produced a receipt")
			}
		})
	}
}
