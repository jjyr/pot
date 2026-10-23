package usage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestSelectedDatesReconcileEverySummary(t *testing.T) {
	for _, name := range []string{"UTC", "Asia/Shanghai", "America/New_York", "Asia/Kathmandu"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			l, _ := ledger(t)
			zone, _ := time.LoadLocation(name)
			begin := time.Date(2024, 3, 9, 0, 0, 0, 0, zone)
			end := time.Date(2024, 3, 12, 0, 0, 0, 0, zone)
			// This historical interval crosses the New York spring DST change.
			points := []time.Time{begin.Add(-time.Millisecond), begin, begin.AddDate(0, 0, 1), end.Add(-time.Millisecond), end}
			for i, at := range points {
				id := fmt.Sprintf("date-%d", i)
				r := reservation(id, at.UnixMilli())
				r.Model, r.Turn = id, id
				if _, err := l.Reserve(ctx, r); err != nil {
					t.Fatal(err)
				}
				if err := l.Settle(ctx, id, Frame{Input: number(100), Output: number(10), Cache: number(75)}); err != nil {
					t.Fatal(err)
				}
				if err := l.MarkRoutingTurn(ctx, id); err != nil {
					t.Fatal(err)
				}
				if err := l.Compaction(ctx, "main", id, CompactionEvent{ID: id, Action: "start", Reason: "idle"}, at.UnixMilli()); err != nil {
					t.Fatal(err)
				}
				if err := l.TagCompaction(ctx, id, id); err != nil {
					t.Fatal(err)
				}
			}
			at := time.Date(2026, 10, 23, 12, 0, 0, 0, zone).UnixMilli()
			s, err := l.Summary(ctx, Query{Start: "2024-03-09", End: "2024-03-11", Scope: "main", Timezone: name}, at)
			if err != nil {
				t.Fatal(err)
			}
			want := Totals{Input: 300, Output: 30, Cache: 225, Requests: 3}
			if s.Total != want || s.RoutingTotal != want || len(s.Models) != 3 || len(s.CompactionTotals) != 1 || s.CompactionTotals[0].Totals != want || s.Agents[0].Totals != want {
				t.Fatalf("date range mixed with lifetime or boundary data: %+v", s)
			}
			if s.Lifetime.Requests != 5 || len(s.Buckets) != 3 || s.Start != "2024-03-09" || s.End != "2024-03-11" {
				t.Fatal(s)
			}
			for _, bucket := range s.Buckets {
				if bucket.Requests != 1 {
					t.Fatal("local day boundary mismatch", bucket)
				}
			}
			s, err = l.Summary(ctx, Query{Start: "2024-01-01", End: "2024-12-31", Timezone: name}, at)
			if err != nil || len(s.Buckets) != 366 || s.Buckets[59].Date != "2024-02-29" {
				t.Fatal("natural leap year lost a date", s.Days, err)
			}
		})
	}
}

func TestSelectedDatesValidationAndCurrentCutoff(t *testing.T) {
	l, _ := ledger(t)
	at := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC).UnixMilli()
	for _, q := range []Query{{Start: "2026-10-01"}, {End: "2026-10-01"}, {Start: "2026-02-30", End: "2026-03-01"}, {Start: "2026-10-23", End: "2026-10-01"}, {Start: "2026-01-01", End: "2026-12-31", Days: 365}, {Start: "2000-01-01", End: "2026-01-01"}} {
		if _, err := l.Summary(context.Background(), q, at); err == nil {
			t.Fatal("invalid dates accepted", q)
		}
	}
	for i, when := range []int64{at, at + 1} {
		r := reservation(fmt.Sprintf("now-%d", i), when)
		if _, err := l.Reserve(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	s, err := l.Summary(context.Background(), Query{Start: "2026-10-01", End: "2026-10-31", Timezone: "UTC"}, at)
	if err != nil || s.Total.Requests != 1 || len(s.Buckets) != 31 || s.Today != "2026-10-23" || s.Buckets[23].Requests != 0 {
		t.Fatal("future data must stay excluded", s, err)
	}
	first := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC)
	s, err = l.Summary(context.Background(), Query{Start: first.Format(time.DateOnly), End: first.AddDate(0, 0, 3659).Format(time.DateOnly), Timezone: "UTC"}, at)
	if err != nil || len(s.Buckets) != 3660 {
		t.Fatal("maximum custom range must fit SQL aggregation limits", s.Days, err)
	}
}
