package control

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/composition"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/executor"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

func TestToolUsageMatchesAgentDirectAndComposedResults(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) > 1 {
			batchReply(w, "done")
			return
		}
		calls := []map[string]any{}
		for i, call := range []modelprotocol.ToolCall{
			{ID: "nonzero", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"exit 7","login":false}`)},
			{ID: "zero", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"exit 0","login":false}`)},
			{ID: "invalid", Name: "exec_command", Arguments: json.RawMessage(`{}`)},
			{ID: "composed", Name: "exec", Arguments: json.RawMessage(`{"code":"const results = await Promise.all([tools.exec_command({cmd:'exit 3',login:false}), tools.exec_command({cmd:'exit 0',login:false})]); text(results);"}`)},
		} {
			calls = append(calls, map[string]any{"index": i, "id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
		}
		data, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": calls}, "finish_reason": "tool_calls"}}})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: " + string(data) + "\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	s := taskControl(t, "usage-fixture", server.URL)
	s.options.Composer = composition.Runner{}
	c, err := s.Conversation(t.Context(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Send(t.Context(), protocol.SendMessage{RequestID: "usage-turn", ConversationID: "main", Text: "run fixture tools"}); err != nil {
		t.Fatal(err)
	}
	snapshot := awaitConversation(t, c, func(s protocol.Snapshot) bool { return s.Turn == nil && len(s.Messages) > 1 })
	if snapshot.Agent.Phase == protocol.AgentFailed {
		t.Fatal(snapshot.Agent)
	}
	db, err := storage.OpenDatabaseReadOnly(filepath.Join(s.options.Root, "usage.sqlite"), storage.UsageDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	count, children := 0, 0
	for _, message := range snapshot.Messages {
		tool := message.Tool
		if tool == nil || tool.Name == "exec" {
			continue
		}
		count++
		if tool.ParentID != "" {
			children++
			if tool.ParentID != "composed" {
				t.Fatal("composition child lost its parent", tool)
			}
		}
		key, _ := json.Marshal([]string{"main", "usage-turn", tool.ID})
		var name string
		var failed bool
		if err := db.QueryRowContext(t.Context(), "SELECT name,failed FROM tools WHERE id=? AND scope=? AND turn_id=? AND end_at IS NOT NULL", string(key), "main", "usage-turn").Scan(&name, &failed); err != nil {
			t.Fatal(tool.ID, err)
		}
		if name != tool.Name || failed != (message.Status == protocol.Failed) {
			t.Errorf("%s: stored name=%s failed=%v, Agent name=%s status=%s result=%s", tool.ID, name, failed, tool.Name, message.Status, tool.Result)
		}
	}
	if count != 5 || children != 2 {
		t.Fatal("missing direct or composed tool results", count, children)
	}
}

func meteredDispatcher(t *testing.T) (*Dispatcher, core.ExecutionContext) {
	t.Helper()
	d, execution, _ := localDispatcher(t)
	ledger, err := usage.Open(filepath.Join(t.TempDir(), "usage.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ledger.Close() })
	d.Usage = ledger
	return d, execution
}

func assertToolUsage(t *testing.T, d *Dispatcher, scope, name string, calls, failed int64) {
	t.Helper()
	page, err := d.Usage.Page(t.Context(), "tools", usage.Query{Scope: scope}, storage.Now())
	if err != nil {
		t.Fatal(err)
	}
	items := page.Items.([]usage.ToolTotal)
	if len(items) != 1 || items[0].Name != name || items[0].Calls != calls || items[0].Failed != failed || items[0].Unfinished != 0 {
		t.Fatalf("scope %s: unexpected stored usage %+v", scope, items)
	}
}

func TestToolUsageRunningAndDelayedExitReceipts(t *testing.T) {
	t.Parallel()
	d, execution := meteredDispatcher(t)
	d.UsageScope = "task-fixture"
	execution.Role = core.Worker
	dispatch := func(id, name string, args json.RawMessage, wantFailed bool) map[string]any {
		t.Helper()
		raw, err := d.Dispatch(t.Context(), execution, modelprotocol.ToolCall{ID: id, Name: name, Arguments: args}, nil)
		if err != nil || agent.ToolResultFailed(raw) != wantFailed {
			t.Fatal(string(raw), err)
		}
		var value map[string]any
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	started := dispatch("start", "exec_command", json.RawMessage(`{"cmd":"read line; exit 9","login":false,"interactive":true,"yield_time_ms":1}`), false)
	if started["session_id"] == nil || started["exit_code"] != nil {
		t.Fatal("expected a running command", started)
	}
	args, _ := json.Marshal(map[string]any{"session_id": started["session_id"], "yield_time_ms": 1})
	running := dispatch("poll", "write_stdin", args, false)
	if running["session_id"] == nil || running["exit_code"] != nil {
		t.Fatal("poll should still be running", running)
	}
	args, _ = json.Marshal(map[string]any{"session_id": started["session_id"], "chars": "\n", "yield_time_ms": 1000})
	finished := dispatch("finish", "write_stdin", args, true)
	if finished["exit_code"] != float64(9) {
		t.Fatal("missing delayed exit", finished)
	}
	// A replay of the final receipt must neither write to a closed process nor add a call.
	dispatch("finish", "write_stdin", args, true)
	page, err := d.Usage.Page(t.Context(), "tools", usage.Query{Scope: d.UsageScope}, storage.Now())
	if err != nil {
		t.Fatal(err)
	}
	items := page.Items.([]usage.ToolTotal)
	if len(items) != 2 || items[0].Name != "write_stdin" || items[0].Calls != 2 || items[0].Failed != 1 || items[1].Name != "exec_command" || items[1].Calls != 1 || items[1].Failed != 0 {
		t.Fatal("polls are separate receipts for one process", items)
	}
}

type cancelledUsageEngine struct{ cancel context.CancelFunc }

func (e cancelledUsageEngine) Execute(ctx context.Context, _ executor.Request, _ func(executor.Event) error) (map[string]any, error) {
	e.cancel()
	return nil, ctx.Err()
}

func TestToolUsageRetainsDispatchErrorSemantics(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"validation", "unregistered", "denied", "cancelled", "isError", "reason"} {
		t.Run(kind, func(t *testing.T) {
			d, execution := meteredDispatcher(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			call := modelprotocol.ToolCall{ID: "call", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"exit 0","login":false}`)}
			var emit func(core.Update) error
			switch kind {
			case "validation":
				call.Arguments = json.RawMessage(`{}`)
			case "unregistered":
				call.Name = "missing_tool"
			case "denied":
				if err := d.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "ask"}); err != nil {
					t.Fatal(err)
				}
				call.Arguments = json.RawMessage(`{"cmd":"exit 0","login":false,"request_unsandboxed":true,"justification":"isolated denial fixture"}`)
				emit = func(update core.Update) error {
					if update.Phase == "waiting_approval" {
						pending := d.Permissions.Pending(d.Owner)
						return d.Permissions.Decide(d.Owner, pending[0].ID, "deny")
					}
					return nil
				}
			case "cancelled":
				d.Host = cancelledUsageEngine{cancel: cancel}
			case "isError", "reason":
				call.Name = "product_fixture"
				d.Product = map[string]agent.Dispatch{call.Name: func(context.Context, core.ExecutionContext, modelprotocol.ToolCall, func(core.Update) error) (json.RawMessage, error) {
					if kind == "isError" {
						return json.RawMessage(`{"isError":true,"execution_status":"not_started"}`), nil
					}
					return json.RawMessage(`{"reason":"refused"}`), nil
				}}
			}
			raw, err := d.Dispatch(ctx, execution, call, emit)
			if !agent.ToolResultFailed(raw) || (err == nil) != (kind == "isError" || kind == "reason") {
				t.Fatal(string(raw), err)
			}
			if kind == "denied" && !errors.Is(err, ErrHostDenied) || kind == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatal("error identity changed", err)
			}
			assertToolUsage(t, d, d.Owner, call.Name, 1, 1)
		})
	}
}

func TestToolUsageReplayKeepsScopeTurnAndCallIdentity(t *testing.T) {
	t.Parallel()
	d, execution := meteredDispatcher(t)
	call := modelprotocol.ToolCall{ID: "same-call", Name: "exec_command", Arguments: json.RawMessage(`{"cmd":"exit 2","login":false}`)}
	for _, scope := range []string{"task-a", "task-b"} {
		d.Owner, execution.Owner, d.UsageScope = scope, scope, scope
		for _, turn := range []string{"turn-a", "turn-b"} {
			d.Turn = turn
			for range 2 {
				raw, err := d.Dispatch(t.Context(), execution, call, nil)
				if err != nil || !agent.ToolResultFailed(raw) {
					t.Fatal(string(raw), err)
				}
			}
		}
		assertToolUsage(t, d, scope, call.Name, 2, 2)
	}
}
