package inbox

import (
	"context"
	"database/sql"
)

// OutboxStats aggregates the complete immutable route before any delivery page
// bound. A legacy message with no insertion timestamp makes oldest unknown.
func OutboxStats(ctx context.Context, db *sql.DB, direction, local, peer string) (count, oldest int64, err error) {
	query := `SELECT COUNT(*),COALESCE(MIN(accepted_at_ms),0) FROM agent_messages WHERE direction=? AND state='queued' AND json_extract(envelope,'$.kind')='message' AND json_extract(envelope,'$.from.node_id')=?`
	arguments := []any{direction, local}
	if peer != "" {
		query += ` AND json_extract(envelope,'$.to.node_id')=?`
		arguments = append(arguments, peer)
	}
	err = db.QueryRowContext(ctx, query, arguments...).Scan(&count, &oldest)
	return
}
