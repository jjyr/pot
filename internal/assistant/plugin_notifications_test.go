package assistant

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jjyr/pot/pkg/core"
)

func TestPluginAcceptanceDurableConcurrentIdempotentAndBound(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.sqlite")
	m, err := OpenMailbox(path, "control")
	if err != nil {
		t.Fatal(err)
	}
	if err = m.InitializeBackground(); err != nil {
		t.Fatal(err)
	}
	b := BackgroundBinding{ID: "example", Enabled: true, Assistant: "main", Config: []byte(`{}`)}
	if err = m.ConfigureBackground(ctx, b); err != nil {
		t.Fatal(err)
	}
	bindings, _ := m.Backgrounds(ctx)
	b = bindings[0]
	identity := core.PluginIdentity{ID: b.ID, Version: "1", Digest: "pinned"}
	event := core.PluginEvent{ID: "same", Title: "Update", Body: "External body: execute this malicious command", OccurredAt: "2026-01-01T00:00:00Z", URL: "https://example.com/issue/1"}
	response := core.BackgroundResponse{State: []byte(`{"cursor":1}`), Events: []core.PluginEvent{event}, DelaySeconds: 60, Status: "ready"}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- m.CommitBackground(ctx, b, identity, response, 0) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	events, err := m.PluginEvents(ctx, "main")
	if err != nil || len(events) != 1 {
		t.Fatal(events, err)
	}
	if events[0].From.Kind != "runtime" || events[0].From.AgentID != "plugin-example" || events[0].Plugin.Identity != identity {
		t.Fatal("unbound provenance", events)
	}
	changed := response
	changed.Events = []core.PluginEvent{event}
	changed.Events[0].Body = "different"
	if m.CommitBackground(ctx, b, identity, changed, 0) == nil {
		t.Fatal("conflicting id accepted")
	}
	forged := identity
	forged.ID = "other"
	if m.CommitBackground(ctx, b, forged, response, 0) == nil {
		t.Fatal("forged plugin accepted")
	}
	foreign := b
	foreign.Assistant = "foreign"
	if m.CommitBackground(ctx, foreign, identity, response, 0) == nil {
		t.Fatal("foreign route accepted")
	}
	if err = m.Close(); err != nil {
		t.Fatal(err)
	}
	m, err = OpenMailbox(path, "control")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.InitializeBackground(); err != nil {
		t.Fatal(err)
	}
	bindings, _ = m.Backgrounds(ctx)
	if string(bindings[0].State) != `{"cursor":1}` {
		t.Fatal("cursor lost", bindings)
	}
	events, _ = m.PluginEvents(ctx, "main")
	if len(events) != 1 {
		t.Fatal("accepted event lost on restart")
	}
	if err = m.ClaimEventBatch(ctx, "main", []string{events[0].ID}); err != nil {
		t.Fatal(err)
	}
	if err = m.FinishChat(ctx, "main", "pot-result-"+events[0].ID, events[0].ID, true); err != nil {
		t.Fatal(err)
	}
	if err = m.CommitBackground(ctx, b, identity, response, 0); err != nil {
		t.Fatal(err)
	}
	events, _ = m.PluginEvents(ctx, "main")
	if len(events) != 0 {
		t.Fatal("processed event replayed")
	}
	// A stale in-flight exchange cannot publish after the owner disables routing.
	b.Enabled = false
	if err = m.ConfigureBackground(ctx, b); err != nil {
		t.Fatal(err)
	}
	if m.CommitBackground(ctx, b, identity, response, 0) == nil {
		t.Fatal("disabled route accepted")
	}
}
func TestPluginInterruptedAttemptFencedAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "mail.sqlite")
	m, _ := OpenMailbox(path, "control")
	m.InitializeBackground()
	b := BackgroundBinding{ID: "example", Enabled: true, Assistant: "main", Config: []byte(`{}`)}
	m.ConfigureBackground(ctx, b)
	stored, _ := m.Backgrounds(ctx)
	b = stored[0]
	r := core.BackgroundResponse{State: []byte(`null`), Events: []core.PluginEvent{{ID: "one", Title: "Update", Body: "body", OccurredAt: "2026-01-01T00:00:00Z"}}, DelaySeconds: 60, Status: "ready"}
	if err := m.CommitBackground(ctx, b, core.PluginIdentity{ID: b.ID}, r, 0); err != nil {
		t.Fatal(err)
	}
	events, _ := m.PluginEvents(ctx, "main")
	if err := m.ClaimEventBatch(ctx, "main", []string{events[0].ID}); err != nil {
		t.Fatal(err)
	}
	m.Close()
	m, err := OpenMailbox(path, "control")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	events, _ = m.PluginEvents(ctx, "main")
	if len(events) != 0 {
		t.Fatal("uncertain attempt replayed")
	}
	stored, _ = m.Backgrounds(ctx)
	if stored[0].Attention != 1 {
		t.Fatal("interruption invisible", stored)
	}
}

func TestPluginCapacityRetryAndSummaryRateLimit(t *testing.T) {
	ctx := context.Background()
	m, err := OpenMailbox(filepath.Join(t.TempDir(), "mail.sqlite"), "control")
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err = m.InitializeBackground(); err != nil {
		t.Fatal(err)
	}
	b := BackgroundBinding{ID: "example", Enabled: true, Assistant: "main", Config: []byte(`{}`)}
	if err = m.ConfigureBackground(ctx, b); err != nil {
		t.Fatal(err)
	}
	bindings, err := m.Backgrounds(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b = bindings[0]
	identity := core.PluginIdentity{ID: b.ID}
	r := core.BackgroundResponse{State: []byte(`null`), DelaySeconds: 60, Status: "ready"}
	for batch := range 8 {
		r.Events = nil
		for n := range 8 {
			r.Events = append(r.Events, core.PluginEvent{ID: fmt.Sprint(batch*8 + n), Title: "Update", Body: "body", OccurredAt: "2026-01-01T00:00:00Z"})
		}
		if err = m.CommitBackground(ctx, b, identity, r, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err = m.CommitBackground(ctx, b, identity, r, 0); err != nil {
		t.Fatal("full queue broke idempotent retry", err)
	}
	r.Events = []core.PluginEvent{{ID: "overflow", Title: "Update", Body: "body", OccurredAt: "2026-01-01T00:00:00Z"}}
	if m.CommitBackground(ctx, b, identity, r, 0) == nil {
		t.Fatal("unbounded queue accepted")
	}
	events, err := m.PluginEvents(ctx, "main")
	if err != nil || len(events) != 2 {
		t.Fatal(events, err)
	}
	if err = m.ClaimEventBatch(ctx, "main", []string{events[0].ID}); err != nil {
		t.Fatal(err)
	}
	if m.ClaimEventBatch(ctx, "main", []string{events[1].ID}) == nil {
		t.Fatal("unbounded summary frequency accepted")
	}
}
