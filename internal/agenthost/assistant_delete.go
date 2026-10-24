package agenthost

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

func activeAssistantTx(ctx context.Context, tx *sql.Tx, id string) error {
	var deleted bool
	err := tx.QueryRowContext(ctx, "SELECT deleted FROM agent_adapter_assistants WHERE id=?", id).Scan(&deleted)
	if errors.Is(err, sql.ErrNoRows) || err == nil && deleted {
		return agentlink.ErrNotFound
	}
	return err
}

func (b *backend) DeleteAssistant(ctx context.Context, r agentlink.DeleteAssistantRequest) error {
	if !validID(r.AssistantID) || r.Revision == 0 || r.Revision > core.MaxSafeInteger {
		return agentlink.ErrAssistantChanged
	}
	return storage.Transaction(ctx, b.db, func(tx *sql.Tx) error {
		var revision uint64
		var deleted bool
		if err := tx.QueryRowContext(ctx, "SELECT revision,deleted FROM agent_adapter_assistants WHERE id=?", r.AssistantID).Scan(&revision, &deleted); err != nil {
			return err
		}
		if revision != r.Revision {
			return agentlink.ErrAssistantChanged
		}
		if deleted {
			return nil
		}
		var busy bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM agent_adapter_jobs WHERE assistant_id=? AND (json_extract(data,'$.task.state') NOT IN ('completed','failed','cancelled') OR json_extract(data,'$.cleanup_uncertain')=1)) OR EXISTS(SELECT 1 FROM agent_messages WHERE direction='incoming' AND state IN ('queued','read') AND json_extract(envelope,'$.to.agent_id')=?)`, r.AssistantID, r.AssistantID).Scan(&busy); err != nil {
			return err
		}
		if busy {
			return agentlink.ErrAssistantBusy
		}
		_, err := tx.ExecContext(ctx, "UPDATE agent_adapter_assistants SET deleted=1,is_default=0 WHERE id=? AND revision=?", r.AssistantID, r.Revision)
		return err
	})
}
