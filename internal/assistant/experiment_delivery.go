package assistant

import "context"

type ExperimentDelivery struct{ State, Turn string }

// ExperimentDeliveries follows existing inbox membership, not new provenance.
func (m *Mailbox) ExperimentDeliveries(ctx context.Context, owner, task string, since int64) ([]ExperimentDelivery, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT state,COALESCE((SELECT 'pot-result-'||batch FROM agent_delivery_items WHERE message=m.id),'') FROM agent_messages m WHERE recipient=? AND direction='in' AND accepted_at_ms>=? AND json_extract(envelope,'$.to.node_id')=? AND ((json_extract(envelope,'$.kind')='task_result' AND json_extract(envelope,'$.result.task_id')=?) OR (json_extract(envelope,'$.from.kind')='task' AND json_extract(envelope,'$.from.agent_id')=?))`, owner, since, m.nodeID, task, task)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExperimentDelivery{}
	for rows.Next() {
		var v ExperimentDelivery
		if err := rows.Scan(&v.State, &v.Turn); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
