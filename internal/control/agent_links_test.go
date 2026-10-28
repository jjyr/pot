package control

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/protocol"
)

// Retry only a confirmed capacity rejection, which precedes remote execution;
// retain the exact original request and return every other outcome unchanged.
func callAgentWhenReady(t *testing.T, ctx context.Context, service *Service, owner string, operation agentlink.Operation, call agentlink.Call) (any, error) {
	return retryAgentCapacity(t, ctx, func(ctx context.Context) (any, error) {
		return service.CallAgent(ctx, owner, operation, call)
	})
}

func retryAgentCapacity(t *testing.T, ctx context.Context, call func(context.Context) (any, error)) (any, error) {
	ctx, cancel := context.WithTimeout(ctx, testutil.Timeout(t, 5*time.Second))
	defer cancel()
	for {
		value, err := call(ctx)
		var api protocol.APIError
		if !errors.Is(err, agentlink.ErrCapacity) && !(errors.As(err, &api) && api.Code == agentlink.ErrCapacity.Error()) {
			return value, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestRetryAgentCapacityPreservesOtherOutcomes(t *testing.T) {
	t.Parallel()
	transport := errors.New("transport outcome unknown")
	for _, tc := range []struct {
		name  string
		err   error
		retry bool
	}{
		{name: "capacity", err: agentlink.ErrCapacity, retry: true},
		{name: "api capacity", err: protocol.APIError{Code: agentlink.ErrCapacity.Error()}, retry: true},
		{name: "success"},
		{name: "transport", err: transport},
		{name: "deadline", err: context.DeadlineExceeded},
		{name: "cancelled", err: context.Canceled},
		{name: "conflict", err: agentlink.ErrConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			value, err := retryAgentCapacity(t, t.Context(), func(context.Context) (any, error) {
				calls++
				if calls == 1 {
					return "original", tc.err
				}
				return "accepted", nil
			})
			if tc.retry {
				if calls != 2 || value != "accepted" || err != nil {
					t.Fatal(calls, value, err)
				}
			} else if calls != 1 || value != "original" || err != tc.err {
				t.Fatal("changed non-capacity outcome", calls, value, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	calls := 0
	_, err := retryAgentCapacity(t, ctx, func(context.Context) (any, error) {
		calls++
		cancel()
		return nil, agentlink.ErrCapacity
	})
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatal("retried after cancellation", calls, err)
	}
}

type agentControlFixture struct {
	service  *Service
	endpoint string
}

func agentControl(t *testing.T, id string) *agentControlFixture {
	t.Helper()
	fixture := &agentControlFixture{}
	manager, err := execution.New(id, "boot-"+id, execution.Limits{MaxActive: 1, MaxQueued: 8})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := secrets.NewMemoryKey(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	fixture.service, err = Open(Options{Root: t.TempDir(), RuntimeID: id, BootID: "boot-" + id, Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { fixture.service.Close() })
	if _, err = legacyMainAssistant(context.Background(), fixture.service, "create-main", "Main"); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func awaitAgentLinkMail(t *testing.T, s *Service, id string) protocol.MailRecord {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 10*time.Second))
	defer cancel()
	updates := s.Mailbox.Changes.Subscribe()
	defer updates.Close()
	for {
		records, err := s.Mailbox.History(ctx, "main", 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			if record.Message.ID == id {
				return record
			}
		}
		if _, err = updates.Next(ctx); err != nil {
			t.Fatalf("message %s was not delivered by Control host: %v", id, err)
		}
	}
}

func TestMissingAssistantCannotPublishIdentity(t *testing.T) {
	t.Parallel()
	s := taskControl(t, "empty-node", "")
	if _, err := s.agentLinkIdentity(context.Background(), "main"); err == nil {
		t.Fatal("missing assistant published identity")
	}
}
