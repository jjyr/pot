package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
)

type RetiredOperation struct {
	CallID        string
	OperationHash string
	Result        json.RawMessage
	Error         string
	Known         bool
}

// ReadRetiredOperations is the frozen decoder for pre-local execution receipts.
// It cannot create, complete or replay an operation.
func ReadRetiredOperations(ctx context.Context, db *sql.DB, node, task, run string) ([]RetiredOperation, error) {
	rows, err := db.QueryContext(ctx, "SELECT command_id,digest,phase,reply,input FROM retired_execution_receipts WHERE control_id=? AND task_id=? AND run_id=?", node, task, run)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []RetiredOperation{}
	size := 0
	for rows.Next() {
		var v RetiredOperation
		var digest, phase string
		var reply, input []byte
		if err = rows.Scan(&v.CallID, &digest, &phase, &reply, &input); err != nil {
			return nil, err
		}
		size += len(reply) + len(input)
		if size > 12*1024*1024 || len(result) >= 10000 {
			return nil, errors.New("archived execution evidence exceeds inspection limit")
		}
		hash, e := core.HashJSON(json.RawMessage(input))
		if e != nil || hash != digest {
			return nil, errors.New("archived execution input checksum mismatch")
		}
		var original struct {
			Operation json.RawMessage `json:"operation"`
			Request   struct {
				ID string `json:"id"`
			} `json:"request"`
		}
		if err = json.Unmarshal(input, &original); err != nil || original.Request.ID != v.CallID {
			return nil, errors.New("archived execution identity mismatch")
		}
		v.OperationHash, err = core.HashJSON(original.Operation)
		if err != nil {
			return nil, err
		}
		if phase == "complete" {
			var value struct {
				Value              json.RawMessage `json:"value"`
				Error              string          `json:"error"`
				CleanupUnconfirmed bool            `json:"cleanup_unconfirmed"`
			}
			if err = json.Unmarshal(reply, &value); err != nil || !json.Valid(value.Value) || value.CleanupUnconfirmed {
				return nil, errors.New("archived execution result unavailable")
			}
			v.Known, v.Result, v.Error = true, value.Value, value.Error
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
