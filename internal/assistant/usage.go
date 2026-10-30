package assistant

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// CommunicationUsage describes durable logical messages, not transport attempts.
// Counts include each local envelope once, including local-to-local delivery.
type CommunicationUsage struct {
	EstimatesKnown         bool                  `json:"estimates_known"`
	Count                  int64                 `json:"count"`
	PayloadBytes           int64                 `json:"payload_bytes"`
	ContentEstimatedTokens int64                 `json:"content_estimated_tokens"`
	Items                  []CommunicationRecord `json:"items"`
	Offset                 int                   `json:"offset"`
	NextOffset             *int                  `json:"next_offset"`
}
type CommunicationRecord struct {
	ID                     string            `json:"id"`
	Kind                   string            `json:"kind"`
	From                   protocol.AgentRef `json:"from"`
	To                     protocol.AgentRef `json:"to"`
	State                  string            `json:"state"`
	At                     int64             `json:"at"`
	PayloadBytes           int64             `json:"payload_bytes"`
	ContentEstimatedTokens *int64            `json:"content_estimated_tokens"`
	EstimateMethod         *string           `json:"estimate_method"`
	Direction              string            `json:"direction"`
}

// CommunicationUsage queries accepted envelopes. The caller validates range and
// pagination. No prompt or message body is exposed by this projection.
func (m *Mailbox) CommunicationUsage(ctx context.Context, scope string, start, stop int64, offset int) (CommunicationUsage, error) {
	result := CommunicationUsage{Items: []CommunicationRecord{}, Offset: offset}
	if offset < 0 || offset > 1000000 || len(scope) > 128 || start < 0 || stop < start {
		return result, fmt.Errorf("invalid communication usage query")
	}
	const filter = ` FROM agent_messages m WHERE m.accepted_at_ms>=? AND m.accepted_at_ms<? AND
 (?='' OR (json_extract(m.envelope,'$.from.node_id')=? AND json_extract(m.envelope,'$.from.agent_id')=?) OR
 (json_extract(m.envelope,'$.to.node_id')=? AND json_extract(m.envelope,'$.to.agent_id')=?) OR
 (json_extract(m.envelope,'$.from.node_id')=? AND json_extract(m.envelope,'$.result.task_id')=?))`
	args := []any{start, stop, scope, m.nodeID, scope, m.nodeID, scope, m.nodeID, scope}
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*),COALESCE(SUM(length(CAST(envelope AS BLOB))),0),COALESCE(SUM(content_estimated_tokens),0),COALESCE(MIN(content_estimated_tokens IS NOT NULL),0)`+filter, args...).Scan(&result.Count, &result.PayloadBytes, &result.ContentEstimatedTokens, &result.EstimatesKnown); err != nil {
			return err
		}
		pageArgs := append(append([]any{}, args...), offset)
		rows, err := tx.QueryContext(ctx, `SELECT m.id,json_extract(envelope,'$.kind'),
 json_extract(envelope,'$.from.node_id'),json_extract(envelope,'$.from.kind'),json_extract(envelope,'$.from.agent_id'),
 json_extract(envelope,'$.to.node_id'),json_extract(envelope,'$.to.kind'),json_extract(envelope,'$.to.agent_id'),
 state,accepted_at_ms,length(CAST(envelope AS BLOB)),content_estimated_tokens,content_estimate_method,direction`+filter+` ORDER BY accepted_at_ms DESC,m.id DESC LIMIT 21 OFFSET ?`, pageArgs...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var r CommunicationRecord
			if err := rows.Scan(&r.ID, &r.Kind, &r.From.NodeID, &r.From.Kind, &r.From.AgentID, &r.To.NodeID, &r.To.Kind, &r.To.AgentID, &r.State, &r.At, &r.PayloadBytes, &r.ContentEstimatedTokens, &r.EstimateMethod, &r.Direction); err != nil {
				return err
			}
			result.Items = append(result.Items, r)
		}
		return rows.Err()
	})
	if len(result.Items) > 20 {
		n := offset + 20
		result.NextOffset = &n
		result.Items = result.Items[:20]
	}
	return result, err
}
