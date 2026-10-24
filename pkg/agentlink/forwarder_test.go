package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/peer"
)

type transportAnswer struct {
	data json.RawMessage
	err  error
}
type transportCall struct {
	request peer.Request
	answer  chan transportAnswer
}
type controlledTransport struct {
	calls chan transportCall
	done  chan struct{}
	once  sync.Once
	count atomic.Int32
}

func newControlledTransport() *controlledTransport {
	return &controlledTransport{calls: make(chan transportCall, 128), done: make(chan struct{})}
}
func (s *controlledTransport) Done() <-chan struct{} { return s.done }
func (s *controlledTransport) Close()                { s.once.Do(func() { close(s.done) }) }
func (s *controlledTransport) Call(ctx context.Context, r peer.Request) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	call := transportCall{request: r, answer: make(chan transportAnswer, 1)}
	s.count.Add(1)
	select {
	case s.calls <- call:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-s.done:
		return nil, peer.ErrClosed
	}
	select {
	case answer := <-call.answer:
		return answer.data, answer.err
	case <-ctx.Done():
		return nil, fmt.Errorf("%w: caller cancelled after dispatch", peer.ErrUncertain)
	case <-s.done:
		return nil, fmt.Errorf("%w: session closed after dispatch", peer.ErrUncertain)
	}
}
func forwardContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}
func testForwarder(t *testing.T, ctx context.Context, dial func(context.Context) (connectionTransport, error)) *connectionForwarder {
	t.Helper()
	f := newConnectionForwarder(ctx, dial, nil, nil)
	t.Cleanup(f.close)
	return f
}
func submitForward(t *testing.T, f *connectionForwarder, ctx context.Context, id string, control bool) *forwardRequest {
	t.Helper()
	r := &forwardRequest{ctx: ctx, request: peer.Request{ID: id, Method: "business", Params: json.RawMessage(`{}`)}, control: control}
	if control {
		r.request.Method = "exchange"
	}
	if err := f.submit(r); err != nil {
		t.Fatal(id, err)
	}
	return r
}
func nextTransportCall(t *testing.T, ctx context.Context, s *controlledTransport) transportCall {
	t.Helper()
	select {
	case call := <-s.calls:
		return call
	case <-ctx.Done():
		t.Fatal("missing transport dispatch", ctx.Err())
		return transportCall{}
	}
}
func forwardResultFor(t *testing.T, ctx context.Context, r *forwardRequest) forwardResult {
	t.Helper()
	select {
	case result := <-r.result:
		return result
	case <-ctx.Done():
		t.Fatal("missing result", r.request.ID, ctx.Err())
		return forwardResult{}
	}
}
func echoCall(call transportCall) {
	raw, _ := json.Marshal(call.request.ID)
	call.answer <- transportAnswer{data: raw}
}

func TestForwarderBoundedQueueFIFOAndIndependentControl(t *testing.T) {
	ctx := forwardContext(t)
	transport := newControlledTransport()
	f := testForwarder(t, ctx, func(context.Context) (connectionTransport, error) { return transport, nil })
	requests := map[string]*forwardRequest{}
	calls := []transportCall{}
	for i := 0; i < 32; i++ {
		id := fmt.Sprintf("active-%02d", i)
		requests[id] = submitForward(t, f, ctx, id, false)
		call := nextTransportCall(t, ctx, transport)
		if call.request.ID != id {
			t.Fatal(call.request.ID, id)
		}
		calls = append(calls, call)
	}
	for i := 0; i < 64; i++ {
		id := fmt.Sprintf("queued-%02d", i)
		requests[id] = submitForward(t, f, ctx, id, false)
	}
	extra := &forwardRequest{ctx: ctx, request: peer.Request{ID: "overflow", Method: "business"}}
	if err := f.submit(extra); !errors.Is(err, ErrCapacity) {
		t.Fatal("32 in flight plus 64 queued must reject before execution", err)
	}
	if transport.count.Load() != 32 {
		t.Fatal("queued requests entered transport", transport.count.Load())
	}
	requests["control"] = submitForward(t, f, ctx, "control", true)
	control := nextTransportCall(t, ctx, transport)
	if control.request.ID != "control" {
		t.Fatal("business queue blocked control", control.request.ID)
	}
	// Release just one slot at a time: the actual next transport invocation must
	// match queue admission order, even though earlier calls complete out of order.
	answered := map[string]bool{}
	failure := errors.New("remote business failure")
	for i, index := range []int{31, 2, 17} {
		call := calls[index]
		if i == 1 {
			call.answer <- transportAnswer{err: failure}
		} else {
			echoCall(call)
		}
		answered[call.request.ID] = true
		next := nextTransportCall(t, ctx, transport)
		want := fmt.Sprintf("queued-%02d", i)
		if next.request.ID != want {
			t.Fatal("non-FIFO dispatch", next.request.ID, want)
		}
		calls = append(calls, next)
	}
	// Control completion and all later replies may arrive in any order. Each
	// result must still be delivered to its original request's bounded channel.
	echoCall(control)
	answered["control"] = true
	for _, call := range calls {
		if !answered[call.request.ID] {
			echoCall(call)
			answered[call.request.ID] = true
		}
	}
	for i := 3; i < 64; i++ {
		call := nextTransportCall(t, ctx, transport)
		echoCall(call)
	}
	for id, r := range requests {
		result := forwardResultFor(t, ctx, r)
		if id == "active-02" {
			if !errors.Is(result.err, failure) {
				t.Fatal("lost original failure", result.err)
			}
			continue
		}
		var echo string
		if result.err != nil || json.Unmarshal(result.data, &echo) != nil || echo != id {
			t.Fatal("response correlation", id, echo, result.err)
		}
	}
	if transport.count.Load() != 97 {
		t.Fatal("overflow sent or request replayed", transport.count.Load())
	}
}

