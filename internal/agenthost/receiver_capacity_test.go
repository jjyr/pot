package agenthost

import (
	"errors"
	"strconv"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

// Preserve the queued legacy bytes: retirement is a separate tombstone, not a
// terminal-state rewrite. All jobs and callers here are isolated test fixtures.
func receiverRetiredJobsFixture(t *testing.T, p *receiverPump, count int) func() {
	t.Helper()
	type snapshot struct{ payload, data string }
	originals := map[string]snapshot{}
	for i := 0; i < count; i++ {
		task, err := p.backend.Submit(t.Context(), agentlink.TaskRequest{
			From:   protocol.AgentRef{NodeID: "old-node", Kind: "assistant", AgentID: "old-owner"},
			Target: agentlink.NodeTarget(p.backend.nodeID), RequestID: "retired-continuation-" + strconv.Itoa(i), Text: "old accepted queued work",
		})
		if err != nil {
			t.Fatal(err)
		}
		var before snapshot
		if err = p.backend.db.QueryRow("SELECT payload,data FROM agent_adapter_jobs WHERE id=?", task.ID).Scan(&before.payload, &before.data); err != nil {
			t.Fatal(err)
		}
		originals[task.ID] = before
		if _, err = p.backend.db.Exec("INSERT INTO retired_agent_link_work VALUES('job',?)", task.ID); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		t.Helper()
		for id, before := range originals {
			var after snapshot
			if err := p.backend.db.QueryRow("SELECT payload,data FROM agent_adapter_jobs WHERE id=?", id).Scan(&after.payload, &after.data); err != nil || before != after {
				t.Fatal("retired job bytes changed", id, err)
			}
		}
	}
}

func TestReceiverTerminalContinuationRetainsCurrentActiveCapacityLimit(t *testing.T) {
	p, remote, d := receiverFixture(t)
	ctx := t.Context()
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	old := receiverRecordFixture(t, p, d)
	j, err := p.backend.read(ctx, d.Work.Request.From, old.Admission.Binding.Task.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	p.backend.runTask(ctx, j)
	receiverMustStep(t, p)
	request := d.Work.Request
	for i := 0; i < 32; i++ {
		request.RequestID = "current-active-" + strconv.Itoa(i)
		if _, err = p.backend.Submit(ctx, request); err != nil {
			t.Fatal(err)
		}
	}
	request.RequestID = "over-active-limit"
	if _, err = p.backend.Submit(ctx, request); !errors.Is(err, agentlink.ErrCapacity) {
		t.Fatal("new work bypassed current active limit", err)
	}
	if _, err = p.backend.Input(ctx, agentlink.TaskGuidance{From: request.From, ID: j.Task.ID, RequestID: "over-active-input", Text: "continue"}); !errors.Is(err, agentlink.ErrCapacity) {
		t.Fatal("native continuation bypassed current active limit", err)
	}
	control := agentlink.MCPControlDelivery{Version: 1, DeliveryID: "capacity-input", Dispatch: d.Claim.Binding, Binding: old.Admission.Binding, Caller: request.From, InputPhase: protocol.TaskComplete, Input: &agentlink.MCPTaskInput{RequestID: "capacity-follow-up", TaskID: d.Claim.Binding.Task.AgentID, RunID: old.Admission.Binding.RunID, Text: "continue"}}
	claim := agentlink.MCPClaim{ConnectionID: p.identity.ConnectionID(), DeliveryID: control.DeliveryID, ClaimID: "capacity-claim", Binding: d.Claim.Binding}
	next := receiverDelivery{Claim: &claim, Control: &control}
	remote.queue = append(remote.queue, next)
	for range 4 {
		receiverMustStep(t, p)
	}
	blocked := receiverRecordFixture(t, p, next)
	if !blocked.Acknowledged || !blocked.Ack.Accepted || blocked.Admission != nil {
		t.Fatal("current capacity did not defer receiver admission", blocked)
	}
	current, err := p.backend.read(ctx, request.From, j.Task.ID)
	if err != nil || current.Task.State != "completed" || current.Task.RunID != old.Admission.Binding.RunID {
		t.Fatal("blocked input changed the terminal run", current, err)
	}
	var inputs int
	if err = p.backend.db.QueryRow("SELECT count(*) FROM agent_adapter_inputs WHERE task_id=?", j.Task.ID).Scan(&inputs); err != nil || inputs != 0 {
		t.Fatal("blocked input created continuation history", inputs, err)
	}
}
