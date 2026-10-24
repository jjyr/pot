package agentlink

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jjyr/pot/pkg/peer"
)

const connectionQueueCapacity = 64

const (
	forwardQueued uint32 = iota
	forwardStarted
	forwardCanceled
)

type connectionTransport interface {
	Call(context.Context, peer.Request) (json.RawMessage, error)
	Close()
	Done() <-chan struct{}
}
type forwardResult struct {
	data    json.RawMessage
	session connectionTransport
	err     error
}
type forwardRequest struct {
	ctx      context.Context
	request  peer.Request
	control  bool
	accepted chan error
	result   chan forwardResult
	state    atomic.Uint32
	stop     func() bool
	session  connectionTransport
}
type forwardChange struct {
	prepare func() error
	reason  error
	session connectionTransport
	reply   chan error
}
type dialResult struct {
	generation uint64
	session    connectionTransport
	err        error
}

// connectionForwarder owns one connection's queue and transport. Its loop never
// waits for a network response; the Session multiplexes the bounded call workers.
type connectionForwarder struct {
	ctx          context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	requests     chan *forwardRequest
	changes      chan forwardChange
	wake         chan struct{}
	completed    chan *forwardRequest
	dialled      chan dialResult
	dial         func(context.Context) (connectionTransport, error)
	publish      func(connectionTransport)
	lost         func(connectionTransport)
	wg           sync.WaitGroup
	syncGate     chan struct{}
	metadataGate chan struct{}
}

func newConnectionForwarder(ctx context.Context, dial func(context.Context) (connectionTransport, error), publish, lost func(connectionTransport)) *connectionForwarder {
	ctx, cancel := context.WithCancel(ctx)
	f := &connectionForwarder{ctx: ctx, cancel: cancel, done: make(chan struct{}), requests: make(chan *forwardRequest), changes: make(chan forwardChange), wake: make(chan struct{}, 1), completed: make(chan *forwardRequest, maxConcurrentBusinessRequests+1), syncGate: make(chan struct{}, 1), metadataGate: make(chan struct{}, 1), dialled: make(chan dialResult), dial: dial, publish: publish, lost: lost}
	go f.run()
	return f
}
func (f *connectionForwarder) submit(r *forwardRequest) error {
	r.accepted = make(chan error, 1)
	r.result = make(chan forwardResult, 1)
	r.stop = context.AfterFunc(r.ctx, func() {
		select {
		case f.wake <- struct{}{}:
		default:
		}
	})
	select {
	case f.requests <- r:
	case <-r.ctx.Done():
		r.stop()
		return r.ctx.Err()
	case <-f.done:
		r.stop()
		return ErrUnavailable
	}
	// Ownership has transferred to the actor. It acknowledges admission without
	// waiting for network I/O; cancellation must not overtake that acknowledgement
	// and hide a transport result from a request that has already started.
	select {
	case err := <-r.accepted:
		return err
	case <-f.done:
		select {
		case err := <-r.accepted:
			return err
		default:
			return ErrUnavailable
		}
	}
}
func (f *connectionForwarder) call(ctx context.Context, request peer.Request, control bool) (forwardResult, error) {
	r := &forwardRequest{ctx: ctx, request: request, control: control}
	if err := f.submit(r); err != nil {
		return forwardResult{}, err
	}
	select {
	case result := <-r.result:
		return result, result.err
	case <-ctx.Done():
		if r.state.CompareAndSwap(forwardQueued, forwardCanceled) {
			return forwardResult{}, ctx.Err()
		}
		// A sent request may have an uncertain outcome. Preserve the transport's
		// answer instead of turning an abandoned wait into an automatic retry.
		result := <-r.result
		return result, result.err
	}
}
func (f *connectionForwarder) change(ctx context.Context, reason error, session connectionTransport) error {
	return f.transition(ctx, reason, session, nil)
}

