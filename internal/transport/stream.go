package transport

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"sync"

	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func (app *Application) Stream(ctx context.Context, path string, receive func(localui.Event) error) error {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" || u.Scheme != "" {
		return protocol.APIError{Code: "not_found", Message: "stream not found"}
	}
	switch u.Path {
	case "/api/v1/events", "/api/v1/changes", "/api/v1/lifecycle":
	default:
		return protocol.APIError{Code: "not_found", Message: "stream not found"}
	}
	if app.Maintenance != nil && app.Maintenance() {
		return protocol.APIError{Code: "update_maintenance", Message: "update in progress"}
	}
	if app.Control == nil {
		return protocol.APIError{Code: "control_unavailable", Message: "control unavailable"}
	}
	switch u.Path {
	case "/api/v1/events":
		return app.streamConversationEvents(ctx, u, receive)
	case "/api/v1/changes":
		return app.streamChangeFeed(ctx, receive)
	default:
		return app.streamLifecycleFeed(ctx, receive)
	}
}

func (app *Application) streamConversationEvents(ctx context.Context, u *url.URL, receive func(localui.Event) error) error {
	owner, err := app.resolveOwner(ctx, u.Query().Get("assistant_id"))
	if err != nil {
		return protocol.APIError{Code: "owner_unavailable", Message: err.Error()}
	}
	subscription, err := app.Control.SubscribeAgent(ctx, owner, u.Query().Get("locale"))
	if err != nil {
		return protocol.APIError{Code: "owner_unavailable", Message: err.Error()}
	}
	defer subscription.Close()

	var cursor int64
	initial := true
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-subscription.Updates:
			if !ok {
				return nil
			}
			frame, err := subscription.Frame(ctx, cursor, initial)
			if err != nil {
				return err
			}
			cursor, initial = frame.Cursor, false
			data, err := json.Marshal(frame)
			if err != nil {
				return err
			}
			if err := receive(localui.Event{
				Type: "conversation",
				ID:   app.BootID + ":" + owner + ":" + strconv.FormatInt(frame.Cursor, 10),
				Data: json.RawMessage(data),
			}); err != nil {
				return err
			}
		}
	}
}

func (app *Application) streamChangeFeed(ctx context.Context, receive func(localui.Event) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	general := app.Control.Changes.Subscribe()
	permissions := app.Control.Permissions.Changes.Subscribe()
	defer general.Close()
	defer permissions.Close()

	merged := make(chan map[string]uint64, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	forward := func(s *core.Subscription) {
		defer wg.Done()
		for {
			topics, err := s.Next(ctx)
			if err != nil {
				return
			}
			select {
			case <-ctx.Done():
				return
			case merged <- topics:
			}
		}
	}
	go forward(general)
	go forward(permissions)
	go func() {
		wg.Wait()
		close(merged)
	}()

	seq := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case topics, ok := <-merged:
			if !ok {
				return nil
			}
			seq++
			body, err := json.Marshal(topics)
			if err != nil {
				return err
			}
			if err := receive(localui.Event{
				Type: "changed",
				ID:   app.BootID + ":" + strconv.Itoa(seq),
				Data: json.RawMessage(body),
			}); err != nil {
				return err
			}
		}
	}
}

func (app *Application) streamLifecycleFeed(ctx context.Context, receive func(localui.Event) error) error {
	return app.Control.StreamLifecycle(ctx, func(kind string, value any) error {
		body, err := json.Marshal(value)
		if err != nil {
			return err
		}
		return receive(localui.Event{
			Type: kind,
			Data: json.RawMessage(body),
		})
	})
}
