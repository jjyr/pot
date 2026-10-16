package control

import (
	"context"
	"github.com/jjyr/pot/internal/protocol"
)

func (s *Service) Notifications(ctx context.Context, after map[string]int64, initialize bool) (protocol.AssistantNotifications, error) {
	result := protocol.AssistantNotifications{Cursors: map[string]int64{}, Messages: []protocol.AssistantNotification{}}
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil {
		return result, err
	}
	for _, d := range definitions {
		c, err := s.Conversation(ctx, d.ID)
		if err != nil {
			return result, err
		}
		cursor, values, err := c.Notifications(ctx, after[d.ID], initialize)
		if err != nil {
			return result, err
		}
		result.Cursors[d.ID] = cursor
		for _, v := range values {
			result.Messages = append(result.Messages, protocol.AssistantNotification{AssistantID: d.ID, Sequence: v.Sequence, Message: v.Message})
		}
	}
	return result, nil
}
