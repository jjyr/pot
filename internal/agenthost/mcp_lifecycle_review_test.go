package agenthost

import (
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"testing"
)

func TestMCPRegressionRetiredJobsMustNotConsumeNewMCPActiveCapacity(t *testing.T) {
	for _, retiredCount := range []int{32, 256} {
		t.Run(fmt.Sprint(retiredCount), func(t *testing.T) {
			p, _, _ := receiverFixture(t)
			ctx := t.Context()
			from := protocol.AgentRef{NodeID: "old-node", Kind: "assistant", AgentID: "old-owner"}
			originals := map[string]string{}
			for i := 0; i < retiredCount; i++ {
				task, e := p.backend.Submit(ctx, agentlink.TaskRequest{Target: agentlink.NodeTarget(p.backend.nodeID), From: from, RequestID: fmt.Sprintf("old-%d", i), Text: "old accepted queued work"})
				if e != nil {
					t.Fatal(e)
				}
				if _, e = p.backend.db.Exec("INSERT INTO retired_agent_link_work VALUES('job',?)", task.ID); e != nil {
					t.Fatal(e)
				}
				var raw string
				if e = p.backend.db.QueryRow("SELECT data FROM agent_adapter_jobs WHERE id=?", task.ID).Scan(&raw); e != nil {
					t.Fatal(e)
				}
				originals[task.ID] = raw
			}
			if e := p.backend.recover(ctx); e != nil {
				t.Fatal(e)
			}
			receiverMustStep(t, p)
			receiverMustStep(t, p)
			records, e := p.Records(ctx)
			if e != nil || len(records) != 1 || records[0].Admission == nil {
				t.Fatal("fresh work unadmitted", records, e)
			}
			for id, expected := range originals {
				var raw string
				if e = p.backend.db.QueryRow("SELECT data FROM agent_adapter_jobs WHERE id=?", id).Scan(&raw); e != nil || raw != expected {
					t.Fatal("retired job changed", id, e)
				}
			}
			// Current accepted work still consumes the original active limit, including
			// the real new admission above; retired exclusions cannot bypass that limit.
			request := agentlink.TaskRequest{Target: agentlink.NodeTarget(p.backend.nodeID), From: from, Text: "fresh accepted work"}
			for i := 1; i < 32; i++ {
				request.RequestID = fmt.Sprintf("fresh-%d", i)
				if _, e = p.backend.Submit(ctx, request); e != nil {
					t.Fatal(e)
				}
			}
			request.RequestID = "over-capacity"
			if _, e = p.backend.Submit(ctx, request); !errors.Is(e, agentlink.ErrCapacity) {
				t.Fatal("fresh capacity limit lost", e)
			}
		})
	}
}

func TestMCPRegressionFreshNativeWorkAdmitsWithoutRetiredCapacity(t *testing.T) {
	p, _, _ := receiverFixture(t)
	if e := p.step(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e := p.step(t.Context()); e != nil {
		t.Fatal(e)
	}
	records, e := p.Records(t.Context())
	if e != nil || len(records) != 1 || records[0].Admission == nil {
		t.Fatal(records, e)
	}
	t.Log("fresh MCP job admits with actual Task/run when capacity is not occupied by retired queued jobs")
}
