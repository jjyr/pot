package usage

import (
	"context"
	"testing"
	"time"
)

func TestCompactionTotalsUseMeteredAttemptsAndCalendarScope(t *testing.T) {
	ctx := context.Background()
	l, _ := ledger(t)
	at := time.Date(2026, 10, 21, 12, 0, 0, 0, time.FixedZone("test", 8*3600))
	first := time.Date(2025, 10, 22, 0, 0, 0, 0, at.Location()).UnixMilli()
	add := func(id, scope, reason string, atMS int64, input, output, cache int64) {
		t.Helper()
		if err := l.Compaction(ctx, scope, "turn-1", CompactionEvent{ID: id, Action: "start", Reason: reason}, atMS); err != nil {
			t.Fatal(err)
		}
		for i := range 2 {
			request := id + string(rune('a'+i))
			r := reservation(request, atMS)
			r.Scope = scope
			if _, err := l.Reserve(ctx, r); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := l.TagCompaction(ctx, request, id); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := l.Settle(ctx, request, Frame{Input: number(input), Output: number(output), Cache: number(cache)}); err != nil {
					t.Fatal(err)
				}
			}
		}
		if err := l.Compaction(ctx, scope, "turn-1", CompactionEvent{ID: id, Action: "failed"}, atMS+1); err != nil {
			t.Fatal(err)
		}
	}
	add("idle", "main", "idle", at.UnixMilli(), 100, 10, 90)
	add("window", "main", "threshold", first, 200, 20, 100)
	add("manual", "main", "manual", at.UnixMilli(), 300, 30, 90)
	add("overflow", "main", "overflow", at.UnixMilli(), 400, 40, 0)
	add("old", "main", "idle", first-1, 1000, 1, 900)
	add("future", "main", "idle", at.UnixMilli()+1, 1000, 1, 900)
	add("other", "other", "idle", at.UnixMilli(), 50, 5, 10)
	// A regular request and a never-sent rejection must not enter the subset.
	for _, id := range []string{"ordinary", "rejected"} {
		r := reservation(id, at.UnixMilli())
		if _, err := l.Reserve(ctx, r); err != nil {
			t.Fatal(err)
		}
		if id == "ordinary" {
			if err := l.Settle(ctx, id, Frame{Input: number(500), Output: number(10)}); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := l.Compaction(ctx, "main", "turn-1", CompactionEvent{ID: "rejected-op", Action: "start", Reason: "idle"}, at.UnixMilli()); err != nil {
				t.Fatal(err)
			}
			if err := l.TagCompaction(ctx, id, "rejected-op"); err != nil {
				t.Fatal(err)
			}
			if err := l.Rejected(ctx, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, scope := range []string{"main", ""} {
		s, err := l.Summary(ctx, Query{Scope: scope, Timezone: "Asia/Shanghai"}, at.UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		if len(s.CompactionTotals) != 2 {
			t.Fatal(s.CompactionTotals)
		}
		var subset Totals
		for _, row := range s.CompactionTotals {
			if !row.InputKnown || !row.OutputKnown || !row.CacheKnown {
				t.Fatal(row)
			}
			if row.Kind == "precompact" {
				want := Totals{Input: 200, Output: 20, Cache: 180, Requests: 2}
				if scope == "" {
					addTotals(&want, Totals{Input: 100, Output: 10, Cache: 20, Requests: 2})
				}
				if row.Totals != want {
					t.Fatal(row, want)
				}
			} else if row.Kind != "compact" || row.Totals != (Totals{Input: 1800, Output: 180, Cache: 380, Requests: 6}) {
				t.Fatal(row)
			}
			addTotals(&subset, row.Totals)
		}
		if subset.Input+subset.Output >= s.Total.Input+s.Total.Output {
			t.Fatal("compaction is a subset, cache is included only once", subset, s.Total)
		}
	}
	short, err := l.Summary(ctx, Query{Scope: "main", Timezone: "Asia/Shanghai", Days: 14}, at.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if short.CompactionTotals[0].Input != 1400 {
		t.Fatal("annual boundary and explicit shorter periods must share the calendar window", short.CompactionTotals)
	}
}

func TestCompactionTotalsPreserveUnreportedCacheAndPartialUsage(t *testing.T) {
	ctx := context.Background()
	l, _ := ledger(t)
	at := time.Now().UnixMilli()
	if err := l.Compaction(ctx, "main", "turn-1", CompactionEvent{ID: "op", Action: "start", Reason: "idle"}, at); err != nil {
		t.Fatal(err)
	}
	r := reservation("request", at)
	if _, err := l.Reserve(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := l.TagCompaction(ctx, r.ID, "op"); err != nil {
		t.Fatal(err)
	}
	if err := l.Settle(ctx, r.ID, Frame{Input: number(100)}); err != nil {
		t.Fatal(err)
	}
	read := func() CompactionTotal {
		t.Helper()
		s, err := l.Summary(ctx, Query{Timezone: "UTC"}, at)
		if err != nil {
			t.Fatal(err)
		}
		return s.CompactionTotals[0]
	}
	if row := read(); !row.InputKnown || row.OutputKnown || row.CacheKnown || row.Input != 100 {
		t.Fatal(row)
	}
	if err := l.Settle(ctx, r.ID, Frame{Output: number(10), Cache: number(0)}); err != nil {
		t.Fatal(err)
	}
	if err := l.Settle(ctx, r.ID, Frame{Output: number(11)}); err != nil {
		t.Fatal(err)
	}
	if row := read(); !row.InputKnown || !row.OutputKnown || !row.CacheKnown || row.Cache != 0 || row.Requests != 1 || row.Output != 11 {
		t.Fatal(row)
	}
}
