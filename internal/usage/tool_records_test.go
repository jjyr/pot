package usage

import (
	"fmt"
	"testing"
	"time"
)

func TestTurnBreakdownAndToolRecordPages(t *testing.T) {
	l, _ := ledger(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC).UnixMilli()
	for _, turn := range []string{"chosen", "other"} {
		r := reservation(turn, at)
		r.Scope = "owner"
		r.Turn = turn
		r.Route.Agent = "main"
		if _, err := l.Reserve(ctx, r); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 23; i++ {
			if err := l.Tool(ctx, "owner", turn, ToolEvent{CallID: fmt.Sprintf("call-%02d", i), Name: "exec_command", Agent: "main", Action: "start", EstimatedTokens: number(4), EstimateMethod: "codex-content-v1"}, at+int64(i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	q := Query{Scope: "owner", Turn: "chosen", Start: "2026-10-30", End: "2026-10-30", Timezone: "UTC"}
	b, err := l.Breakdown(ctx, q, at+100)
	if err != nil || len(b.Models) != 1 || b.Models[0].Requests != 1 || b.Models[0].InputKnown || b.Tools.Calls != 23 {
		t.Fatal(b, err)
	}
	p, err := l.ToolRecords(ctx, q, at+100)
	if err != nil {
		t.Fatal(err)
	}
	items := p.Items.([]ToolRecord)
	if len(items) != 20 || p.NextOffset == nil || *p.NextOffset != 20 || items[0].ID != "call-22" || items[0].ResultTokens != nil {
		t.Fatal(p)
	}
	q.Offset = *p.NextOffset
	p, err = l.ToolRecords(ctx, q, at+100)
	if err != nil || len(p.Items.([]ToolRecord)) != 3 || p.NextOffset != nil {
		t.Fatal(p, err)
	}
	q.Turn = "missing"
	b, err = l.Breakdown(ctx, q, at+100)
	if err != nil || len(b.Models) != 0 || b.Tools.Calls != 0 {
		t.Fatal(b, err)
	}
}
