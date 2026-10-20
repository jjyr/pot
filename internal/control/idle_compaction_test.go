package control

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/core"
)

func TestIdleCompactionCanonicalOutcomeGate(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	db := s.Tasks.DB()
	put := func(owner, id, body string) {
		t.Helper()
		if _, err := db.Exec(`INSERT OR REPLACE INTO tasks(assistant_id,id,phase,created,body)VALUES(?,?,'running',1,?)`, owner, id, body); err != nil {
			t.Fatal(err)
		}
	}
	check := func(block bool) {
		t.Helper()
		started := false
		err := s.compactGate("main")(ctx, func() error { started = true; return nil })
		if block {
			if !errors.Is(err, assistant.ErrCompactDeferred) || started {
				t.Fatal("unprocessed outcome admitted", err)
			}
		} else if err != nil || !started {
			t.Fatal("unfinished/foreign Task blocked", err)
		}
	}
	put("main", "running", `{"run_id":"r"}`)
	put("foreign", "done", `{"run_id":"f","outcome":{"phase":"complete"}}`)
	check(false)
	put("main", "done", `{"run_id":"run","outcome":{"phase":"failed"}}`)
	check(true) // Before notification enqueue.
	envelope := `{"kind":"task_result","result":{"task_id":"done","run_id":"run"}}`
	if _, err := db.Exec(`INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient)VALUES('result',?,'in','queued','c','main')`, envelope); err != nil {
		t.Fatal(err)
	}
	check(true)
	if _, err := db.Exec(`UPDATE agent_messages SET state='processed' WHERE id='result'`); err != nil {
		t.Fatal(err)
	}
	check(false)
	put("main", "done", `{"run_id":"next-run","outcome":{"phase":"complete"}}`)
	check(true) // Old run's receipt cannot consume the new result.
	put("main", "done", `{"run_id":"next-run","outcome":{"phase":"complete","suppression_reason":"schedule policy"}}`)
	check(false)
	// Completion uses the same SQLite writer boundary. It cannot land between
	// the outcome check and the durable compact start callback.
	entered := make(chan struct{})
	release := make(chan struct{})
	gated := make(chan error, 1)
	go func() { gated <- s.compactGate("main")(ctx, func() error { close(entered); <-release; return nil }) }()
	<-entered
	committed := make(chan error, 1)
	go func() {
		_, err := db.Exec(`UPDATE tasks SET body='{"run_id":"next-run","outcome":{"phase":"complete"}}' WHERE id='done' AND assistant_id='main'`)
		committed <- err
	}()
	select {
	case err := <-committed:
		t.Fatal("completion crossed start boundary", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	if err := <-gated; err != nil {
		t.Fatal(err)
	}
	if err := <-committed; err != nil {
		t.Fatal(err)
	}
	check(true)
}
func TestIdleCompactionDelayConnectionAndModel(t *testing.T) {
	s := taskResourceService(t)
	ctx := context.Background()
	minutes := uint64(20)
	s.Profiles.mu.Lock()
	s.Profiles.state.Profiles = []core.Profile{{ID: "p", ModelSettings: &core.ModelSettings{IdleCompactMinutes: &minutes, IdleCompactModels: map[string]uint64{"fast": 5}}}}
	s.Profiles.mu.Unlock()
	for _, tc := range []struct {
		profile, model string
		want           time.Duration
	}{{"p", "fast", 5 * time.Minute}, {"p", "other", 20 * time.Minute}, {"removed", "fast", 0}} {
		got, err := s.idleCompactPolicy(ctx, core.Thread{Config: core.ResolvedSpec{SourceProfileID: tc.profile, Model: tc.model}})
		if err != nil || got.Delay != tc.want {
			t.Fatal(tc, got, err)
		}
	}
}
