package protocol

import "github.com/jjyr/pot/pkg/core"

// ChatReply describes execution for one accepted input. Transport acceptance and
// connection health are independent of this lifecycle. Unknown never means failed.
type ChatReply struct {
	InputID   string `json:"input_id"`
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
	Uncertain bool   `json:"uncertain,omitempty"`
}

func (r ChatReply) Terminal() bool {
	return r.State == "completed" || r.State == "failed" || r.State == "cancelled" || r.State == "unknown"
}

func (r ChatReply) Valid() bool {
	if !core.Identifier(r.InputID) || len(r.Error) > 65536 {
		return false
	}
	switch r.State {
	case "pending", "queued", "running", "waiting", "paused", "cancelling", "completed", "failed", "cancelled", "unknown":
		return true
	}
	return false
}

// ProjectChatReplies adapts native conversation facts to the shared contract.
// Completed model messages are intermediate output while their turn is active.
func ProjectChatReplies(messages []Message, turn *TurnState) []ChatReply {
	rows := []ChatReply{}
	indices := map[string]int{}
	parents := map[string]string{}
	input := ""
	for _, m := range messages {
		if (m.Role == User && !m.Steering) || (m.Role == System && m.Compaction == nil) {
			input = m.ID
			state := "completed"
			if m.Queued {
				state = "queued"
			}
			indices[input] = len(rows)
			rows = append(rows, ChatReply{InputID: input, State: state})
		}
		if m.Role != Assistant {
			continue
		}
		parent := m.ReplyTo
		if parent == "" {
			parent = input
		}
		parents[m.ID] = parent
	}
	for _, m := range messages {
		if m.Role != Assistant {
			continue
		}
		parent := parents[m.ID]
		if i, ok := indices[parent]; ok {
			switch m.Status {
			case Complete:
				rows[i].State, rows[i].Error = "completed", ""
			case Failed:
				rows[i].State, rows[i].Error = "failed", m.Error
			case MessageCancelled:
				rows[i].State = "cancelled"
			}
		}
	}
	if turn != nil {
		state := "running"
		if turn.Cancelling {
			state = "cancelling"
		} else if turn.Paused {
			state = "paused"
		} else if turn.State == "waiting" {
			state = "waiting"
		}
		r := ChatReply{InputID: turn.UserMessageID, State: state}
		if i, ok := indices[r.InputID]; ok {
			rows[i] = r
		} else {
			rows = append(rows, r)
		}
	}
	return rows
}

// ChatLookup identifies a caller-owned input at the receiving execution adapter.
type ChatLookup struct {
	From        AgentRef `json:"from"`
	AssistantID string   `json:"assistant_id"`
	MessageID   string   `json:"message_id"`
}
type ChatStatus struct {
	ChatReply
	Messages []Envelope `json:"messages"`
}

// SendReceiptQuery identifies an acceptance, not whether the Agent consumed it.
// The owning Control and assistant are selected by the authenticated transport.
type SendReceiptQuery struct {
	RequestID          string `json:"request_id"`
	ConversationID     string `json:"conversation_id"`
	UserMessageID      string `json:"user_message_id,omitempty"`
	RetryUserMessageID string `json:"retry_user_message_id,omitempty"`
}

type SendReceipt struct {
	SendReceiptQuery
	RuntimeID string `json:"runtime_id"`
	OwnerID   string `json:"owner_id"`
	State     string `json:"state"`
	MessageID string `json:"message_id,omitempty"`
}
