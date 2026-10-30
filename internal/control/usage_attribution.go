package control

import (
	"context"
	"strings"
)

// Cost correlation follows the original request, not the lifetime of a reusable
// task. A later continuation has a new input and cannot be charged to its old ask.
func (s *Service) usageRequest(ctx context.Context, owner, turn string) (string, error) {
	if !strings.HasPrefix(turn, "pot-result-") {
		return turn, nil
	}
	// DeliveryMail intentionally contains correspondence only, not task results.
	rows, err := s.tasksDB.QueryContext(ctx, `SELECT json_extract(envelope,'$.result.task_id'),json_extract(envelope,'$.result.run_id') FROM agent_messages WHERE recipient=? AND id IN (SELECT message FROM agent_delivery_items WHERE batch=?) AND json_extract(envelope,'$.kind')='task_result'`, owner, strings.TrimPrefix(turn, "pot-result-"))
	if err != nil {
		return "", err
	}
	type resultRun struct{ task, run string }
	var results []resultRun
	for rows.Next() {
		var r resultRun
		if err = rows.Scan(&r.task, &r.run); err != nil {
			rows.Close()
			return "", err
		}
		results = append(results, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return "", err
	}
	for _, r := range results {
		record, err := s.Tasks.Run(ctx, owner, r.task, r.run)
		if err != nil {
			return "", err
		}
		if record == nil || record.RunInputID != "" {
			return "", nil
		}
	}
	return s.experimentRoot(ctx, owner, turn)
}
