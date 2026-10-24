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
			s := &Service{options: Options{Backend: backend, HostIdentity: peer.Identity{NodeID: "receiver"}}, admissions: map[string]admission{}}
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
			for range maxConcurrentBusinessRequests {
				if !s.admit("connection", businessAdmission) {
					t.Fatal("handler leaked capacity")
				}
			}
		})
	}
}

func TestAuthenticatedAdmissionHasNoAccumulatedRateLimit(t *testing.T) {
	s := &Service{admissions: map[string]admission{}}
	const id = "connection"
	if !s.admit(id, controlAdmission) || s.admit(id, controlAdmission) {
		t.Fatal("control must retain one independent slot")
	}
	for range maxConcurrentBusinessRequests {
		if !s.admit(id, businessAdmission) {
			t.Fatal("control consumed business slot")
		}
	}
	if s.admit(id, businessAdmission) {
		t.Fatal("business limit exceeded")
	}
	s.release(id, controlAdmission)
	for range maxConcurrentBusinessRequests {
		s.release(id, businessAdmission)
	}
	for range 500 {
		if !s.admit(id, businessAdmission) || !s.admit(id, controlAdmission) {
			t.Fatal("authenticated calls retained old rate budget")
		}
		s.release(id, businessAdmission)
		s.release(id, controlAdmission)
	}
}
func TestPreviewKeepsIndependentAdmissionBudget(t *testing.T) {
	s := &Service{admissions: map[string]admission{}}
	if !s.admitPreview("invite") || s.admitPreview("invite") {
		t.Fatal("preview must retain single slot")
	}
	s.releasePreview("invite")
	for i := 1; i < maxPreviewRequestsPerMinute; i++ {
		if !s.admitPreview("invite") {
			t.Fatal("early preview rate limit", i)
		}
		s.releasePreview("invite")
	}
	if s.admitPreview("invite") || !s.admitPreview("other") {
		t.Fatal("preview rate budget is not scoped to invitation")
	}
	for range 200 {
		if !s.admit("invite", businessAdmission) {
			t.Fatal("preview consumed connection budget")
		}
		s.release("invite", businessAdmission)
	}
	s.previewRates["invite"] = previewRate{At: time.Now().Add(-2 * time.Minute), Count: 180, Active: 1}
	if s.admitPreview("invite") {
		t.Fatal("rollover released active preview")
	}
	s.releasePreview("invite")
	if !s.admitPreview("invite") {
		t.Fatal("preview window did not roll over")
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
	gate := &admissionBackend{entered: make(chan struct{}, maxConcurrentBusinessRequests), finish: make(chan error, maxConcurrentBusinessRequests)}
	backend.gate.Store(gate)
	// Always unblock server handlers before fixture/session cleanup, even when
	// an assertion fails. A caller timeout does not cancel a remote handler.
	defer close(gate.finish)
	done := make(chan error, maxConcurrentBusinessRequests)
	for range maxConcurrentBusinessRequests {
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
		t.Fatal("excess raw peer request was not rejected", err)
	}
	if gate.calls.Load() != maxConcurrentBusinessRequests {
		t.Fatal("rejected peer request entered backend", gate.calls.Load())
	}
	for range maxConcurrentBusinessRequests {
		gate.finish <- nil
	}
	for range maxConcurrentBusinessRequests {
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
