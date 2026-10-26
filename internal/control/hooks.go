package control

import (
	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/pkg/protocol"
)

// Producers commit authoritative state first, then publish full business facts
// independently of UI invalidations. Task inbox entries already committed in
// the same terminal transaction; delivery mechanisms are not known here.
func (s *Service) publishHook(event hooks.Event) {
	fact := protocol.LifecycleEvent{ID: event.ID, Kind: string(event.Kind), Owner: event.Owner, Task: event.TaskID}
	if event.Result != nil {
		fact.Name = event.Result.Title
		fact.Suppressed = event.Result.SuppressionReason != ""
	}
	s.Lifecycle.Publish(fact)
	for _, topic := range event.Topics() {
		s.Changes.Notify(topic)
	}
}

func (s *Service) publishReply(owner string, m protocol.Message) {

	s.Lifecycle.Publish(protocol.LifecycleEvent{ID: m.ID, Kind: "reply_completed", Owner: owner, Preview: m.PreviewText()})
}
