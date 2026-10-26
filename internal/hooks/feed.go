package hooks

import (
	"github.com/jjyr/pot/pkg/protocol"
	"sync"
)

// Feed broadcasts committed facts independently. It is process-local, has no
// replay, and disconnects slow subscribers rather than silently losing events.
// Notification receipts, not this feed, own persistent delivery state.
type Feed struct {
	mu          sync.Mutex
	subscribers map[chan protocol.LifecycleEvent]struct{}
}

func (f *Feed) Subscribe() (<-chan protocol.LifecycleEvent, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subscribers == nil {
		f.subscribers = map[chan protocol.LifecycleEvent]struct{}{}
	}
	ch := make(chan protocol.LifecycleEvent, 128)
	f.subscribers[ch] = struct{}{}
	return ch, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if _, ok := f.subscribers[ch]; ok {
			delete(f.subscribers, ch)
			close(ch)
		}
	}
}
func (f *Feed) Publish(e protocol.LifecycleEvent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for ch := range f.subscribers {
		select {
		case ch <- e:
		default:
			delete(f.subscribers, ch)
			close(ch)
		}
	}
}

// Authority accompanies the authenticated stream, not retained business data.
type Owner struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Boot    string   `json:"boot"`
	Control string   `json:"control"`
	Space   string   `json:"space"`
	Kinds   []string `json:"kinds,omitempty"`
}
type Authority struct {
	Control string            `json:"control"`
	Boot    string            `json:"boot"`
	Owners  []Owner           `json:"owners"`
	Sources map[string]string `json:"sources,omitempty"`
}
