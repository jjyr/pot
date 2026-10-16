package sandbox

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPreparedReceiptInspectionNeverPreparesOrExecutesAgain(t *testing.T) {
	m, p, grant := setupManager(t, true)
	ctx := context.Background()
	original := json.RawMessage(`{"cmd":"printf original","login":false,"yield_time_ms":1000}`)
	prepared, err := m.PrepareOperation(ctx, grant, "exec_command", original)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.RememberPrepared(ctx, grant, "call", "exec_command", original, prepared); err != nil {
		t.Fatal(err)
	}
	expected, err := m.Execute(ctx, grant, "call", "exec_command", prepared, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	starts, executions := p.starts, p.executions
	p.mu.Unlock()
	result, err := m.InspectPreparedReceipt(ctx, grant, "call", "exec_command", original)
	if err != nil || string(result) != string(expected) {
		t.Fatal(string(result), err)
	}
	changed := grant
	changed.Turn = "another-turn"
	if _, err = m.InspectPreparedReceipt(ctx, changed, "call", "exec_command", original); err == nil {
		t.Fatal("rebound run accepted")
	}
	if _, err = m.InspectPreparedReceipt(ctx, grant, "call", "exec_command", json.RawMessage(`{"cmd":"different"}`)); err == nil {
		t.Fatal("rebound arguments accepted")
	}
	if _, err = m.InspectPreparedReceipt(ctx, grant, "missing", "exec_command", original); err == nil {
		t.Fatal("missing result synthesized")
	}
	if err = m.RememberPrepared(ctx, grant, "call", "exec_command", json.RawMessage(`{}`), prepared); err == nil {
		t.Fatal("prepared identity changed")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if starts != p.starts || executions != p.executions {
		t.Fatal("inspection retried guest work", p.starts, p.executions)
	}
}