func TestForwarderQueuedCancellationDoesNotSendAndFreesCapacity(t *testing.T) {
	ctx := forwardContext(t)
	transport := newControlledTransport()
	f := testForwarder(t, ctx, func(context.Context) (connectionTransport, error) { return transport, nil })
	active := []transportCall{}
	for i := 0; i < 32; i++ {
		submitForward(t, f, ctx, fmt.Sprintf("active-%d", i), false)
		active = append(active, nextTransportCall(t, ctx, transport))
	}
	cancelledCtx, cancel := context.WithCancel(ctx)
	cancelled := submitForward(t, f, cancelledCtx, "never-send", false)
	for i := 1; i < 64; i++ {
		submitForward(t, f, ctx, fmt.Sprintf("queued-%d", i), false)
	}
	cancel()
	replacement := submitForward(t, f, ctx, "replacement", false)
	if result := forwardResultFor(t, ctx, cancelled); !errors.Is(result.err, context.Canceled) {
		t.Fatal(result.err)
	}
	for _, call := range active {
		echoCall(call)
	}
	for range 64 {
		call := nextTransportCall(t, ctx, transport)
		if call.request.ID == "never-send" {
			t.Fatal("cancelled queued request was sent")
		}
		echoCall(call)
	}
	if result := forwardResultFor(t, ctx, replacement); result.err != nil {
		t.Fatal(result.err)
	}
	if transport.count.Load() != 96 {
		t.Fatal("cancelled request reached transport", transport.count.Load())
	}
}

