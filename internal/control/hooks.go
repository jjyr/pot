package control

import "github.com/jjyr/pot/internal/hooks"

// Producers commit authoritative state first. UI hooks only invalidate current
// projections, including the existing approval cards and user reminders. Task
// hooks have already committed their Agent inbox entry in the same transaction.
func (s *Service) publishHook(event hooks.Event) {
	for _, topic := range event.Topics() {
		s.Changes.Notify(topic)
	}
}
