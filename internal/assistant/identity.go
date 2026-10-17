package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

// RenameOnce binds a chat tool call to its owner and name. Retrying a completed
// call returns its receipt, even after a later settings edit.
func (r *Registry) RenameOnce(ctx context.Context, owner, name, request string) (Definition, error) {
	if err := ValidateName(name); err != nil {
		return Definition{}, err
	}
	hash, err := core.HashJSON([]string{owner, name})
	if err != nil {
		return Definition{}, err
	}
	var definition Definition
	err = storage.Transaction(ctx, r.db, func(tx *sql.Tx) error {
		var stored, response string
		err := tx.QueryRowContext(ctx, "SELECT input_hash,response FROM assistant_rename_requests WHERE id=?", request).Scan(&stored, &response)
		if err == nil {
			if stored != hash {
				return errors.New("conflicting assistant rename request")
			}
			return core.DecodeJSON([]byte(response), &definition)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err = tx.QueryRowContext(ctx, "SELECT id,name,revision,request_id FROM assistant_definitions WHERE id=?", owner).Scan(&definition.ID, &definition.Name, &definition.Revision, &definition.RequestID); err != nil {
			return err
		}
		if definition.Revision >= core.MaxSafeInteger {
			return errors.New("assistant revision exhausted")
		}
		definition.Name = name
		definition.Revision++
		if _, err = tx.ExecContext(ctx, "UPDATE assistant_definitions SET name=?,revision=? WHERE id=?", name, definition.Revision, owner); err != nil {
			return err
		}
		body, err := json.Marshal(definition)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO assistant_rename_requests VALUES (?,?,?)", request, hash, string(body))
		return err
	})
	return definition, err
}
