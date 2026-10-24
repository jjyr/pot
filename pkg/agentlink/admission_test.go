package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/peer"
)

// Directory is the first backend call in every business request. Holding it
// proves admission without starting Tasks or relying on scheduler timing.
type admissionBackend struct {
	Backend
	entered chan struct{}
	finish  chan error
	calls   atomic.Int32
}

func (b *admissionBackend) Directory(ctx context.Context) (Directory, error) {
	b.calls.Add(1)
	b.entered <- struct{}{}
	select {
	case err := <-b.finish:
		return Directory{NodeID: "receiver", Name: "Receiver", Adapter: "pot"}, err
	case <-ctx.Done():
		return Directory{}, ctx.Err()
	}
}

func TestBusinessAdmissionReleasesCapacity(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{{"success", nil}, {"error", errors.New("backend failure")}, {"cancel", context.Canceled}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			backend := &admissionBackend{entered: make(chan struct{}, 1), finish: make(chan error, 1)}
			s := &Service{options: Options{Backend: backend, HostIdentity: peer.Identity{NodeID: "receiver"}}, rates: map[string]rate{}}
			done := make(chan error, 1)
			go func() {
				_, err := s.business(ctx, Connection{ID: "connection"}, peer.Identity{}, peer.Request{Method: "directory", Params: json.RawMessage(`{}`)})
				done <- err
			}()
			select {
			case <-backend.entered:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if tc.err == context.Canceled {
				cancel()
			} else {
				backend.finish <- tc.err
			}
			if err := <-done; !errors.Is(err, tc.err) {
				t.Fatalf("got %v, want %v", err, tc.err)
			}
			for range 5 {
				if !s.admit("connection", businessAdmission) {
					t.Fatal("handler leaked capacity")
				}
			}
		})
	}
}

func TestAdmissionControlSlotAndSharedRateBudget(t *testing.T) {
	s := &Service{rates: map[string]rate{}}
	const id = "connection"
	if !s.admit(id, controlAdmission) || s.admit(id, controlAdmission) {
		t.Fatal("control requests must retain a single slot")
	}
	for range 5 {
		if !s.admit(id, businessAdmission) {
			t.Fatal("control request consumed business capacity")
		}
	}
	if s.admit(id, businessAdmission) || !s.admit("other-connection", businessAdmission) {
		t.Fatal("business capacity must be bounded per connection")
	}
	s.release("other-connection", businessAdmission)
	s.release(id, controlAdmission)
	for range 5 {
		s.release(id, businessAdmission)
	}
	// Include the six admissions above in the same per-connection rate window.
	for i := 6; i < 180; i++ {
		kind := admissionKind(i % 2)
		if !s.admit(id, kind) {
			t.Fatal("rate limit before 180 admissions", i)
		}
		s.release(id, kind)
	}
	if s.admit(id, businessAdmission) || s.admit(id, controlAdmission) || s.rates[id].Count != 180 {
		t.Fatal("180/minute limit changed", s.rates[id])
	}
	if !s.admit("other-connection", businessAdmission) {
		t.Fatal("rate budget was shared between connections")
	}
	s.release("other-connection", businessAdmission)
	// Expiring the rate window must not release still-running handlers.
	s.rates[id] = rate{At: time.Now().Add(-2 * time.Minute), Count: 180, Active: [2]int{0, 5}}
	if s.admit(id, businessAdmission) {
		t.Fatal("rate-window rollover released an active handler")
	}
	s.release(id, businessAdmission)
	if !s.admit(id, businessAdmission) || s.rates[id].Count != 1 || s.rates[id].Active[businessAdmission] != 5 {
		t.Fatal("rate window did not reset while retaining active handlers", s.rates[id])
	}
}

type peerAdmissionBackend struct {
	*testBackend
	gate atomic.Pointer[admissionBackend]
}

func (b *peerAdmissionBackend) Directory(ctx context.Context) (Directory, error) {
	if gate := b.gate.Load(); gate != nil {
		return gate.Directory(ctx)
	}
	return b.testBackend.Directory(ctx)
}

func TestBusinessAdmissionOverAuthenticatedPeer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 15*time.Second))
	defer cancel()
	a, b := newFixture(t, "sender"), newFixture(t, "receiver")
	a.service.options.Backend = &testBackend{f: a}
	backend := &peerAdmissionBackend{testBackend: &testBackend{f: b}}
	b.service.options.Backend = backend
	c := connect(t, a, b)
	session, err := a.service.ensureSession(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	gate := &admissionBackend{entered: make(chan struct{}, 5), finish: make(chan error, 5)}
	backend.gate.Store(gate)
	// Always unblock server handlers before fixture/session cleanup, even when
	// an assertion fails. A caller timeout does not cancel a remote handler.
	defer close(gate.finish)
	done := make(chan error, 5)
	for range 5 {
		go func() {
			var d Directory
			done <- a.service.call(ctx, session, "directory", struct{}{}, &d)
		}()
		select {
		case <-gate.entered:
		case err := <-done:
			t.Fatalf("request returned before backend entry: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	var d Directory
	if err := a.service.call(ctx, session, "directory", struct{}{}, &d); !errors.Is(err, ErrCapacity) {
		t.Fatal("sixth peer request was not rejected", err)
	}
	if gate.calls.Load() != 5 {
		t.Fatal("rejected peer request entered backend", gate.calls.Load())
	}
	for range 5 {
		gate.finish <- nil
	}
	for range 5 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	gate.finish <- nil
	if err := a.service.call(ctx, session, "directory", struct{}{}, &d); err != nil || d.NodeID != b.id {
		t.Fatal("peer capacity did not recover", d, err)
	}
}
