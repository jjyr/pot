package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/storage"
)

type OperationRecord struct {
	Operation Operation `json:"operation"`
	Receipt   Receipt   `json:"receipt"`
}

func (o *Operations) RecoveryRecords(ctx context.Context, owner, turn string) ([]OperationRecord, error) {
	rows, err := o.db.QueryContext(ctx, "SELECT i.body,o.id,o.input_hash,o.state,o.result,o.error FROM operation_inputs i JOIN operations o ON o.id=i.id WHERE i.owner=? AND i.turn_id=? ORDER BY i.id", owner, turn)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := []OperationRecord{}
	size := 0
	for rows.Next() {
		var record OperationRecord
		var input, raw []byte
		if err = rows.Scan(&input, &record.Receipt.ID, &record.Receipt.InputHash, &record.Receipt.State, &raw, &record.Receipt.Error); err != nil {
			return nil, err
		}
		if err = core.DecodeJSON(input, &record.Operation); err != nil {
			return nil, err
		}
		hash, err := core.HashJSON(record.Operation)
		if err != nil || hash != record.Receipt.InputHash {
			return nil, errors.New("original operation input checksum mismatch")
		}
		record.Receipt.Result = json.RawMessage(raw)
		size += len(input) + len(raw)
		if size > 12*1024*1024 || len(records) >= 10000 {
			return nil, errors.New("original recovery evidence exceeds bounded inspection limit")
		}
		records = append(records, record)
	}
	return records, rows.Err()
}

// ReconcileKnown accepts only independently inspected original results. Product
// adapters never expose this method as a caller-supplied receipt/cleanup API.
func (o *Operations) ReconcileKnown(ctx context.Context, op Operation, value json.RawMessage, operationErr error) error {
	id, err := op.Key()
	if err != nil {
		return err
	}
	hash, err := core.HashJSON(op)
	if err != nil {
		return err
	}
	if len(value) == 0 {
		value = json.RawMessage("null")
	}
	if !json.Valid(value) || len(value) > 4*1024*1024 {
		return errors.New("invalid original operation result")
	}
	message := ""
	if operationErr != nil {
		message = operationErr.Error()
	}
	return storage.Transaction(ctx, o.db, func(tx *sql.Tx) error {
		var storedHash, state, storedError string
		var result []byte
		if err := tx.QueryRowContext(ctx, "SELECT input_hash,state,result,error FROM operations WHERE id=?", id).Scan(&storedHash, &state, &result, &storedError); err != nil {
			return err
		}
		if hash != storedHash {
			return errors.New("original operation identity changed")
		}
		if state == "complete" {
			a, _ := core.HashJSON(json.RawMessage(result))
			b, _ := core.HashJSON(value)
			if a != b || message != storedError {
				return errors.New("original receipt conflicts with completed operation")
			}
			return nil
		}
		if state != "started" && state != "unknown" {
			return errors.New("operation cannot be reconciled")
		}
		_, err := tx.ExecContext(ctx, "UPDATE operations SET state='complete',result=?,error=? WHERE id=?", []byte(value), message, id)
		return err
	})
}
