package tasks

import (
	"context"
	"fmt"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

func (s *Service) initializeOutcomes() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS task_outcomes(
 sequence INTEGER PRIMARY KEY AUTOINCREMENT CHECK(sequence<=9007199254740991),
 assistant_id TEXT NOT NULL,task_id TEXT NOT NULL,title TEXT NOT NULL,
 phase TEXT NOT NULL,updated_at_ms INTEGER NOT NULL);
 CREATE INDEX IF NOT EXISTS task_outcomes_owner_sequence ON task_outcomes(assistant_id,sequence);`)
	if err != nil {
		return err
	}
	return s.db.QueryRow("SELECT COALESCE(MAX(sequence),0) FROM task_outcomes").Scan(&s.outcomeCursor)
}

// Outcomes reads a bounded, owner-scoped projection of committed terminal
// transitions. A nil cursor establishes a baseline without reading history.
// It has no delivery/acknowledgment state and is independent of Agent reporting.
func (s *Service) Outcomes(ctx context.Context, owner string, after *uint64) (protocol.TaskOutcomePage, error) {
	page := protocol.TaskOutcomePage{Items: []protocol.TaskOutcome{}}
	if !core.Identifier(owner) || after != nil && *after > core.MaxSafeInteger {
		return page, fmt.Errorf("invalid Task outcome cursor/owner")
	}
	if after == nil {
		err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0) FROM task_outcomes WHERE assistant_id=?", owner).Scan(&page.Cursor)
		return page, err
	}
	page.Cursor = *after
	rows, err := s.db.QueryContext(ctx, `SELECT sequence,task_id,title,phase,updated_at_ms FROM task_outcomes
 WHERE assistant_id=? AND sequence>? ORDER BY sequence LIMIT 101`, owner, *after)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		var item protocol.TaskOutcome
		if err := rows.Scan(&item.Sequence, &item.ID, &item.Title, &item.Phase, &item.UpdatedAtMS); err != nil {
			return page, err
		}
		if len(page.Items) == 100 {
			page.HasMore = true
			break
		}
		page.Items = append(page.Items, item)
		page.Cursor = item.Sequence
	}
	return page, rows.Err()
}