// prepare and the transport fence commit in the same actor turn. Once accepted,
// caller cancellation cannot leave a persisted pause without its transport fence.
func (f *connectionForwarder) transition(ctx context.Context, reason error, session connectionTransport, prepare func() error) error {
	c := forwardChange{reason: reason, session: session, prepare: prepare, reply: make(chan error, 1)}
	select {
	case f.changes <- c:
	case <-ctx.Done():
		if session != nil {
			session.Close()
		}
		return ctx.Err()
	case <-f.done:
		if session != nil {
			session.Close()
		}
		return ErrUnavailable
	}
	select {
	case err := <-c.reply:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-f.done:
		return ErrUnavailable
	}
}
func (f *connectionForwarder) close() { f.cancel(); <-f.done }
func (f *connectionForwarder) run() {
	defer close(f.done)
	business := make(chan *forwardRequest, connectionQueueCapacity)
	control := make(chan *forwardRequest, 1)
	var session connectionTransport
	var sessionDone <-chan struct{}
	var generation uint64
	var dialCancel context.CancelFunc
	var dialing bool
	var reason error
	active := [2]int{}
	finish := func(r *forwardRequest, result forwardResult) { r.stop(); r.result <- result }
	rejectQueue := func(q chan *forwardRequest, err error) {
		if err == nil {
			err = ErrUnavailable
		}
		for len(q) > 0 {
			r := <-q
			finish(r, forwardResult{err: err})
		}
	}
	prune := func(q chan *forwardRequest) {
		for n := len(q); n > 0; n-- {
			r := <-q
			if err := r.ctx.Err(); err != nil {
				finish(r, forwardResult{err: err})
			} else {
				q <- r
			}
		}
	}
	drop := func() {
		generation++
		if dialCancel != nil {
			dialCancel()
			dialCancel = nil
		}
		dialing = false
		if session != nil {
			old := session
			session = nil
			sessionDone = nil
			old.Close()
			if f.lost != nil {
				f.lost(old)
			}
		}
	}
	startDial := func() {
		dialing = true
		ctx, cancel := context.WithTimeout(f.ctx, 15*time.Second)
		dialCancel = cancel
		epoch := generation
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			defer cancel()
			s, err := f.dial(ctx)
			result := dialResult{epoch, s, err}
			select {
			case f.dialled <- result:
			case <-f.ctx.Done():
				if s != nil {
					s.Close()
				}
			}
		}()
	}
	dispatch := func(r *forwardRequest) {
		if err := r.ctx.Err(); err != nil {
			finish(r, forwardResult{err: err})
			return
		}
		if f.ctx.Err() != nil {
			finish(r, forwardResult{err: ErrUnavailable})
			return
		}
		if !r.state.CompareAndSwap(forwardQueued, forwardStarted) {
			finish(r, forwardResult{err: r.ctx.Err()})
			return
		}
		if r.request.Method == "" {
			finish(r, forwardResult{session: session})
			return
		}
		kind := 0
		if r.control {
			kind = 1
		}
		active[kind]++
		r.session = session
		f.wg.Add(1)
		go func() {
			defer f.wg.Done()
			ctx, cancel := context.WithTimeout(r.ctx, 15*time.Second)
			stop := context.AfterFunc(f.ctx, cancel)
			data, err := r.session.Call(ctx, r.request)
			stop()
			cancel()
			finish(r, forwardResult{data: data, err: err})
			select {
			case f.completed <- r:
			case <-f.ctx.Done():
			}
		}()
	}
	pump := func() {
		prune(business)
		prune(control)
		if session == nil {
			if !dialing && (len(business) > 0 || len(control) > 0) {
				startDial()
			}
			return
		}
		// Separate existing control capacity remains available while business calls
		// are blocked. No business request owns or leases the multiplexed Session.
		if active[1] == 0 && len(control) > 0 {
			dispatch(<-control)
		}
		for active[0] < maxConcurrentBusinessRequests && len(business) > 0 {
			dispatch(<-business)
		}
	}
	for {
		if f.ctx.Err() != nil {
			rejectQueue(business, ErrUnavailable)
			rejectQueue(control, ErrUnavailable)
			drop()
			f.wg.Wait()
			return
		}
		pump()
		select {
		case <-f.ctx.Done():
			rejectQueue(business, ErrUnavailable)
			rejectQueue(control, ErrUnavailable)
			drop()
			f.wg.Wait()
			return
		case r := <-f.requests:
			err := r.ctx.Err()
			if err == nil && reason != nil && !(errors.Is(reason, ErrRevoked) && r.request.Method == "revoke") {
				err = reason
			}
			q := business
			if r.control {
				q = control
			}
			prune(q)
			if err == nil && len(q) == cap(q) {
				err = ErrCapacity
			}
			if err != nil {
				r.stop()
				r.accepted <- err
				continue
			}
			q <- r
			r.accepted <- nil
		case <-f.wake:
		case r := <-f.completed:
			kind := 0
			if r.control {
				kind = 1
			}
			active[kind]--
			if r.request.Method == "revoke" && session == r.session {
				drop()
			}
		case result := <-f.dialled:
			if result.generation != generation || !dialing {
				if result.session != nil {
					result.session.Close()
				}
				continue
			}
			dialing = false
			dialCancel = nil
			if result.err != nil {
				if result.session != nil {
					result.session.Close()
				}
				rejectQueue(business, result.err)
				rejectQueue(control, result.err)
				continue
			}
			session = result.session
			sessionDone = session.Done()
			if f.publish != nil {
				f.publish(session)
			}
		case <-sessionDone:
			old := session
			session = nil
			sessionDone = nil
			if f.lost != nil {
				f.lost(old)
			}
		case change := <-f.changes:
			if change.prepare != nil {
				if err := change.prepare(); err != nil {
					if change.session != nil {
						change.session.Close()
					}
					change.reply <- err
					continue
				}
			}
			if change.session != nil {
				if session == change.session {
					change.reply <- nil
					continue
				}
				if reason != nil {
					change.session.Close()
					change.reply <- reason
					continue
				}
				drop()
				session = change.session
				sessionDone = session.Done()
				if f.publish != nil {
					f.publish(session)
				}
			} else {
				rejectQueue(business, change.reason)
				rejectQueue(control, change.reason)
				drop()
				reason = change.reason
				if errors.Is(reason, ErrUnavailable) {
					reason = nil
				}
			}
			change.reply <- nil
		}
	}
}
