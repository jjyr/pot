package resourcecontrol

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSameSandboxExecutionsCancelIndependentlyAndReuseConfirmedResources(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	gate := make(chan struct{})
	var calls atomic.Int32
	options, closeStore := fixture(t, roundTrip(func(r *http.Request) (*http.Response, error) {
		calls.Add(1)
		select {
		case <-gate:
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("verified fixture bytes")), ContentLength: 22}, nil
		case <-r.Context().Done():
			return nil, r.Context().Err()
		}
	}))
	defer closeStore()
	m, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	a, err := m.requestFor(ctx, "shared", "", "task-a", "turn-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.requestFor(ctx, "shared", "", "task-b", "turn-b")
	if err != nil || a.ID == b.ID {
		t.Fatal(a, b, err)
	}
	for _, r := range []Request{a, b} {
		if _, err = m.Start(r.ID); err != nil {
			t.Fatal(err)
		}
	}
	for {
		m.mu.Lock()
		subscribed := m.entries[a.ID].request.ResourceID != "" && m.entries[b.ID].request.ResourceID != ""
		changed := m.changed
		m.mu.Unlock()
		if subscribed {
			break
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if _, err = m.Cancel(a.ID); err != nil {
		t.Fatal(err)
	}
	close(gate)
	await(t, m, b.ID, "ready")
	await(t, m, a.ID, "cancelled")
	if calls.Load() != 1 {
		t.Fatal("shared transfer repeated", calls.Load())
	}
	before, err := m.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.WaitFor(ctx, "shared", "", "task-c", "turn-c"); err != nil {
		t.Fatal(err)
	}
	after, err := m.Snapshot(ctx)
	if err != nil || len(after.Requests) != len(before.Requests) {
		t.Fatal("confirmed bundle created another consent request", after, err)
	}
}

func TestCancelledManualConsentCanBeRequestedAgainWithoutRestartingOldWait(t *testing.T) {
	options, closeStore := fixture(t, nil)
	defer closeStore()
	m, err := Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := m.Request(ctx, "shared", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Cancel(first.ID); err != nil {
		t.Fatal(err)
	}
	next, err := m.Request(ctx, "shared", "")
	if err != nil || next.ID == first.ID || next.State != "awaiting_consent" {
		t.Fatal(first, next, err)
	}
	m.mu.Lock()
	old := m.entries[first.ID].request
	m.mu.Unlock()
	if old.State != "cancelled" {
		t.Fatal("new owner action revived original cancelled request", old)
	}
}
