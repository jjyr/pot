package usage

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestUsagePagesOrderingBoundariesAndScope(t *testing.T) {
	ctx := context.Background()
	l, _ := ledger(t)
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC).UnixMilli()
	// Shuffle insertion order, with equal request timestamps across page boundaries
	// and equal tool call counts.
	for index := 0; index < 43; index++ {
		i := index * 17 % 43
		id := fmt.Sprintf("%02d", i)
		r := reservation("request-"+id, at+int64(i/3))
		r.Model = "model-" + id
		if _, err := l.Reserve(ctx, r); err != nil {
			t.Fatal(err)
		}
		if i != 0 {
			if err := l.Settle(ctx, r.ID, Frame{Input: number(int64(i + 1)), Output: number(2), Cache: number(1)}); err != nil {
				t.Fatal(err)
			}
		}
		if err := l.Compaction(ctx, "main", "turn", CompactionEvent{ID: "compact-" + id, Action: "start", Reason: "threshold"}, at+int64(i/2)); err != nil {
			t.Fatal(err)
		}
		for j := 0; j < 1+i/2; j++ {
			call := fmt.Sprintf("call-%s-%d", id, j)
			if err := l.Tool(ctx, "main", "turn", ToolEvent{Action: "start", CallID: call, Name: "tool-" + id}, at); err != nil {
				t.Fatal(err)
			}
			if err := l.Tool(ctx, "main", "turn", ToolEvent{Action: "end", CallID: call, Content: []byte(`"` + strings.Repeat("x", 50-i) + `"`)}, at+10); err != nil {
				t.Fatal(err)
			}
		}
	}
	other := reservation("other-request", at+100)
	other.Scope = "task"
	if _, err := l.Reserve(ctx, other); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"requests", "compactions", "tools", "models"} {
		t.Run(kind, func(t *testing.T) {
			var ids []string
			var previousCalls int64 = 1<<63 - 1
			var previousName string
			for _, offset := range []int{0, 20, 40, 60} {
				p, err := l.Page(ctx, kind, Query{Scope: "main", Offset: offset, Timezone: "UTC"}, at+100)
				if err != nil {
					t.Fatal(err)
				}
				if (p.NextOffset != nil) != (offset < 40) || p.NextOffset != nil && *p.NextOffset != offset+20 {
					t.Fatal("invalid next page", p)
				}
				if got, want := reflect.ValueOf(p.Items).Len(), max(0, min(20, 43-offset)); got != want {
					t.Fatalf("offset %d: got %d rows, want %d", offset, got, want)
				}
				switch rows := p.Items.(type) {
				case []RequestRecord:
					for _, row := range rows {
						ids = append(ids, row.ID)
						if row.ID == "request-00" && (row.Input != nil || row.Output != nil || row.Provider != "provider-a") {
							t.Fatal("unknown usage or route attribution lost", row)
						}
					}
				case []CompactionRecord:
					for _, row := range rows {
						ids = append(ids, row.ID)
					}
				case []ToolTotal:
					for _, row := range rows {
						if row.Calls > previousCalls || row.Calls == previousCalls && row.Name < previousName {
							t.Fatal("tools must sort by calls then name", row)
						}
						previousCalls, previousName = row.Calls, row.Name
						ids = append(ids, row.Name)
					}
				case []AgentModel:
					for _, row := range rows {
						ids = append(ids, row.Model)
					}
				}
			}
			seen := map[string]bool{}
			for i, id := range ids {
				if seen[id] {
					t.Fatal("duplicate across page boundary", id)
				}
				seen[id] = true
				if kind == "requests" && id != fmt.Sprintf("request-%02d", 42-i) || kind == "compactions" && id != fmt.Sprintf("compact-%02d", i) {
					t.Fatal("requests must be newest first; compactions must follow occurrence order", ids)
				}
			}
			if len(ids) != 43 {
				t.Fatal("missing records", len(ids))
			}
			p, err := l.Page(ctx, kind, Query{Scope: "other"}, at+100)
			if err != nil || p.NextOffset != nil || reflect.ValueOf(p.Items).Len() != 0 {
				t.Fatal("scope leak", p, err)
			}
		})
	}
	for _, test := range []struct {
		kind string
		q    Query
	}{{"requests", Query{}}, {"tools", Query{}}, {"compactions", Query{}}, {"invalid", Query{}}, {"models", Query{Days: 2}}, {"models", Query{Offset: -1}}, {"models", Query{Timezone: "invalid/zone"}}} {
		if _, err := l.Page(ctx, test.kind, test.q, at); err == nil {
			t.Fatal("invalid query accepted", test)
		}
	}
}

func TestSummaryFortnightRange(t *testing.T) {
	ctx := context.Background()
	l, _ := ledger(t)
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for i, day := range []time.Time{
		time.Date(2026, 10, 4, 23, 59, 59, 0, time.UTC),
		time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC),
		at.AddDate(0, 0, -8),
		at,
	} {
		r := reservation(fmt.Sprintf("request-%d", i), day.UnixMilli())
		if _, err := l.Reserve(ctx, r); err != nil {
			t.Fatal(err)
		}
		if err := l.Settle(ctx, r.ID, Frame{Input: number(100), Output: number(10)}); err != nil {
			t.Fatal(err)
		}
	}
	s, err := l.Summary(ctx, Query{Days: 14, Timezone: "UTC"}, at.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if s.Days != 14 || len(s.Buckets) != 14 || s.Total.Requests != 3 || s.Lifetime.Requests != 4 {
		t.Fatal("expected fourteen daily slots and range-limited totals", s)
	}
	if s.Buckets[0].Date != "2026-10-05" || s.Buckets[0].Input != 100 || s.Buckets[1].Input != 0 || s.Buckets[13].Date != "2026-10-18" || s.Buckets[13].Input != 100 {
		t.Fatal("range must include the first midnight, empty dates and today", s.Buckets)
	}
}

func TestModelPagesGlobalRangeAndSessionLifetime(t *testing.T) {
	ctx := context.Background()
	l, _ := ledger(t)
	at := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	for i, days := range []int{-14, -13, -8, -1} {
		r := reservation(fmt.Sprintf("request-%d", i), at.AddDate(0, 0, days).UnixMilli())
		if _, err := l.Reserve(ctx, r); err != nil {
			t.Fatal(err)
		}
		if err := l.Settle(ctx, r.ID, Frame{Input: number(100), Output: number(10)}); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		scope string
		days  int
		count int64
	}{{"", 0, 4}, {"", 14, 3}, {"", 30, 4}, {"main", 14, 4}} {
		p, err := l.Page(ctx, "models", Query{Scope: test.scope, Days: test.days, Timezone: "UTC"}, at.UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		rows := p.Items.([]AgentModel)
		if len(rows) != 1 || rows[0].Requests != test.count || rows[0].Input != 100*test.count {
			t.Fatal(test, p)
		}
	}
}
