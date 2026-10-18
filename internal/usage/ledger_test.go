package usage

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func ledger(t *testing.T) (*Ledger, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "usage.sqlite")
	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l, path
}
func number(n int64) *int64 { return &n }
func reservation(id string, at int64) ReserveRequest {
	return ReserveRequest{ID: id, Scope: "main", Turn: "turn-1", AtMS: at, Model: "model-a", InputEstimate: 100, MaxOutput: 100, Route: &Route{Provider: "provider-a", Generation: 1, SpecHash: "hash", Agent: "pot", ProfileID: "profile-a"}}
}
func TestReservationBothAllowancesAtomicClaimAndRestart(t *testing.T) {
	ctx := context.Background()
	l, path := ledger(t)
	at := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC).UnixMilli()
	if err := l.SetPolicy(ctx, Policy{"day", 500, "UTC", true}); err != nil {
		t.Fatal(err)
	}
	if err := l.SetProviderPolicy(ctx, "provider-a", 0, Policy{"day", 250, "UTC", true}); err != nil {
		t.Fatal(err)
	}
	r := reservation("request-1", at)
	r.MaxOutput = 400
	first, err := l.Reserve(ctx, r)
	if err != nil || first == nil || first.OutputCap != 150 {
		t.Fatal(first, err)
	}
	if claimed, err := l.Claim(ctx, r.ID); err != nil || !claimed {
		t.Fatal(claimed, err)
	}
	if claimed, err := l.Claim(ctx, r.ID); err != nil || claimed {
		t.Fatal("network request reclaimed", claimed, err)
	}
	duplicate, err := l.Reserve(ctx, r)
	if err != nil || duplicate == nil || !duplicate.Duplicate || duplicate.OutputCap != 150 {
		t.Fatal(duplicate, err)
	}
	conflict := r
	conflict.Model = "other"
	if _, err := l.Reserve(ctx, conflict); err == nil {
		t.Fatal("conflicting request accepted")
	}
	q, err := l.ProviderQuota(ctx, "provider-a", at)
	if err != nil || q.Reserved != 250 || q.Remaining != 0 {
		t.Fatal(q, err)
	}
	if next, err := l.Reserve(ctx, reservation("request-2", at)); err != nil || next != nil {
		t.Fatal(next, err)
	}
	l.Close()
	l, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if claimed, err := l.Claim(ctx, r.ID); err != nil || claimed {
		t.Fatal("unknown request replayed", claimed, err)
	}
	q, err = l.ProviderQuota(ctx, "provider-a", at)
	if err != nil || q.Reserved != 250 {
		t.Fatal("unknown reservation lost", q, err)
	}
	if err = l.Settle(ctx, r.ID, Frame{Input: number(120), Cache: number(200)}); err != nil {
		t.Fatal(err)
	}
	q, _ = l.ProviderQuota(ctx, "provider-a", at)
	if q.Reserved != 250 || q.Used != 120 {
		t.Fatal("partial usage released reservation", q)
	}
	if err = l.Settle(ctx, r.ID, Frame{Output: number(20)}); err != nil {
		t.Fatal(err)
	}
	q, _ = l.ProviderQuota(ctx, "provider-a", at)
	if q.Reserved != 0 || q.Used != 140 || q.Remaining != 110 {
		t.Fatal(q)
	}
	s, err := l.Summary(ctx, Query{Scope: "main"}, at)
	if err != nil || s.Lifetime.Input != 120 || s.Lifetime.Output != 20 || s.Lifetime.Cache != 120 || s.Lifetime.Unknown != 0 {
		t.Fatal(s, err)
	}
}
func TestConcurrentAllowanceAndProviderCAS(t *testing.T) {
	ctx := context.Background()
	l, path := ledger(t)
	at := time.Now().UnixMilli()
	if err := l.SetPolicy(ctx, Policy{"day", 1000, "UTC", true}); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var wg sync.WaitGroup
	results := make(chan bool, 16)
	failures := make(chan error, 16)
	for i := range 16 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			db := l
			if i%2 == 1 {
				db = second
			}
			r := reservation(fmt.Sprintf("request-%d", i), at)
			result, err := db.Reserve(ctx, r)
			if err != nil {
				failures <- err
			} else {
				results <- result != nil
			}
		}(i)
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	wins := 0
	for allowed := range results {
		if allowed {
			wins++
		}
	}
	q, err := l.Quota(ctx, at)
	if err != nil || wins != 5 || q.Reserved != 1000 || q.Remaining != 0 {
		t.Fatal(wins, q, err)
	}
	failures = make(chan error, 2)
	for _, db := range []*Ledger{l, second} {
		wg.Add(1)
		go func(db *Ledger) {
			defer wg.Done()
			failures <- db.SetProviderPolicy(ctx, "provider-a", 0, Policy{"week", 10000, "UTC", true})
		}(db)
	}
	wg.Wait()
	close(failures)
	wins = 0
	for err := range failures {
		if err == nil {
			wins++
		}
	}
	if wins != 1 {
		t.Fatalf("provider CAS winners=%d", wins)
	}
}
func TestCalendarDSTAndManualResumeBothPolicies(t *testing.T) {
	ctx := context.Background()
	l, _ := ledger(t)
	p := Policy{"day", 1000, "America/New_York", false}
	at := time.Date(2026, 3, 8, 12, 0, 0, 0, time.UTC).UnixMilli()
	start, end, err := p.Window(at)
	if err != nil || end-start != 23*time.Hour.Milliseconds() {
		t.Fatal(start, end, err)
	}
	p.Period = "week"
	start, end, err = p.Window(at)
	if err != nil || time.UnixMilli(start).In(mustZone(t, p.Timezone)).Weekday() != time.Monday || end-start != 167*time.Hour.Milliseconds() {
		t.Fatal(start, end, err)
	}
	p.Period = "day"
	if err = l.SetPolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err = l.SetProviderPolicy(ctx, "provider-a", 0, Policy{"day", 500, "Asia/Shanghai", false}); err != nil {
		t.Fatal(err)
	}
	r := reservation("request", at)
	r.InputEstimate = 400
	r.MaxOutput = 100
	if allowed, err := l.Reserve(ctx, r); err != nil || allowed == nil {
		t.Fatal(allowed, err)
	}
	if err = l.Wait(ctx, WaitRequest{Scope: "main", Turn: "turn-1", Provider: "provider-a", AtMS: at, Required: 200}); err != nil {
		t.Fatal(err)
	}
	if ready, err := l.Ready(ctx, "main", at); err != nil || ready {
		t.Fatal(ready, err)
	}
	originalWait, err := l.Waiting(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = l.AdmitWaitRelease(ctx, "main", "resume", *originalWait); err != nil {
		t.Fatal(err)
	}
	if ready, err := l.Ready(ctx, "main", at); err != nil || ready {
		t.Fatal("manual resume bypassed quota", ready, err)
	}
	if err = l.Rejected(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if ready, err := l.Ready(ctx, "main", at); err != nil || !ready {
		t.Fatal(ready, err)
	}
	waiting, err := l.Waiting(ctx, "main")
	if err != nil || waiting == nil || !waiting.Released {
		t.Fatal(waiting, err)
	}
}
func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	zone, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return zone
}
func TestSummaryCompactionToolDedupAndCategories(t *testing.T) {
	ctx := context.Background()
	l, _ := ledger(t)
	at := time.Now().UnixMilli()
	for i := range 22 {
		r := reservation(fmt.Sprintf("request-%d", i), at)
		r.Model = fmt.Sprintf("model-%02d", i)
		r.Turn = fmt.Sprintf("turn-%d", i)
		if _, err := l.Reserve(ctx, r); err != nil {
			t.Fatal(err)
		}
		if err := l.Settle(ctx, r.ID, Frame{Input: number(int64(i + 1)), Output: number(5), Cache: number(1)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.MarkRoutingTurn(ctx, "turn-0"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := l.Tool(ctx, "main", "turn-0", ToolEvent{Action: "start", CallID: "call-1", Name: "exec"}, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Tool(ctx, "main", "turn-0", ToolEvent{Action: "end", CallID: "call-1", Content: []byte(`[ { "type": "text", "text": "hello" } ]`)}, at+50); err != nil {
		t.Fatal(err)
	}
	before, after := int64(1000), int64(100)
	if err := l.Compaction(ctx, "main", "turn-0", CompactionEvent{ID: "compaction-1", Action: "start", Reason: "threshold", TokensBefore: &before}, at); err != nil {
		t.Fatal(err)
	}
	if err := l.TagCompaction(ctx, "request-0", "compaction-1"); err != nil {
		t.Fatal(err)
	}
	if err := l.Compaction(ctx, "main", "turn-0", CompactionEvent{ID: "compaction-1", Action: "complete", TokensAfter: &after}, at+100); err != nil {
		t.Fatal(err)
	}
	if err := l.Compaction(ctx, "other", "turn-0", CompactionEvent{ID: "compaction-1", Action: "complete"}, at); err == nil {
		t.Fatal("compaction owner changed")
	}
	s, err := l.Summary(ctx, Query{Scope: "main", Days: 365, Timezone: "Asia/Shanghai"}, at+200)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Buckets) != 53 || s.Lifetime.Requests != 22 || s.RoutingTotal.Requests != 1 {
		t.Fatalf("summary mismatch: %+v", s)
	}
	models, err := l.Page(ctx, "models", Query{Scope: "main"}, at+200)
	if err != nil || len(models.Items.([]AgentModel)) != 20 || models.NextOffset == nil {
		t.Fatal(models, err)
	}
	next, err := l.Page(ctx, "models", Query{Scope: "main", Offset: 20}, at+200)
	if err != nil || len(next.Items.([]AgentModel)) != 2 || next.NextOffset != nil {
		t.Fatal(next, err)
	}
	tools, err := l.Page(ctx, "tools", Query{Scope: "main"}, at+200)
	if err != nil {
		t.Fatal(err)
	}
	tool := tools.Items.([]ToolTotal)
	if len(tool) != 1 || tool[0].Calls != 1 || tool[0].DurationMS != 50 {
		t.Fatal(tools)
	}
	compact, err := l.Page(ctx, "compactions", Query{Scope: "main"}, at+200)
	if err != nil {
		t.Fatal(err)
	}
	records := compact.Items.([]CompactionRecord)
	if len(records) != 1 || records[0].Requests != 1 || records[0].State != "complete" {
		t.Fatal(compact)
	}

}
func TestLifetimeRequestFilesRaceAndNeverReset(t *testing.T) {
	directory := t.TempDir()
	var wg sync.WaitGroup
	results := make(chan uint64, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := ReserveIn(directory, 3)
			if err == nil {
				results <- n
			}
		}()
	}
	wg.Wait()
	close(results)
	seen := map[uint64]bool{}
	for n := range results {
		if seen[n] {
			t.Fatal("duplicate reservation", n)
		}
		seen[n] = true
	}
	if len(seen) != 3 {
		t.Fatal(seen)
	}
	if _, err := ReserveIn(directory, 3); err == nil {
		t.Fatal("exhausted request count reset")
	}
}
