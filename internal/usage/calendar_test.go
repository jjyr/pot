package usage

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAnnualCalendarAttributionAndLocalDates(t *testing.T) {
	for _, name := range []string{"UTC", "Asia/Shanghai", "America/New_York", "Asia/Kathmandu"} {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			l, _ := ledger(t)
			zone, err := time.LoadLocation(name)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 11, 2, 12, 0, 0, 0, zone)
			first := time.Date(2025, 11, 3, 0, 0, 0, 0, zone)
			fixtures := []struct {
				at    time.Time
				scope string
				agent string
				model string
			}{
				{first.Add(-time.Millisecond), "main", "task", "outside-year"},
				{first, "main", "task", "shared"},
				{at, "main", "pot", "shared"},
				{at.Add(time.Millisecond), "main", "task", "future"},
				{at, "other", "task", "other-scope"},
				{at, "main", "", "unknown-route"},
				// Both sides of the repeated New York 01:30 on the DST boundary.
				{time.Date(2026, 11, 1, 5, 30, 0, 0, time.UTC), "main", "summary", "shared"},
				{time.Date(2026, 11, 1, 6, 30, 0, 0, time.UTC), "main", "summary", "shared"},
			}
			for i := range 25 {
				fixtures = append(fixtures, struct {
					at    time.Time
					scope string
					agent string
					model string
				}{at.AddDate(0, 0, -i), "main", "task", fmt.Sprintf("model-%02d", i)})
			}
			expected := map[string]int64{}
			for i, f := range fixtures {
				r := reservation(fmt.Sprintf("calendar-%d", i), f.at.UnixMilli())
				r.Scope, r.Model = f.scope, f.model
				if f.agent == "" {
					r.Route = nil
				} else {
					r.Route.Agent = f.agent
				}
				if _, err := l.Reserve(ctx, r); err != nil {
					t.Fatal(err)
				}
				if err := l.Settle(ctx, r.ID, Frame{Input: number(100), Output: number(10), Cache: number(80)}); err != nil {
					t.Fatal(err)
				}
				if f.scope == "main" && !f.at.Before(first) && !f.at.After(at) {
					expected[f.at.In(zone).Format("2006-01-02")]++
				}
			}
			s, err := l.Summary(ctx, Query{Scope: "main", Timezone: name}, at.UnixMilli())
			if err != nil {
				t.Fatal(err)
			}
			if s.Days != 365 || len(s.Buckets) != 365 || len(s.Models) != 27 || len(s.Agents) != 4 {
				t.Fatalf("missing complete daily/model/category aggregates: days=%d buckets=%d models=%d agents=%d", s.Days, len(s.Buckets), len(s.Models), len(s.Agents))
			}
			var total Totals
			for _, bucket := range s.Buckets {
				count := expected[bucket.Date]
				if bucket.Requests != count || bucket.Input != count*100 || bucket.Output != count*10 || bucket.Cache != count*80 || bucket.Agents == nil {
					t.Fatalf("incorrect local date or cache accounting: %+v, expected %d requests", bucket, count)
				}
				var agents Totals
				for _, agent := range bucket.Agents {
					addTotals(&agents, agent.Totals)
				}
				if agents != bucket.Totals {
					t.Fatal("daily categories do not sum to daily total", bucket)
				}
				addTotals(&total, bucket.Totals)
			}
			if total != s.Total || s.Total.Requests != 30 {
				t.Fatal("year total includes another scope, future data, or cache twice", s.Total)
			}
			var agents, models Totals
			for _, agent := range s.Agents {
				addTotals(&agents, agent.Totals)
			}
			for _, model := range s.Models {
				addTotals(&models, model.Totals)
				if model.Model == "shared" && model.Requests != 4 {
					t.Fatal("same model must combine different categories", model)
				}
			}
			if agents != s.Total || models != s.Total {
				t.Fatal("annual distributions must sum to annual total", agents, models, s.Total)
			}
		})
	}
}

func TestAnnualCalendarEmptyAndLeapDay(t *testing.T) {
	l, _ := ledger(t)
	s, err := l.Summary(context.Background(), Query{Timezone: "UTC"}, time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Buckets) != 365 || s.Buckets[363].Date != "2024-02-29" || s.Agents == nil || s.Models == nil || len(s.Agents) != 0 || len(s.Models) != 0 {
		t.Fatal("empty calendar must preserve leap day and empty aggregates", s)
	}
}
