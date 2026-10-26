package systemnotifications

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/internal/localui"
	"github.com/jjyr/pot/pkg/protocol"
	"time"
)

type Owner = hooks.Owner

// Consume transports full committed facts. It never reads business resources,
// compares catalogue/cursor state, or reconstructs events from invalidations.
func (h *Hub) Consume(ctx context.Context, caller localui.Caller) {
	for ctx.Err() == nil {
		cycle := rand.Text()
		_ = caller.Stream(ctx, "/api/v1/lifecycle", func(frame localui.Event) error {
			switch frame.Type {
			case "authority":
				var authority hooks.Authority
				if err := json.Unmarshal(frame.Data, &authority); err != nil {
					return err
				}
				h.mu.Lock()
				h.control = authority.Control
				h.sources = authority.Sources
				h.owners = map[string]Owner{}
				for _, o := range authority.Owners {
					o.Boot += "/" + cycle
					h.owners[o.ID] = o
				}
				h.signalLocked()
				h.mu.Unlock()
			case "lifecycle":
				var fact protocol.LifecycleEvent
				if err := json.Unmarshal(frame.Data, &fact); err != nil {
					return err
				}
				// Retain this received fact through a failed log commit. This does
				// not reread business state or replay a prior stream lifetime.
				for {
					if err := h.Accept(ctx, fact); err == nil {
						return nil
					}
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(time.Second):
					}
				}
			}
			return nil
		})
		h.mu.Lock()
		h.control = ""
		h.owners = map[string]Owner{}
		h.signalLocked()
		h.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
}
