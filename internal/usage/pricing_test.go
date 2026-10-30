package usage

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestExactCostCacheAndMissingUsage(t *testing.T) {
	p := Price{Version: "test", Input: "2", Output: "8", CacheRead: "0.2", CacheWrite: "2.5"}
	got, e := Cost(p, number(1000), number(200), number(300), number(100))
	if e != nil || got != "0.003110000000000" {
		t.Fatal(got, e)
	}
	for _, v := range []struct{ in, out, read, write *int64 }{
		{nil, number(1), number(0), number(0)}, {number(1), nil, number(0), number(0)},
		{number(1), number(1), nil, number(0)}, {number(1), number(1), number(0), nil},
		{number(1), number(1), number(1), number(1)}, {number(1), number(1), number(-1), number(0)},
	} {
		if _, e = Cost(p, v.in, v.out, v.read, v.write); e == nil {
			t.Fatal("incomplete/invalid cost accepted", v)
		}
	}
	p.CacheRead = ""
	p.CacheWrite = ""
	p.Input = "0.000000001"
	p.Output = "0"
	if got, e = Cost(p, number(1), number(0), nil, nil); e != nil || got != "0.000000000000001" {
		t.Fatal(got, e)
	}
	for _, s := range []string{"-1", "NaN", "1e2", "1/2", "1000001", "0.0000000001", ""} {
		p.Input = s
		if p.Validate() == nil {
			t.Fatal(s)
		}
	}
}

func TestAccountingSnapshotsAggregationAndRestart(t *testing.T) {
	l, path := ledger(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC).UnixMilli()
	rate := Price{Version: "v1", Input: "2", Output: "8", CacheRead: "0.2"}
	add := func(id, owner, task, request, stage string, settle bool) {
		t.Helper()
		r := reservation(id, at)
		r.Price = &rate
		r.Attribution = &Attribution{Assistant: owner, Task: task, Request: request, Stage: stage}
		if task != "" {
			r.Scope = task
		}
		if _, e := l.Reserve(ctx, r); e != nil {
			t.Fatal(e)
		}
		if _, e := l.Claim(ctx, id); e != nil {
			t.Fatal(e)
		}
		if settle {
			for range 2 {
				if e := l.Settle(ctx, id, Frame{Input: number(1000), Output: number(200), Cache: number(300), Reasoning: number(100)}); e != nil {
					t.Fatal(e)
				}
			}
		}
	}
	add("main", "owner", "", "ask", "assistant_processing", true)
	rate.Input = "100"
	rate.Version = "v2"
	// Updating usage must retain the price captured before the price table changed.
	if e := l.Settle(ctx, "main", Frame{Output: number(200)}); e != nil {
		t.Fatal(e)
	}
	add("task", "owner", "task-1", "ask", "task_execution", true)
	add("other", "other", "", "other-request", "assistant_processing", true)
	add("missing", "owner", "", "ask", "assistant_result", false)
	add("rejected", "owner", "", "ask", "assistant_processing", false)
	if e := l.Rejected(ctx, "rejected"); e != nil {
		t.Fatal(e)
	}
	r := reservation("pending", at)
	r.Attribution = &Attribution{Assistant: "owner"}
	if _, e := l.Reserve(ctx, r); e != nil {
		t.Fatal(e)
	}
	q := AccountingQuery{Query: Query{Days: 14, Timezone: "UTC"}, Assistant: "owner", Request: "ask", GroupBy: "stage"}
	got, e := l.Accounting(ctx, q, at+1)
	if e != nil {
		t.Fatal(e)
	}
	// v1: .00306, v2: .07166; unknown request contributes no invented amount.
	if got.Total.Requests != 3 || got.Total.Priced != 2 || got.Total.MissingUsage != 1 || got.Total.USD != "0.074720000000000" || got.Total.Complete || len(got.Groups) != 3 {
		t.Fatalf("%+v", got)
	}
	q.Task = "task-1"
	got, e = l.Accounting(ctx, q, at+1)
	if e != nil || got.Total.Requests != 1 || got.Total.USD != "0.071660000000000" {
		t.Fatal(got, e)
	}
	l.Close()
	l, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	got, e = l.Accounting(ctx, q, at+1)
	if e != nil || got.Total.USD != "0.071660000000000" {
		t.Fatal(got, e)
	}
}

