// Package inbox owns the durable message envelope shared by Task inputs,
// assistant correspondence and Runtime lifecycle events.
package inbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/pkg/protocol"
)

const MaxBatchMessages = 8
const MaxBatchBytes = 262144

func Initialize(db *sql.DB) error {
	var legacy bool
	if err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name IN ('assistant_mail','task_inputs','task_notifications'))").Scan(&legacy); err != nil {
		return err
	}
	if legacy {
		return errors.New("unsupported legacy agent message storage")
	}

	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS agent_messages(
 sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,
 envelope TEXT NOT NULL,direction TEXT NOT NULL,state TEXT NOT NULL,
 conversation TEXT NOT NULL,recipient TEXT NOT NULL,detail TEXT);
 CREATE INDEX IF NOT EXISTS agent_messages_recipient ON agent_messages(recipient,direction,state,sequence);
 CREATE INDEX IF NOT EXISTS agent_messages_context ON agent_messages(conversation,recipient,state);
 CREATE TABLE IF NOT EXISTS agent_result_observations(owner TEXT NOT NULL,turn TEXT NOT NULL,message TEXT NOT NULL,PRIMARY KEY(owner,turn,message));
 CREATE TABLE IF NOT EXISTS agent_delivery_items(batch TEXT NOT NULL,message TEXT NOT NULL UNIQUE,PRIMARY KEY(batch,message));`)
	return err
}

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
	_, err = tx.ExecContext(ctx, "INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient) VALUES(?,?,?,?,?,?)", message.ID, string(body), direction, state, message.ConversationID, message.To.AgentID)
	return err
}
