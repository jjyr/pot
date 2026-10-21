package inbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/protocol"
)

var ErrChatBinding = errors.New("chat input binding or projection differs")

// ReadChat checks the immutable input binding before reading its explicit
// replies. Mailbox storage names are private to the execution adapter.
func ReadChat(ctx context.Context, db *sql.DB, q protocol.ChatLookup, incoming, outgoing string) (protocol.Envelope, protocol.ChatStatus, error) {
	var parent protocol.Envelope
	result := protocol.ChatStatus{ChatReply: protocol.ChatReply{InputID: q.MessageID}, Messages: []protocol.Envelope{}}
	var raw, state string
	var detail sql.NullString
	err := db.QueryRowContext(ctx, "SELECT envelope,state,detail FROM agent_messages WHERE id=? AND direction=?", q.MessageID, incoming).Scan(&raw, &state, &detail)
	if err == sql.ErrNoRows {
		return parent, result, sql.ErrNoRows
	}
	if err != nil {
		return parent, result, err
	}
	if json.Unmarshal([]byte(raw), &parent) != nil || parent.Kind != "message" || parent.From.Kind != "user" || parent.From != q.From || parent.To.Kind != "assistant" || parent.To.AgentID != q.AssistantID {
		return parent, result, ErrChatBinding
	}
	switch state {
	case "queued", "read":
		result.State = "queued"
	case "processing":
		result.State = "running"
	case "waiting":
		result.State = "waiting"
	case "processed", "suppressed":
		result.State = "completed"
	case "failed":
		result.State = "failed"
	case "interrupted":
		result.State = "unknown"
	default:
		return parent, result, ErrChatBinding
	}
	if result.State == "failed" || result.State == "unknown" {
		result.Error = detail.String
	}
	rows, err := db.QueryContext(ctx, `SELECT envelope FROM agent_messages WHERE direction=? AND json_extract(envelope,'$.kind')='message' AND json_extract(envelope,'$.reply_to')=? ORDER BY sequence LIMIT ?`, outgoing, parent.ID, protocol.MailPageSize+1)
	if err != nil {
		return parent, result, err
	}
	defer rows.Close()
	for rows.Next() {
		var m protocol.Envelope
		if err := rows.Scan(&raw); err != nil {
			return parent, result, err
		}
		if json.Unmarshal([]byte(raw), &m) != nil || m.From != parent.To || m.To != parent.From || m.ConversationID != parent.ConversationID {
			return parent, result, ErrChatBinding
		}
		result.Messages = append(result.Messages, m)
	}
	if len(result.Messages) > protocol.MailPageSize {
		return parent, result, ErrChatBinding
	}
	return parent, result, rows.Err()
}
