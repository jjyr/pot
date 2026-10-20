package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/storage"
)

// RecordCompaction retains private request-local evidence, never credentials or
// a second usage total. Every attempt remains linked through the normal ledger.
func (l *Ledger) RecordCompaction(ctx context.Context, scope, turn, id string, body json.RawMessage) error {
	if !json.Valid(body) {
		return errors.New("invalid compaction evidence")
	}
	result, err := l.db.ExecContext(ctx, `INSERT INTO compaction_evidence(compaction_id,at,body) SELECT id,?,? FROM compactions WHERE id=? AND scope=? AND turn_id=?`, storage.Now(), string(body), id, scope, turn)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n != 1 {
		return errors.New("compaction owner mismatch")
	}
	return err
}
func (l *Ledger) CompactionRecord(ctx context.Context, scope, id string) (map[string]any, error) {
	var turn, reason, state string
	var at int64
	var ended, before, after *int64
	if err := l.db.QueryRowContext(ctx, "SELECT turn_id,reason,state,at,end_at,tokens_before,tokens_after FROM compactions WHERE id=? AND scope=?", id, scope).Scan(&turn, &reason, &state, &at, &ended, &before, &after); err != nil {
		return nil, err
	}
	evidence := []json.RawMessage{}
	rows, err := l.db.QueryContext(ctx, "SELECT body FROM compaction_evidence WHERE compaction_id=? ORDER BY sequence", id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var body string
		if err = rows.Scan(&body); err != nil {
			rows.Close()
			return nil, err
		}
		evidence = append(evidence, json.RawMessage(body))
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	attempts := []map[string]any{}
	rows, err = l.db.QueryContext(ctx, "SELECT id,model,state,input,output,cache,original FROM requests WHERE compaction_id=? AND scope=? ORDER BY at,id", id, scope)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var request, model, status, original string
		var input, output *int64
		var cache int64
		if err = rows.Scan(&request, &model, &status, &input, &output, &cache, &original); err != nil {
			return nil, err
		}
		metrics := compactionMetrics(evidence, request)
		var uncached, hitRate any
		if metrics["cache_read_tokens"] != nil && input != nil {
			cached := metrics["cache_read_tokens"].(float64)
			uncached = max(0, float64(*input)-cached)
			if *input > 0 {
				hitRate = cached / float64(*input)
			}
		}
		attempts = append(attempts, map[string]any{"usage_details": metrics, "uncached_input": uncached, "cache_hit_rate": hitRate, "id": request, "model": model, "state": status, "input": input, "output": output, "ledger_cache": cache, "reservation": json.RawMessage(original)})
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	var continuation any
	if ended != nil {
		var request, model, status string
		var input, output *int64
		var cache int64
		err = l.db.QueryRowContext(ctx, "SELECT id,model,state,input,output,cache FROM requests WHERE scope=? AND compaction_id IS NULL AND at>=? AND rowid>(SELECT COALESCE(MAX(rowid),0) FROM requests WHERE compaction_id=?) ORDER BY at,id LIMIT 1", scope, *ended, id).Scan(&request, &model, &status, &input, &output, &cache)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		if err == nil {
			continuation = map[string]any{"id": request, "model": model, "state": status, "input": input, "output": output, "ledger_cache": cache}
		}
	}
	var ratio any
	if before != nil && after != nil && *before > 0 {
		ratio = float64(*after) / float64(*before)
	}
	return map[string]any{"schema_version": 1, "operation_id": id, "scope": scope, "turn": turn, "reason": reason, "state": state, "at": at, "end_at": ended, "tokens_before_estimate": before, "tokens_after_estimate": after, "context_ratio_estimate": ratio, "next_continuation": continuation, "verified_cost": nil, "method": "append", "evidence": evidence, "attempts": attempts}, rows.Err()
}

// The diagnostic record distinguishes unreported subsets from a reported zero.
// Native frames remain in evidence for provider-specific fields not understood here.
func compactionMetrics(evidence []json.RawMessage, request string) map[string]any {
	metrics := map[string]any{"cache_read_tokens": nil, "cache_write_tokens": nil, "reasoning_tokens": nil}
	set := func(key string, source map[string]any, name string) {
		if number, ok := source[name].(float64); ok && number >= 0 && number <= float64(MaxSafeInteger) {
			metrics[key] = number
		}
	}
	object := func(value any) map[string]any { m, _ := value.(map[string]any); return m }
	for _, raw := range evidence {
		var report struct {
			Evidence json.RawMessage `json:"evidence"`
		}
		if json.Unmarshal(raw, &report) != nil {
			continue
		}
		var body map[string]any
		if json.Unmarshal(report.Evidence, &body) != nil || body["request_id"] != request {
			continue
		}
		if frames, ok := body["usage_frames"].([]any); ok {
			for _, frame := range frames {
				set("cache_read_tokens", object(frame), "prompt_cache_hit_tokens")
			}
		}
		if native, ok := body["native_response_sse"].(string); ok {
			parser := modelprotocol.SSE{}
			events, _ := parser.Push([]byte(native))
			for _, event := range events {
				var frame map[string]any
				if json.Unmarshal(event, &frame) != nil {
					continue
				}
				usage := object(frame["usage"])
				if response := object(frame["response"]); response != nil {
					usage = object(response["usage"])
				}
				set("reasoning_tokens", object(usage["completion_tokens_details"]), "reasoning_tokens")
				set("reasoning_tokens", object(usage["output_tokens_details"]), "reasoning_tokens")
				set("cache_write_tokens", usage, "cache_creation_input_tokens")
				metadata := object(frame["usageMetadata"])
				set("reasoning_tokens", metadata, "thoughtsTokenCount")
				set("cache_read_tokens", metadata, "cachedContentTokenCount")
			}
		}
	}
	return metrics
}
