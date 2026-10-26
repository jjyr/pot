package control

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestApprovalHooksUpdateUIWithoutAssistantDelivery(t *testing.T) {
	t.Parallel()
	for _, action := range []string{"allow_task", "allow_always", "deny", "expire", "policy", "cancel"} {
		t.Run(action, func(t *testing.T) {
			p := permissionFixture(t)
			s := &Service{}
			factsA, stopA := s.Lifecycle.Subscribe()
			defer stopA()
			factsB, stopB := s.Lifecycle.Subscribe()
			defer stopB()
			events := make(chan hooks.Event, 8)
			p.PublishHook = func(event hooks.Event) { s.publishHook(event); events <- event }
			sub := s.Changes.Subscribe()
			defer sub.Close()
			ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 3*time.Second))
			defer cancel()
			done := make(chan error, 1)
			r := hostRequest("original")
			r.Task = "original-task"
			go func() { _, err := p.Authorize(ctx, r); done <- err }()
			var requested hooks.Event
			select {
			case requested = <-events:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if requested.Kind != hooks.ApprovalRequested || requested.Owner != r.Owner || requested.TaskID != r.Task || requested.RunID != r.Turn || requested.Generation != r.Generation || requested.Result != nil {
				t.Fatal(requested)
			}
			for _, facts := range []<-chan protocol.LifecycleEvent{factsA, factsB} {
				select {
				case fact := <-facts:
					if fact.ID != requested.ID || fact.Owner != r.Owner || fact.Kind != "approval_requested" || fact.Task != r.Task || fact.Preview != "" {
						t.Fatal("lifecycle consumer lost committed approval", fact)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			if len(p.Pending(r.Owner)) != 1 {
				t.Fatal("UI cannot read pending approval")
			}
			var err error
			switch action {
			case "expire":
				p.Expire(r.Owner, r.Turn, r.Generation)
			case "policy":
				err = p.Configure(r.MachineID, MachinePolicy{ExecutionAccess: "deny"})
			case "cancel":
				cancel()
			default:
				err = p.Decide(r.Owner, requested.ApprovalID, action)
			}
			if err != nil {
				t.Fatal(err)
			}
			wait, stop := context.WithTimeout(context.Background(), time.Second)
			defer stop()
			var resolved hooks.Event
			select {
			case resolved = <-events:
			case <-wait.Done():
				t.Fatal(wait.Err())
			}
			kind := hooks.ApprovalResolved
			if action == "expire" || action == "policy" || action == "cancel" {
				kind = hooks.ApprovalExpired
			}
			if resolved.Kind != kind || resolved.ApprovalID != requested.ApprovalID {
				t.Fatal(resolved)
			}
			result := <-done
			if (action == "allow_task" || action == "allow_always") && result != nil {
				t.Fatal(result)
			}
			if action == "deny" && !errors.Is(result, ErrHostDenied) {
				t.Fatal(result)
			}
			if len(p.Pending(r.Owner)) != 0 {
				t.Fatal("resolved/expired approval remained visible")
			}
			if kind == hooks.ApprovalResolved {
				if err = p.Decide(r.Owner, requested.ApprovalID, action); err != nil {
					t.Fatal(err)
				}
				p.Expire(r.Owner, r.Turn, r.Generation)
			}
			if len(events) != 0 {
				t.Fatal("approval lifecycle hook repeated")
			}
			topics, err := sub.Next(wait)
			if err != nil || topics["permissions"] == 0 || topics["tasks"] == 0 || topics["assistant-mail"] != 0 {
				t.Fatal("approval hook routed to assistant", topics, err)
			}
		})
	}
}

func TestGrantResolutionHooksCoverAllMatchingPendingCommands(t *testing.T) {
	t.Parallel()
	p := permissionFixture(t)
	events := make(chan hooks.Event, 8)
	p.PublishHook = func(event hooks.Event) { events <- event }
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 3*time.Second))
	defer cancel()
	done := make(chan error, 2)
	for _, call := range []string{"first", "second"} {
		r := hostRequest(call)
		go func() { _, err := p.Authorize(ctx, r); done <- err }()
	}
	requested := map[string]bool{}
	for len(requested) < 2 {
		select {
		case event := <-events:
			requested[event.ApprovalID] = true
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	for id := range requested {
		if err := p.Decide("assistant", id, "allow_task"); err != nil {
			t.Fatal(err)
		}
		break
	}
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	for range 2 {
		event := <-events
		if event.Kind != hooks.ApprovalResolved || !requested[event.ApprovalID] {
			t.Fatal(event)
		}
		delete(requested, event.ApprovalID)
	}
	if len(requested) != 0 || len(p.Pending("assistant")) != 0 {
		t.Fatal("matching pending approvals did not settle")
	}
}
