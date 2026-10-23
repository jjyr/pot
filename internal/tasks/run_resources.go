package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
)

func (j *Journal) PreparedResources(ctx context.Context, node, task, run string) (json.RawMessage, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	return j.resources.Read(node, task, run)
}
func (j *Journal) PinResources(ctx context.Context, node, task, run string, body json.RawMessage) (raw json.RawMessage, digest string, err error) {
	if !json.Valid(body) || len(body) > 24*1024*1024 {
		return nil, "", fmt.Errorf("invalid prepared resource evidence")
	}
	err = j.changeTx(ctx, node, task, run, func(_ *sql.Tx, value *Run) error {
		if value.Phase != Running || value.BootID != j.BootID || value.StopRequested != "" || value.Finalizing {
			return fmt.Errorf("resource preparation requires current original run")
		}
		var e error
		raw, digest, e = j.resources.Pin(node, task, run, body)
		return e
	})
	return
}
