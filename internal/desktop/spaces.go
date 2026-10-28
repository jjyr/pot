//go:build darwin || (linux && desktop)

package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
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
