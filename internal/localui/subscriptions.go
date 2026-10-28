package localui

import (
	"context"
	"errors"
	"net/url"
	"sync"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// NativeEvent routes a broadcast to exactly one WebView subscription and one
// connection attempt. Neither a subscription receipt nor connected grants access.
type NativeEvent struct {
	Token      string            `json:"token"`
	Generation uint64            `json:"generation"`
	SpaceID    string            `json:"space_id"`
	Owner      string            `json:"owner"`
	Status     string            `json:"status,omitempty"`
	Error      string            `json:"error,omitempty"`
	Snapshot   *protocol.UIFrame `json:"snapshot,omitempty"`
}

type nativeSubscription struct{ cancel context.CancelFunc }

// NativeSubscriptions owns desktop stream recovery independently of Wails. The
// platform service supplies the same Manager and its broadcast event emitter.
type NativeSubscriptions struct {
	ctx     context.Context
	manager *Manager
	emit    func(string, NativeEvent)
	mu      sync.Mutex
	active  map[string]*nativeSubscription
	closed  bool
	wg      sync.WaitGroup
}

func NewNativeSubscriptions(ctx context.Context, manager *Manager, emit func(string, NativeEvent)) *NativeSubscriptions {
	return &NativeSubscriptions{ctx: ctx, manager: manager, emit: emit, active: make(map[string]*nativeSubscription)}
}

func (s *NativeSubscriptions) Subscribe(token, space, owner, locale string) error {
	if _, err := s.manager.Space(space); err != nil {
		return err
	}
	if !core.Identifier(owner) || !core.Identifier(token) {
		return errors.New("invalid subscription identity")
	}
	ctx, cancel := context.WithCancel(s.ctx)
	entry := &nativeSubscription{cancel: cancel}
	s.mu.Lock()
	if s.closed || len(s.active) >= 128 || s.active[token] != nil {
		s.mu.Unlock()
		cancel()
		return errors.New("subscription unavailable")
	}
	s.active[token] = entry
	s.wg.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.wg.Done()
		defer cancel()
		defer func() {
			s.mu.Lock()
			if s.active[token] == entry {
				delete(s.active, token)
			}
			s.mu.Unlock()
		}()
		s.stream(ctx, token, space, owner, locale)
	}()
	return nil
}

func (s *NativeSubscriptions) Unsubscribe(token string) {
	s.mu.Lock()
	entry := s.active[token]
	delete(s.active, token)
	s.mu.Unlock()
	if entry != nil {
		entry.cancel()
	}
}

func (s *NativeSubscriptions) Close() {
	s.mu.Lock()
	s.closed = true
	for token, entry := range s.active {
		entry.cancel()
		delete(s.active, token)
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *NativeSubscriptions) stream(ctx context.Context, token, space, owner, locale string) {
	path := "/api/v1/events?assistant_id=" + url.QueryEscape(owner) + "&locale=" + url.QueryEscape(locale)
	delay := 250 * time.Millisecond
	for generation := uint64(1); ctx.Err() == nil; generation++ {
		emit := func(name, status string, frame *protocol.UIFrame, err error) {
			if ctx.Err() != nil {
				return
			}
			value := NativeEvent{Token: token, Generation: generation, SpaceID: space, Owner: owner, Status: status, Snapshot: frame}
			if err != nil {
				value.Error = err.Error()
			}
			s.emit(name, value)
		}
		emit("pot:subscription", "connecting", nil, nil)
		err := s.manager.Stream(ctx, space, path, func(event Event) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if event.Type != "conversation" {
				return nil
			}
			var value protocol.UIFrame
			if err := protocol.DecodeStrict(event.Data, &value); err != nil {
				return err
			}
			emit("pot:snapshot", "", &value, nil)
			emit("pot:subscription", "connected", nil, nil)
			delay = 250 * time.Millisecond
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		// EOF is also a loss of transport, even if a Caller reports nil.
		if err == nil {
			err = errors.New("conversation stream closed")
		}
		emit("pot:subscription", "offline", nil, err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay = min(delay*2, 5*time.Second)
	}
}
