package linkhost

import (
	"context"
	"github.com/jjyr/pot/pkg/agentlink"
	"testing"
)

func TestAgentPluginsRejectDuplicateAndInvalidAdapters(t *testing.T) {
	h := New()
	p := Messages{Name: "pot", Service: &agentlink.Service{}}
	if err := h.RegisterAgent(p); err != nil {
		t.Fatal(err)
	}
	if h.RegisterAgent(p) == nil {
		t.Fatal("duplicate adapter accepted")
	}
	if h.RegisterAgent(Messages{Name: "invalid"}) == nil {
		t.Fatal("empty service accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	New().Run(ctx)
}
