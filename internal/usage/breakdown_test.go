package usage

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBreakdownStagesToolsAndUnknownUsage(t *testing.T) {
	l, _ := ledger(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC).UnixMilli()
	for _, r := range []struct {
		id, scope, turn, role string
		input, output         *int64
	}{
		{"main", "owner", "user", "pot", number(100), number(10)},
		{"mail", "owner", "pot-result-batch", "summary", number(30), number(4)},
		{"task", "task1", "run", "task", number(20), nil},
		{"rejected", "owner", "user", "main", nil, nil},
	} {
		req := reservation(r.id, at)
		req.Scope = r.scope
		req.Turn = r.turn
		req.Route.Agent = r.role
		if _, err := l.Reserve(ctx, req); err != nil {
			t.Fatal(err)
		}
		if _, err := l.db.Exec(`UPDATE requests SET input=?,output=?,state=? WHERE id=?`, r.input, r.output, map[bool]string{true: "rejected", false: "complete"}[r.id == "rejected"], r.id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"a", "b"} {
		event := ToolEvent{CallID: id, Name: "send_message", Agent: "main", Action: "start", EstimatedTokens: number(6), EstimateMethod: "codex-content-v1", Arguments: json.RawMessage(`{ "x": 1 }`)}
		if err := l.Tool(ctx, "owner", "user", event, at); err != nil {
			t.Fatal(err)
		}
		event.Action = "end"
		event.EstimatedTokens = number(9)
		event.Content = json.RawMessage(`"ok"`)
		if err := l.Tool(ctx, "owner", "user", event, at+5); err != nil {
			t.Fatal(err)
		}
		event.Content = json.RawMessage(`"changed replay"`)
		if err := l.Tool(ctx, "owner", "user", event, at+50); err != nil {
			t.Fatal(err)
		}
	}
	got, err := l.Breakdown(ctx, Query{Days: 14, Timezone: "UTC"}, at+100)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ToolGroups) != 1 || got.ToolGroups[0].Name != "send_message" || got.ToolGroups[0].CallEstimatedTokens != 12 {
		t.Fatal(got.ToolGroups)
	}
	if len(got.Models) != 3 || got.Tools.Calls != 2 || got.Tools.DurationMS != 10 || got.Tools.CallEstimatedTokens != 12 || got.Tools.ResultEstimatedTokens != 18 {
		t.Fatalf("%+v", got)
	}
	for _, v := range got.Models {
		if v.Agent == "task" && v.OutputKnown {
			t.Fatal("unknown output became known")
		}
	}

}

func TestBreakdownCompactionExclusiveAndWindowed(t *testing.T) {
	l, _ := ledger(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC).UnixMilli()
	for _, id := range []string{"compact", "future", "rejected", "unknown"} {
		r := reservation(id, at)
		r.Turn = "pot-result-batch"
		r.Route.Agent = "summary"
		if id == "future" {
			r.AtMS = at + 1000
		}
		if id == "unknown" {
			r.Route = nil
		}
		if _, err := l.Reserve(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.db.Exec(`UPDATE requests SET compaction_id='operation',input=11,output=2 WHERE id='compact'`); err != nil {
		t.Fatal(err)
	}
	if err := l.Rejected(ctx, "rejected"); err != nil {
		t.Fatal(err)
	}
	got, err := l.Breakdown(ctx, Query{Start: "2026-10-30", End: "2026-10-30", Timezone: "UTC"}, at)
	if err != nil || len(got.Models) != 2 {
		t.Fatal(got, err)
	}
	var count int64
	for _, group := range got.Models {
		count += group.Requests
		if group.Agent == "main" && (group.Stage != "compaction" || group.Input != 11 || !group.InputKnown || !group.OutputKnown || group.CacheKnown) {
			t.Fatal(group)
		}
		if group.Agent == "unknown" && (group.InputKnown || group.OutputKnown) {
			t.Fatal(group)
		}
	}
	if count != 2 {
		t.Fatal(got)
	}
}
