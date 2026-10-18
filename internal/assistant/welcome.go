package assistant

import (
	"database/sql"
	"encoding/json"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// initializeWelcome commits the transcript and model checkpoint with the initial
// conversation marker. It emits no unread notification and makes no model call.
func initializeWelcome(tx *sql.Tx, options ConversationOptions) error {
	message := protocol.Message{ID: storage.ID(), ConversationID: options.ConversationID, Role: protocol.Assistant, Text: options.InitialWelcome, Status: protocol.Complete, CreatedAtMS: storage.Now()}
	if err := message.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO messages VALUES (?,NULL,?)", message.ID, string(body)); err != nil {
		return err
	}
	event, err := json.Marshal(protocol.Event{EventID: storage.ID(), Type: "message", Message: &message})
	if err != nil {
		return err
	}
	if _, err = tx.Exec("INSERT INTO events(body) VALUES (?)", string(event)); err != nil {
		return err
	}
	welcome := modelprotocol.TextMessage("assistant", options.InitialWelcome)
	welcome.ID = message.ID
	checkpoint, err := json.Marshal(agent.Checkpoint{Type: "context", Version: 2, Messages: []modelprotocol.Message{welcome}, Pending: []modelprotocol.ToolCall{}, Receipts: []agent.ToolReceipt{}, SteeringIDs: []string{}})
	if err != nil {
		return err
	}
	_, err = tx.Exec("INSERT INTO metadata VALUES (?,?)", ContextKey, string(checkpoint))
	return err
}
