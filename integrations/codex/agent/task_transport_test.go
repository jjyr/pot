package agent

import (
	"context"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/linkhost"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/execution"
	"testing"
	"time"
)

func linkedPot(t *testing.T) (context.Context, *control.Service, *agentHost, agentlink.Connection) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	h, _ := testHost(t)
	t.Cleanup(h.close)
	h.backend.binary = fakeBinary
	listener, err := linkhost.ListenPeer(ctx, "127.0.0.1:0", h.peerServer())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	done := make(chan struct{})
	go func() { h.backend.run(ctx); close(done) }()
	mailDone := make(chan struct{})
	go func() { h.backend.runMessages(ctx); close(mailDone) }()
	t.Cleanup(func() { cancel(); <-done; <-mailDone })
	manager, err := execution.New("review-pot", "boot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Stop() })
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	pot, err := control.Open(control.Options{Context: ctx, Root: t.TempDir(), RuntimeID: "review-pot", BootID: "boot", Manager: manager, Keys: keys})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pot.Close() })
	invitation, err := h.links.Invite(ctx, "tcp://"+listener.Addr())
	if err != nil {
		t.Fatal(err)
	}
	connected, err := pot.ManageAgentLink(ctx, control.AgentLinkCommand{Action: "connect", Address: invitation.Address})
	if err != nil {
		t.Fatal(err)
	}
	return ctx, pot, h, connected.(agentlink.Connection)
}

func TestCodexTaskAndPeerApprovalFromPot(t *testing.T) {
	ctx, pot, h, link := linkedPot(t)
	exercisePotHumanReview(t, ctx, pot, h, link)
}
