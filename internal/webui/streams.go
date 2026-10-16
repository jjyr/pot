package webui

import (
	"context"
	"errors"
	"net/http"
	"time"
)

func (s *Server) stream(r *http.Request, generation uint64) (context.Context, func(), error) {
	ctx, cancel := context.WithCancel(r.Context())
	s.mu.Lock()
	if s.config.Generation != generation || len(s.streams) >= 128 {
		s.mu.Unlock()
		cancel()
		return nil, nil, errors.New("browser connection unavailable")
	}
	s.next++
	id := s.next
	s.streams[id] = browserStream{cancel: cancel, owner: sessionOwner(r)}
	c := s.config
	s.mu.Unlock()
	go func() {
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				if !s.authenticated(r, c) {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() { cancel(); s.mu.Lock(); delete(s.streams, id); s.mu.Unlock() }, nil
}
