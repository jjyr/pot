//go:build darwin

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/pkg/protocol"
	"net/url"
	"time"
)

func (s *Service) call(ctx context.Context, method, path, body string) (string, error) {
	u, err := url.ParseRequestURI(path)
	if err != nil || u.IsAbs() || u.Host != "" {
		return "", errors.New("invalid application path")
	}
	space := u.Query().Get("space_id")
	if u.Path == "/api/v1/spaces" {
		if method == "GET" {
			data, err := json.Marshal(s.spaces.Snapshot())
			return string(data), err
		}
		return "", errors.New("local UI scope is fixed; connect peers through Agent Link")
	}
	data, err := s.spaces.Call(ctx, space, method, path, json.RawMessage(body))
	return string(data), err
}
func (s *Service) streamSpace(ctx context.Context, space, owner, locale string) {
	path := "/api/v1/events?assistant_id=" + url.QueryEscape(owner) + "&locale=" + url.QueryEscape(locale)
	delay := 250 * time.Millisecond
	for {
		s.app.Event.Emit("pot:subscription", map[string]any{"space_id": space, "owner": owner, "status": "connecting"})
		err := s.spaces.Stream(ctx, space, path, func(event localui.Event) error {
			if event.Type != "conversation" {
				return nil
			}
			var value protocol.UIFrame
			if err := protocol.DecodeStrict(event.Data, &value); err != nil {
				return err
			}
			s.app.Event.Emit("pot:snapshot", map[string]any{"space_id": space, "owner": owner, "snapshot": value})
			s.app.Event.Emit("pot:subscription", map[string]any{"space_id": space, "owner": owner, "status": "connected"})
			delay = 250 * time.Millisecond
			return nil
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			s.app.Event.Emit("pot:subscription", map[string]any{"space_id": space, "owner": owner, "status": "offline", "error": err.Error()})
		}
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