func TestForwarderInflightCancellationPreservesUncertainOutcomeWithoutReplay(t *testing.T) {
	ctx := forwardContext(t)
	transport := newControlledTransport()
	f := testForwarder(t, ctx, func(context.Context) (connectionTransport, error) { return transport, nil })
	requestCtx, cancel := context.WithCancel(ctx)
	result := make(chan error, 1)
	go func() {
		_, err := f.call(requestCtx, peer.Request{ID: "once", Method: "business"}, false)
		result <- err
	}()
	if call := nextTransportCall(t, ctx, transport); call.request.ID != "once" {
		t.Fatal(call.request.ID)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, peer.ErrUncertain) {
			t.Fatal("lost uncertain transport outcome", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	next := submitForward(t, f, ctx, "next", false)
	echoCall(nextTransportCall(t, ctx, transport))
	if response := forwardResultFor(t, ctx, next); response.err != nil {
		t.Fatal(response.err)
	}
	if transport.count.Load() != 2 {
		t.Fatal("cancelled in-flight request was replayed", transport.count.Load())
	}
}

func TestForwarderSharedDialIgnoresIndividualCancellationAndConnectionsAreIndependent(t *testing.T) {
	ctx := forwardContext(t)
	transport := newControlledTransport()
	entered := make(chan struct{})
	release := make(chan struct{})
	var dials atomic.Int32
	f := testForwarder(t, ctx, func(ctx context.Context) (connectionTransport, error) {
		if dials.Add(1) == 1 {
			close(entered)
		}
		select {
		case <-release:
			return transport, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	firstCtx, cancel := context.WithCancel(ctx)
	first := submitForward(t, f, firstCtx, "cancelled-first", false)
	<-entered
	remaining := []*forwardRequest{}
	for i := 0; i < 16; i++ {
		remaining = append(remaining, submitForward(t, f, ctx, fmt.Sprintf("shared-%d", i), false))
	}
	cancel()
	if result := forwardResultFor(t, ctx, first); !errors.Is(result.err, context.Canceled) {
		t.Fatal(result.err)
	}
	otherTransport := newControlledTransport()
	other := testForwarder(t, ctx, func(context.Context) (connectionTransport, error) { return otherTransport, nil })
	independent := submitForward(t, other, ctx, "independent", false)
	echoCall(nextTransportCall(t, ctx, otherTransport))
	if result := forwardResultFor(t, ctx, independent); result.err != nil {
		t.Fatal("different connection blocked by slow dial", result.err)
	}
	close(release)
	for range remaining {
		echoCall(nextTransportCall(t, ctx, transport))
	}
	for _, r := range remaining {
		if result := forwardResultFor(t, ctx, r); result.err != nil {
			t.Fatal(result.err)
		}
	}
	if dials.Load() != 1 || transport.count.Load() != 16 {
		t.Fatal("did not share dial or sent cancelled waiter", dials.Load(), transport.count.Load())
	}
}

func TestForwarderDisconnectDiscardsLateDialAndReconnects(t *testing.T) {
	ctx := forwardContext(t)
	old := newControlledTransport()
	fresh := newControlledTransport()
	entered := make(chan struct{})
	release := make(chan struct{})
	var dials atomic.Int32
	f := testForwarder(t, ctx, func(context.Context) (connectionTransport, error) {
		if dials.Add(1) == 1 {
			close(entered)
			<-release
			return old, nil
		}
		return fresh, nil
	})
	pending := submitForward(t, f, ctx, "old-generation", false)
	<-entered
	if err := f.change(ctx, ErrDisconnected, nil); err != nil {
		t.Fatal(err)
	}
	if result := forwardResultFor(t, ctx, pending); !errors.Is(result.err, ErrDisconnected) {
		t.Fatal(result.err)
	}
	close(release)
	select {
	case <-old.Done():
	case <-ctx.Done():
		t.Fatal("late dial was not discarded")
	}
	if err := f.change(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
	request := submitForward(t, f, ctx, "new-generation", false)
	echoCall(nextTransportCall(t, ctx, fresh))
	if result := forwardResultFor(t, ctx, request); result.err != nil {
		t.Fatal(result.err)
	}
}

func TestForwarderInboundAttachWinsPendingDialAndOldDoneCannotRemoveIt(t *testing.T) {
	ctx := forwardContext(t)
	outbound := newControlledTransport()
	inbound := newControlledTransport()
	entered := make(chan struct{})
	release := make(chan struct{})
	f := testForwarder(t, ctx, func(context.Context) (connectionTransport, error) { close(entered); <-release; return outbound, nil })
	request := submitForward(t, f, ctx, "inbound-wins", false)
	<-entered
	if err := f.change(ctx, nil, inbound); err != nil {
		t.Fatal(err)
	}
	call := nextTransportCall(t, ctx, inbound)
	close(release)
	select {
	case <-outbound.Done():
	case <-ctx.Done():
		t.Fatal("superseded outbound survived")
	}
	select {
	case <-inbound.Done():
		t.Fatal("late outbound closed selected inbound")
	default:
	}
	echoCall(call)
	if result := forwardResultFor(t, ctx, request); result.err != nil {
		t.Fatal(result.err)
	}
	next := submitForward(t, f, ctx, "still-inbound", false)
	echoCall(nextTransportCall(t, ctx, inbound))
	if result := forwardResultFor(t, ctx, next); result.err != nil {
		t.Fatal("old Done removed current session", result.err)
	}
}

func TestForwarderCloseDrainsQueuedAndSentWaitersAndCancelsDial(t *testing.T) {
	ctx := forwardContext(t)
	t.Run("dial", func(t *testing.T) {
		entered := make(chan struct{})
		stopped := make(chan struct{})
		f := testForwarder(t, ctx, func(ctx context.Context) (connectionTransport, error) {
			close(entered)
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		})
		request := submitForward(t, f, ctx, "pending", false)
		<-entered
		f.close()
		<-stopped
		if result := forwardResultFor(t, ctx, request); !errors.Is(result.err, ErrUnavailable) {
			t.Fatal(result.err)
		}
	})
	t.Run("calls", func(t *testing.T) {
		transport := newControlledTransport()
		f := testForwarder(t, ctx, func(context.Context) (connectionTransport, error) { return transport, nil })
		sent := submitForward(t, f, ctx, "sent", false)
		nextTransportCall(t, ctx, transport)
		pending := []*forwardRequest{}
		// Fill every business slot before placing an unsent request behind them.
		for i := 1; i < 32; i++ {
			pending = append(pending, submitForward(t, f, ctx, fmt.Sprintf("sent-%d", i), false))
			nextTransportCall(t, ctx, transport)
		}
		queued := submitForward(t, f, ctx, "queued", false)
		f.close()
		if result := forwardResultFor(t, ctx, sent); !errors.Is(result.err, peer.ErrUncertain) {
			t.Fatal("sent result was hidden by shutdown", result.err)
		}
		for _, request := range pending {
			if result := forwardResultFor(t, ctx, request); !errors.Is(result.err, peer.ErrUncertain) {
				t.Fatal(result.err)
			}
		}
		if result := forwardResultFor(t, ctx, queued); !errors.Is(result.err, ErrUnavailable) {
			t.Fatal(result.err)
		}
		if transport.count.Load() != 32 {
			t.Fatal("shutdown sent queued call", transport.count.Load())
		}
	})
}

type immediateTransport struct{ *controlledTransport }

func (s immediateTransport) Call(_ context.Context, r peer.Request) (json.RawMessage, error) {
	s.count.Add(1)
	return json.Marshal(r.ID)
}
func TestForwarderAuthenticatedCallsExceedOldRateBudget(t *testing.T) {
	ctx := forwardContext(t)
	transport := immediateTransport{newControlledTransport()}
	f := testForwarder(t, ctx, func(context.Context) (connectionTransport, error) { return transport, nil })
	for i := 0; i < 500; i++ {
		result, err := f.call(ctx, peer.Request{ID: fmt.Sprint(i), Method: "call"}, i%2 == 0)
		var id string
		if err != nil || json.Unmarshal(result.data, &id) != nil || id != fmt.Sprint(i) {
			t.Fatal(i, id, err)
		}
	}
	if transport.count.Load() != 500 {
		t.Fatal(transport.count.Load())
	}
}

func TestForwarderFailedDialEndsCurrentWaitersWithoutReplay(t *testing.T) {
	ctx := forwardContext(t)
	entered, release := make(chan struct{}), make(chan struct{})
	failure := errors.New("handshake rejected")
	transport := newControlledTransport()
	var dials atomic.Int32
	f := testForwarder(t, ctx, func(ctx context.Context) (connectionTransport, error) {
		if dials.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
				return nil, failure
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return transport, nil
	})
	first := submitForward(t, f, ctx, "first", false)
	<-entered
	second := submitForward(t, f, ctx, "second", false)
	close(release)
	for _, r := range []*forwardRequest{first, second} {
		if result := forwardResultFor(t, ctx, r); !errors.Is(result.err, failure) {
			t.Fatal(result.err)
		}
	}
	if dials.Load() != 1 {
		t.Fatal("failed requests were replayed", dials.Load())
	}
	next := submitForward(t, f, ctx, "explicit-next", false)
	echoCall(nextTransportCall(t, ctx, transport))
	if result := forwardResultFor(t, ctx, next); result.err != nil {
		t.Fatal(result.err)
	}
	if dials.Load() != 2 || transport.count.Load() != 1 {
		t.Fatal("unexpected replay", dials.Load(), transport.count.Load())
	}
}

func TestForwarderCommittedDisconnectSurvivesCallerCancellation(t *testing.T) {
	ctx := forwardContext(t)
	entered, release := make(chan struct{}), make(chan struct{})
	transport := newControlledTransport()
	f := testForwarder(t, ctx, func(context.Context) (connectionTransport, error) { return transport, nil })
	first := submitForward(t, f, ctx, "sent-before-disconnect", false)
	nextTransportCall(t, ctx, transport)
	caller, cancel := context.WithCancel(ctx)
	returned := make(chan error, 1)
	go func() {
		returned <- f.transition(caller, ErrDisconnected, nil, func() error {
			close(entered)
			<-release // Simulates a durable state change that has already committed.
			return nil
		})
	}()
	<-entered
	cancel()
	if err := <-returned; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	select {
	case <-transport.Done():
	case <-ctx.Done():
		t.Fatal("committed pause did not close transport")
	}
	if result := forwardResultFor(t, ctx, first); !errors.Is(result.err, peer.ErrUncertain) {
		t.Fatal(result.err)
	}
	if _, err := f.call(ctx, peer.Request{Method: "business"}, false); !errors.Is(err, ErrDisconnected) {
		t.Fatal("late caller reconnected paused transport", err)
	}
}
