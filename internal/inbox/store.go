// Package inbox owns the durable message envelope shared by Task inputs,
// assistant correspondence and Runtime lifecycle events.
package inbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

const MaxBatchMessages = 8
const MaxBatchBytes = 262144

// EnqueueTx lets producers commit their state change and its message atomically.
// Retrying the same envelope is idempotent; conflicting content is rejected.
func EnqueueTx(ctx context.Context, tx *sql.Tx, message protocol.Envelope, direction, state string) error {
	if err := message.Validate(); err != nil {
		return err
	}
	body, err := json.Marshal(message)
	if err != nil {
		return err
	}
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT envelope FROM agent_messages WHERE id=?", message.ID).Scan(&previous)
	if err == nil {
		if previous != string(body) {
			return errors.New("message identity has different content")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient,accepted_at_ms) VALUES(?,?,?,?,?,?,?)", message.ID, string(body), direction, state, message.ConversationID, message.To.AgentID, time.Now().UnixMilli())
	return err
}
