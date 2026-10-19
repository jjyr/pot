package linkhost

import (
	"context"
	"testing"

	"github.com/jjyr/pot/internal/controlplane"
	"github.com/jjyr/pot/pkg/agentlink"
)

func TestTypedPluginsKeepRunnerTargetAndRejectCrossKindCollision(t *testing.T) {
	h := New()
	if err := h.RegisterAgent(Messages{Name: "pot", Service: &agentlink.Service{}}); err != nil {
		t.Fatal(err)
	}
	if err := h.RegisterExecution(Runner{}); err != nil {
		t.Fatal(err)
	}
	if err := h.RegisterAgent(Messages{Name: "runner", Service: &agentlink.Service{}}); err == nil {
		t.Fatal("cross-kind collision accepted")
	}
	if err := h.RegisterExecution(Runner{}); err == nil {
		t.Fatal("duplicate execution adapter accepted")
	}
	if _, err := h.Execution("pot"); err == nil {
		t.Fatal("message plugin acquired execution contract")
	}
	p, err := h.Execution("runner")
	if err != nil {
		t.Fatal(err)
	}
	target, ok := p.Target("original-runtime").(controlplane.RemotePort)
	if !ok || target.RuntimeID != "original-runtime" {
		t.Fatal("target was replaced")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	New().Run(ctx)
}