func TestAccountingCacheFramesAndAuditPages(t *testing.T) {
	l, _ := ledger(t)
	ctx := t.Context()
	at := time.Now().UnixMilli()
	p := Price{Version: "audit", Input: "2", Output: "8", CacheRead: "0.2", CacheWrite: "2.5"}
	for i := range 22 {
		r := reservation(fmt.Sprintf("r-%02d", i), at)
		r.Price = &p
		r.Attribution = &Attribution{Assistant: "owner", Request: "request", Stage: "task_execution", Effort: "high"}
		if _, e := l.Reserve(ctx, r); e != nil {
			t.Fatal(e)
		}
		// Cache reports may arrive before input; retain the raw counts for pricing.
		if e := l.Settle(ctx, r.ID, Frame{Cache: number(300), CacheWrite: number(100), Reasoning: number(50)}); e != nil {
			t.Fatal(e)
		}
		if e := l.Settle(ctx, r.ID, Frame{Input: number(1000), Output: number(200)}); e != nil {
			t.Fatal(e)
		}
	}
	q := AccountingQuery{Query: Query{Days: 14, Timezone: "UTC"}, Details: true, Assistant: "owner"}
	v, e := l.Accounting(ctx, q, at+1)
	if e != nil || v.Total.Requests != 22 || v.Total.USD != "0.068420000000000" || len(v.Records) != 20 || v.NextOffset == nil || *v.NextOffset != 20 {
		t.Fatal(v, e)
	}
	r := v.Records[0]
	if r.Price == nil || r.Price.Version != "audit" || r.CacheWrite == nil || *r.CacheWrite != 100 || r.Reasoning == nil || *r.Reasoning != 50 || r.Effort != "high" {
		t.Fatal(r)
	}
	q.Offset = 20
	v, e = l.Accounting(ctx, q, at+1)
	if e != nil || len(v.Records) != 2 || v.NextOffset != nil || v.Total.Requests != 22 {
		t.Fatal(v, e)
	}
	if e = l.Settle(ctx, "r-00", Frame{Cache: number(1001)}); e != nil {
		t.Fatal(e)
	}
	v, e = l.Accounting(ctx, q, at+1)
	if e != nil || v.Total.MissingUsage != 1 || v.Total.Priced != 21 {
		t.Fatal(v, e)
	}
}

func TestAccountingDuplicateReserveConcurrentFramesAndBoundaries(t *testing.T) {
	l, _ := ledger(t)
	ctx := t.Context()
	at := time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC).UnixMilli()
	price := Price{Version: "v1", Input: "2", Output: "8"}
	r := reservation("request", at)
	r.Price = &price
	r.Attribution = &Attribution{Assistant: "owner", Stage: "assistant_processing"}
	if _, e := l.Reserve(ctx, r); e != nil {
		t.Fatal(e)
	}
	price.Input = "10"
	price.Version = "v2"
	if v, e := l.Reserve(ctx, r); e != nil || !v.Duplicate {
		t.Fatal(v, e)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := l.Settle(ctx, r.ID, Frame{Input: number(1000), Output: number(200)}); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	q := AccountingQuery{Query: Query{Start: "2026-10-30", End: "2026-10-30", Timezone: "UTC"}}
	for _, group := range []string{"model", "stage", "assistant", "task", "date"} {
		q.GroupBy = group
		v, e := l.Accounting(ctx, q, at+100)
		if e != nil || v.Total.Requests != 1 || v.Total.USD != "0.003600000000000" || len(v.Groups) != 1 || v.Groups[0].USD != v.Total.USD {
			t.Fatal(v, e)
		}
	}
	q.Start = "2026-10-29"
	q.End = q.Start
	v, e := l.Accounting(ctx, q, at+100)
	if e != nil || v.Total.Requests != 0 {
		t.Fatal(v, e)
	}
	q.Start = "2026-10-30"
	q.End = q.Start
	q.Timezone = "America/Los_Angeles"
	v, e = l.Accounting(ctx, q, at+100)
	if e != nil || v.Total.Requests != 0 {
		t.Fatal(v, e)
	}
}
