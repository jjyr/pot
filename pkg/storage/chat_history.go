package storage

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strconv"

	"github.com/jjyr/pot/pkg/protocol"
)

const UIPageSize = 80
const UIMaxPageSize = 120
const UIMaxChanges = 256

// UIWindow reads insertion order, independently of timestamps and mutable message
// bodies. Message upserts preserve rowid. A Store belongs to one conversation.
func (s *Store) UIWindow(ctx context.Context, before, after int64, around string, limit int) ([]protocol.UIMessage, protocol.UIHistory, error) {
	var meta protocol.UIHistory
	if limit <= 0 || limit > UIMaxPageSize {
		limit = UIPageSize
	}
	if around != "" {
		var sequence int64
		if err := s.db.QueryRowContext(ctx, "SELECT rowid FROM messages WHERE id=?", around).Scan(&sequence); err != nil {
			return nil, meta, err
		}
		// Include the target and a small amount of surrounding context, without
		// reading the intervening history from the newest page.
		before = sequence + int64(limit/2) + 1
	}
	query := "SELECT rowid,body FROM messages"
	args := []any{}
	switch {
	case before > 0:
		query += " WHERE rowid<?"
		args = append(args, before)
	case after > 0:
		query += " WHERE rowid>?"
		args = append(args, after)
	}
	if after > 0 {
		query += " ORDER BY rowid LIMIT ?"
	} else {
		query += " ORDER BY rowid DESC LIMIT ?"
	}
	args = append(args, limit)
	messages, err := s.uiMessages(ctx, query, args...)
	if err != nil {
		return nil, meta, err
	}
	if after == 0 {
		slices.Reverse(messages)
	}
	if len(messages) > 0 {
		meta.Before = messages[0].Sequence
		meta.After = messages[len(messages)-1].Sequence
		if err = s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM messages WHERE rowid<?),EXISTS(SELECT 1 FROM messages WHERE rowid>?)", meta.Before, meta.After).Scan(&meta.HasMore, &meta.HasNewer); err != nil {
			return nil, meta, err
		}
	}
	return messages, meta, nil
}

func (s *Store) uiMessages(ctx context.Context, query string, args ...any) ([]protocol.UIMessage, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.UIMessage{}
	for rows.Next() {
		var sequence int64
		var body []byte
		var message protocol.Message
		if err = rows.Scan(&sequence, &body); err != nil {
			return nil, err
		}
		if err = protocol.DecodeStrict(body, &message); err != nil {
			return nil, err
		}
		out = append(out, protocol.UIMessage{Message: message, Sequence: strconv.FormatInt(sequence, 10)})
	}
	return out, rows.Err()
}

func (s *Store) UIMessage(ctx context.Context, id string) (*protocol.UIMessage, error) {
	messages, err := s.uiMessages(ctx, "SELECT rowid,body FROM messages WHERE id=?", id)
	if err != nil || len(messages) == 0 {
		return nil, err
	}
	return &messages[0], nil
}

// UIChanges coalesces repeated token writes to the same message in SQL. Callers
// cap the journal cursor range before reading; no history-sized Go slice is made.
func (s *Store) UIChanges(ctx context.Context, after, through int64) ([]protocol.UIMessage, error) {
	if through < after || through-after > UIMaxChanges {
		return nil, errors.New("UI change window exceeded")
	}
	return s.uiMessages(ctx, `SELECT rowid,body FROM messages WHERE id IN
(SELECT json_extract(body,'$.message.id') FROM events WHERE cursor>? AND cursor<=?) ORDER BY rowid`, after, through)
}

func IsMissingUIMessage(err error) bool { return errors.Is(err, sql.ErrNoRows) }
