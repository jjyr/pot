package assistant

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

// CompressRolloutBatch examines at most 32 registry keys without materializing
// profiles or opening conversations. limit bounds successful publications. The
// returned cursor resumes after the last examined key; empty means end of scan.
func (r *Registry) CompressRolloutBatch(ctx context.Context, before time.Time, cursor string, limit int, bytesPerSecond int64) (next string, compressed int, err error) {
	if limit <= 0 {
		return cursor, 0, nil
	}
	if limit > 32 {
		limit = 32
	}
	rows, e := r.db.QueryContext(ctx, "SELECT id FROM assistant_definitions WHERE id>? ORDER BY id LIMIT 32", cursor)
	if e != nil {
		return cursor, 0, e
	}
	var ids []string
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return cursor, 0, e
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return cursor, 0, e
	}
	next = cursor
	for index, id := range ids {
		if e = ctx.Err(); e != nil {
			return next, compressed, errors.Join(err, e)
		}
		next = id
		if !core.Identifier(id) {
			err = errors.Join(err, errors.New("invalid assistant rollout registry identity"))
			continue
		}
		did, e := storage.CompressConversationContext(ctx, filepath.Join(r.Directory(id), "conversation.sqlite"), before, bytesPerSecond)
		if e != nil {
			err = errors.Join(err, fmt.Errorf("assistant %s rollout: %w", id, e))
		}
		if did {
			compressed++
		}
		if compressed >= limit {
			if index == len(ids)-1 && len(ids) < 32 {
				next = ""
			}
			return next, compressed, err
		}
	}
	if len(ids) < 32 {
		next = ""
	}
	return next, compressed, err
}

// Called only after the persisted turn has been cleared. The callback is a
// nonblocking maintenance signal, never the compression job itself.
func (c *Conversation) notifyTurnFinishedLocked() {
	if c.options.OnTurnFinished != nil {
		c.options.OnTurnFinished()
	}
}
