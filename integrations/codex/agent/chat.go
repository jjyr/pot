package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/agentlink"
)

func (b *backend) ChatStatus(ctx context.Context, query agentlink.ChatLookup) (agentlink.ChatStatus, error) {
	parent, result, err := inbox.ReadChat(ctx, b.db, query, "incoming", "outgoing")
	if errors.Is(err, sql.ErrNoRows) {
		err = agentlink.ErrNotFound
	}
	if errors.Is(err, inbox.ErrChatBinding) {
		err = agentlink.ErrUnauthorized
	}
	if err != nil {
		return result, err
	}
	var raw string
	err = b.db.QueryRowContext(ctx, `SELECT data FROM codex_link_jobs WHERE json_extract(data,'$.message.id')=?`, query.MessageID).Scan(&raw)
	if err == sql.ErrNoRows {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	var j job
	if json.Unmarshal([]byte(raw), &j) != nil || j.Message == nil || j.Message.From != query.From || j.Task.Assistant.Address != parent.To {
		return result, agentlink.ErrConflict
	}
	result.State, result.Error = j.Task.State, j.Task.Error
	if j.CleanupUncertain {
		result.State = "unknown"
	}
	if result.State == "waiting_approval" {
		result.State = "running"
	}
	if !result.ChatReply.Valid() {
		return result, agentlink.ErrConflict
	}
	// The durable job outcome is authoritative even before the outbox publisher
	// gets its next wakeup. This is the identical immutable envelope it publishes.
	if reply := messageReply(j); reply != nil && result.Terminal() {
		found := false
		for _, m := range result.Messages {
			if m.ID == reply.ID {
				found = true
			}
		}
		if !found {
			result.Messages = append(result.Messages, *reply)
		}
	}
	return result, nil
}
