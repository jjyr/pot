package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/storage"
)

type InstructionUpdate struct {
	ID     string
	Text   string
	Before Rules
	Result *Rules
}

// PrepareInstructionUpdate fixes the reviewed revision before an asynchronous
// owner decision. No live rule changes and no database lock survives this call.
func (c *Conversation) PrepareInstructionUpdate(ctx context.Context, id, text string) (InstructionUpdate, error) {
	var result InstructionUpdate
	if !core.Identifier(id) || len(text) > 262144 {
		return result, errors.New("invalid instruction update")
	}
	if _, err := c.store.DB().ExecContext(ctx, `CREATE TABLE IF NOT EXISTS assistant_instruction_updates(id TEXT PRIMARY KEY,text TEXT NOT NULL,before_rules TEXT NOT NULL,result TEXT)`); err != nil {
		return result, err
	}
	before, err := c.Instructions(ctx)
	if err != nil {
		return result, err
	}
	data, _ := json.Marshal(before)
	err = storage.Transaction(ctx, c.store.DB(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO assistant_instruction_updates VALUES(?,?,?,NULL)", id, text, string(data))
		if err != nil {
			return err
		}
		var original string
		var raw sql.NullString
		if err = tx.QueryRowContext(ctx, "SELECT text,before_rules,result FROM assistant_instruction_updates WHERE id=?", id).Scan(&original, &data, &raw); err != nil {
			return err
		}
		if original != text {
			return errors.New("instruction request identity reused")
		}
		result = InstructionUpdate{ID: id, Text: text}
		if err = core.DecodeJSON(data, &result.Before); err != nil {
			return err
		}
		if raw.Valid {
			var rules Rules
			if err = core.DecodeJSON([]byte(raw.String), &rules); err != nil {
				return err
			}
			result.Result = &rules
		}
		return nil
	})
	return result, err
}
func (c *Conversation) CommitInstructionUpdate(ctx context.Context, id string) (Rules, error) {
	var result Rules
	err := storage.Transaction(ctx, c.store.DB(), func(tx *sql.Tx) error {
		var text, beforeRaw string
		var saved sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT text,before_rules,result FROM assistant_instruction_updates WHERE id=?", id).Scan(&text, &beforeRaw, &saved); err != nil {
			return err
		}
		if saved.Valid {
			return core.DecodeJSON([]byte(saved.String), &result)
		}
		var before, current Rules
		if err := core.DecodeJSON([]byte(beforeRaw), &before); err != nil {
			return err
		}
		var currentRaw string
		if err := tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='assistant.instructions.v1'").Scan(&currentRaw); err != nil {
			return err
		}
		if err := core.DecodeJSON([]byte(currentRaw), &current); err != nil {
			return err
		}
		if current.Revision != before.Revision {
			return errors.New("assistant instructions changed after review; read and propose a new request")
		}
		result = Rules{SchemaVersion: 1, Text: text, Revision: storage.ID()}
		data, _ := json.Marshal(result)
		if _, err := tx.ExecContext(ctx, "UPDATE metadata SET value=? WHERE key='assistant.instructions.v1'", string(data)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE assistant_instruction_updates SET result=? WHERE id=?", string(data), id)
		return err
	})
	return result, err
}
