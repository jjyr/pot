package assistant

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/pkg/agent"
)

// Diagnostics exposes bounded execution metadata; model/tool payloads and error
// text are excluded even when the internal observation store contains them.
func (c *Conversation) Diagnostics(ctx context.Context) (map[string]any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rows, err := c.store.DB().QueryContext(ctx, "SELECT sequence,turn_id,generation,body FROM assistant_observations ORDER BY sequence DESC LIMIT 200")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []map[string]any{}
	for rows.Next() {
		var seq, generation uint64
		var turn string
		var raw []byte
		if err = rows.Scan(&seq, &turn, &generation, &raw); err != nil {
			return nil, err
		}
		var o agent.Observation
		if err = json.Unmarshal(raw, &o); err != nil {
			return nil, err
		}
		entries = append(entries, map[string]any{"sequence": seq, "turn_id": turn, "generation": generation, "kind": o.Kind, "id": o.ID, "stage": o.Stage, "compaction_id": o.CompactionID, "tokens": o.Tokens, "threshold": o.Threshold})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	var control any
	if c.turn != nil {
		control = map[string]any{"user_message_id": c.turn.UserMessageID, "paused": c.turn.Paused}
	}
	return map[string]any{"owner": c.options.Owner, "entries": entries, "control": control}, nil
}
