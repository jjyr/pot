//go:build darwin

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/spaces"
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
		if method != "POST" {
			return "", errors.New("invalid Spaces method")
		}
		var r struct {
			Action    string `json:"action"`
			SpaceID   string `json:"space_id"`
			Endpoint  string `json:"endpoint"`
			Token     string `json:"token"`
			CAPEM     string `json:"ca_pem"`
			ControlID string `json:"control_id"`
			Name      string `json:"name"`
		}
		if err = protocol.DecodeStrict([]byte(body), &r); err != nil {
			return "", err
		}
		switch r.Action {
		case "connect":
			if r.Endpoint != "" {
				err = s.spaces.Connect(ctx, spaces.ConnectRequest{Endpoint: r.Endpoint, Token: r.Token, CAPEM: r.CAPEM, ControlID: r.ControlID, Name: r.Name})
			} else {
				err = s.spaces.Reconnect(ctx, r.SpaceID)
			}
		case "disconnect":
			err = s.spaces.Disconnect(r.SpaceID)
		case "remove":
			err = s.spaces.Remove(r.SpaceID)
		case "select":
			err = s.spaces.Select(r.SpaceID)
		default:
			err = errors.New("unknown Space action")
		}
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(s.spaces.Snapshot())
		return string(data), err
	}
	data, err := s.spaces.Call(ctx, space, method, path, json.RawMessage(body))
	return string(data), err
}
func (s *Service) streamSpace(ctx context.Context, space, owner string) {
	path := "/api/v1/events?assistant_id=" + url.QueryEscape(owner)
	delay := 250 * time.Millisecond
	for {
		s.app.Event.Emit("pot:subscription", map[string]any{"space_id": space, "owner": owner, "status": "connecting"})
		err := s.spaces.Stream(ctx, space, path, func(event spaces.Event) error {
			if event.Type != "snapshot" {
				return nil
			}
			var value protocol.Snapshot
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
			s.app.Event.Emit("pot:subscription", map[string]any{"space_id": space, "owner": owner, "status": "offline"})
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
