package assistant

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/storage"
)

func (c *Conversation) Notifications(ctx context.Context, after int64, initialize bool) (int64, []storage.Notification, error) {
	if after < 0 || uint64(after) > core.MaxSafeInteger {
		return 0, nil, errors.New("invalid notification cursor")
	}
	if initialize {
		sequence, err := c.store.NotificationSequence(ctx)
		return sequence, []storage.Notification{}, err
	}
	values, err := c.store.Notifications(ctx, after)
	if err != nil {
		return 0, nil, err
	}
	cursor := after
	for _, value := range values {
		cursor = max(cursor, value.Sequence)
	}
	return cursor, values, nil
}
