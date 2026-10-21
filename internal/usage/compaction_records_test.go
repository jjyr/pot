package usage

import (
	"context"
	"encoding/json"
	"testing"
)

func TestCompactionDownloadOwnershipNullableUsageAndContinuation(t *testing.T) {
	l, _ := ledger(t)
	ctx := context.Background()
	if err := l.Compaction(ctx, "main", "turn-1", CompactionEvent{ID: "compact", Action: "start", Reason: "idle", TokensBefore: number(20000)}, 100); err != nil {
		t.Fatal(err)
	}
	body := json.RawMessage(`{"method":"append","request":{"messages":["private history"]}}`)
	if err := l.RecordCompaction(ctx, "foreign", "turn-1", "compact", body); err == nil {
		t.Fatal("foreign evidence accepted")
	}
	if err := l.RecordCompaction(ctx, "main", "turn-1", "compact", body); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Reserve(ctx, reservation("summary", 101)); err != nil {
		t.Fatal(err)
	}
	if err := l.TagCompaction(ctx, "summary", "compact"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.CompactionRecord(ctx, "foreign", "compact"); err == nil {
		t.Fatal("foreign history leaked")
	}
	first, err := l.CompactionRecord(ctx, "main", "compact")
	if err != nil {
		t.Fatal(err)
	}
	attempts := first["attempts"].([]map[string]any)
	if len(attempts) != 1 || attempts[0]["input"].(*int64) != nil {
		t.Fatal("unknown usage coerced to zero", attempts)
	}
	if err := l.Settle(ctx, "summary", Frame{Input: number(12000), Output: number(700), Cache: number(11000)}); err != nil {
		t.Fatal(err)
	}
	if err := l.Compaction(ctx, "main", "turn-1", CompactionEvent{ID: "compact", Action: "complete", TokensAfter: number(2000)}, 110); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Reserve(ctx, reservation("continuation", 111)); err != nil {
		t.Fatal(err)
	}
	record, err := l.CompactionRecord(ctx, "main", "compact")
	if err != nil {
		t.Fatal(err)
	}
	if record["context_ratio_estimate"] != 0.1 || record["next_continuation"].(map[string]any)["id"] != "continuation" || len(record["evidence"].([]json.RawMessage)) != 1 {
		t.Fatal(record)
	}
	var count int
	if err = l.db.QueryRow("SELECT COUNT(*) FROM requests").Scan(&count); err != nil || count != 2 {
		t.Fatal("download duplicated usage", count, err)
	}
}

func TestCompactionNativeUsageSubsetsRemainUnknownUntilReported(t *testing.T) {
	empty := compactionMetrics(nil, "request")
	if empty["cache_read_tokens"] != nil || empty["reasoning_tokens"] != nil {
		t.Fatal(empty)
	}
	raw, _ := json.Marshal(map[string]any{"evidence": map[string]any{"request_id": "request", "usage_frames": []any{map[string]any{"prompt_cache_hit_tokens": 0}}, "native_response_sse": "data: {\"usage\":{\"completion_tokens_details\":{\"reasoning_tokens\":17},\"cache_creation_input_tokens\":12}}\n\n"}})
	metrics := compactionMetrics([]json.RawMessage{raw}, "request")
	if metrics["cache_read_tokens"] != float64(0) || metrics["reasoning_tokens"] != float64(17) || metrics["cache_write_tokens"] != float64(12) {
		t.Fatal(metrics)
	}
	if foreign := compactionMetrics([]json.RawMessage{raw}, "foreign"); foreign["reasoning_tokens"] != nil {
		t.Fatal("cross-attempt usage", foreign)
	}
}
