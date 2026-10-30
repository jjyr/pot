package control

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// StreamAgentExecution subscribes before reading state, coalesces notifications,
// and keeps terminal snapshots subscribed until the consumer cancels.
func (s *Service) StreamAgentExecution(ctx context.Context, owner, request string, receive func(protocol.AgentExecutionStatus) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c, err := s.Conversation(ctx, owner)
	if err != nil {
		return err
	}
	conversation, remove, err := c.SubscribeUI(ctx)
	if err != nil {
		return err
	}
	defer remove()
	subscriptions := []*core.Subscription{s.Tasks.Subscribe(), s.Mailbox.Changes.Subscribe(), s.Permissions.Changes.Subscribe(), s.RunJournal.Subscribe()}
	wake := make(chan struct{}, 1)
	signal := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	var wg sync.WaitGroup
	for i, sub := range subscriptions {
		wg.Add(1)
		go func(index int, sub *core.Subscription) {
			defer wg.Done()
			var runs uint64
			for {
				topics, e := sub.Next(ctx)
				if e != nil {
					return
				}
				if index == 3 {
					if topics["runs"] == runs {
						continue
					}
					runs = topics["runs"]
				}
				signal()
			}
		}(i, sub)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-conversation:
				if !ok {
					cancel()
					return
				}
				signal()
			}
		}
	}()
	defer func() {
		cancel()
		for _, sub := range subscriptions {
			sub.Close()
		}
		wg.Wait()
	}()
	var previous []byte
	publish := func() error {
		value, e := s.AgentExecutionStatus(ctx, owner, request)
		if e != nil {
			return e
		}
		signature := value
		// Conversation token deltas alone must not turn this into a token stream.
		signature.ConversationCursor = 0
		raw, e := json.Marshal(signature)
		if e != nil {
			return e
		}
		if bytes.Equal(raw, previous) {
			return nil
		}
		if e = receive(value); e != nil {
			return e
		}
		previous = raw
		return nil
	}
	if err = publish(); err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
		}
		// A bounded debounce runs only after a real notification, never periodically.
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		select {
		case <-wake:
		default:
		}
		if err = publish(); err != nil {
			return err
		}
	}
}
