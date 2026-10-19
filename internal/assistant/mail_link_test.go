package assistant

import (
	"context"
	"fmt"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestPendingLinkFiltersFullRouteBeforePageLimit(t *testing.T) {
	ctx := context.Background()
	mail, _ := mailbox(t, "source")
	local, peer := address("source", "alice"), address("target", "bob")
	for index := 0; index < protocol.MailPageSize+1; index++ {
		sendMail(t, mail, fmt.Sprintf("other-local-%d", index), address("source", "other"), peer, nil)
		sendMail(t, mail, fmt.Sprintf("other-peer-%d", index), local, address("target", "other"), nil)
		sendMail(t, mail, fmt.Sprintf("other-control-%d", index), local, address("elsewhere", "bob"), nil)
	}
	// Identical text IDs with another kind are a distinct source identity.
	task := local
	task.Kind = "task"
	sendMail(t, mail, "task-same-id", task, peer, nil)
	wanted := sendMail(t, mail, "bound-route", local, peer, nil)
	pending, err := mail.PendingLink(ctx, local, peer)
	if err != nil || len(pending) != 1 || !sameEnvelope(pending[0], wanted) {
		t.Fatal("unrelated backlog blocked or leaked into link", pending, err)
	}
	if err = mail.Acknowledge(ctx, protocol.Receipt{Message: wanted}); err != nil {
		t.Fatal(err)
	}
	pending, err = mail.PendingLink(ctx, local, peer)
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	other, err := mail.PendingLink(ctx, address("source", "other"), peer)
	if err != nil || len(other) != protocol.MailPageSize {
		t.Fatal("another link lost its pending messages", other, err)
	}
}
