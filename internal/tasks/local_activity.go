package tasks

import (
	"context"
	"fmt"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

// Match the same ownership facts as Record.local without loading model snapshots
// or run histories for activity reads. Historical targets remain authoritative.
const localExecutionFilter = `json_extract(body,'$.execution') IS NULL
 AND json_extract(body,'$.node_id')=?
 AND (json_extract(body,'$.info.execution_target') IS NULL OR
      json_extract(body,'$.info.execution_target.runtime_id')=json_extract(body,'$.node_id'))
 AND (json_extract(body,'$.launch') IS NULL OR (
      json_extract(body,'$.launch.control_id')=json_extract(body,'$.node_id') AND
      json_extract(body,'$.launch.runtime_id')=json_extract(body,'$.node_id') AND
      json_extract(body,'$.launch.execution_target.runtime_id')=json_extract(body,'$.node_id')))`

// LocalActive enumerates this Pot executor's work, including retired assistants.
// Linked submissions and historical remote launches cannot enter maintenance or
// allowance recovery as local work.
func (s *Service) LocalActive(ctx context.Context) ([]protocol.TaskInfo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT json_extract(body,'$.info') FROM tasks WHERE `+localExecutionFilter+` AND phase IN ('queued','waiting','running','waiting_approval') ORDER BY created,id`, s.options.NodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []protocol.TaskInfo{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var info protocol.TaskInfo
		if err := core.DecodeJSON([]byte(raw), &info); err != nil {
			return nil, err
		}
		items = append(items, info)
	}
	return items, rows.Err()
}

// Executing reports local owner-scoped execution without loading Task histories.
func (s *Service) Executing(ctx context.Context, assistant string) (bool, error) {
	if !core.Identifier(assistant) {
		return false, fmt.Errorf("invalid assistant identity")
	}
	var executing bool
	err := s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM tasks WHERE assistant_id=? AND `+localExecutionFilter+` AND phase='running'
  AND json_extract(body,'$.info.archived') IS NOT TRUE AND json_extract(body,'$.info.paused') IS NOT TRUE)`, assistant, s.options.NodeID).Scan(&executing)
	return executing, err
}

type Activity struct {
	AssistantID string
	Phase       protocol.TaskPhase
	Count       int64
}

// LocalActivity aggregates receiver-owned work. An unavailable original run is
// not an allowance wait, and a caller's receipt never represents local load.
func (s *Service) LocalActivity(ctx context.Context) ([]Activity, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT assistant_id,phase,COUNT(*) FROM tasks WHERE `+localExecutionFilter+` AND phase IN ('queued','running','waiting','waiting_approval')
  AND (phase!='waiting' OR json_extract(body,'$.info.remote_waiting') IS NOT TRUE) GROUP BY assistant_id,phase`, s.options.NodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Activity{}
	for rows.Next() {
		var item Activity
		if err := rows.Scan(&item.AssistantID, &item.Phase, &item.Count); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
