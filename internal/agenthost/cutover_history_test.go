package agenthost

import (
	"errors"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestRetiredNativeWorkKeepsLastStateAcrossRecovery(t *testing.T) {
	h, _ := testHost(t)
	defer h.close()
	b := h.backend
	ctx := t.Context()
	from := protocol.AgentRef{NodeID: "old-node", Kind: "assistant", AgentID: "old-owner"}
	task, e := b.Submit(ctx, agentlink.TaskRequest{Target: agentlink.NodeTarget(b.nodeID), From: from, RequestID: "legacy", Text: "Old accepted work"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = b.db.Exec("INSERT INTO retired_agent_link_work VALUES('job',?)", task.ID); e != nil {
		t.Fatal(e)
	}
	var before, after string
	if e = b.db.QueryRow("SELECT data FROM agent_adapter_jobs WHERE id=?", task.ID).Scan(&before); e != nil {
		t.Fatal(e)
	}
	if e = b.recover(ctx); e != nil {
		t.Fatal(e)
	}
	if _, e = b.Cancel(ctx, from, task.ID); !errors.Is(e, agentlink.ErrUnsupported) {
		t.Fatal(e)
	}
	if _, e = b.Input(ctx, agentlink.TaskGuidance{From: from, ID: task.ID, RequestID: "retry", Text: "resume"}); !errors.Is(e, agentlink.ErrUnsupported) {
		t.Fatal(e)
	}
	if e = b.db.QueryRow("SELECT data FROM agent_adapter_jobs WHERE id=?", task.ID).Scan(&after); e != nil || before != after {
		t.Fatal("native legacy state rewritten", e)
	}
}
