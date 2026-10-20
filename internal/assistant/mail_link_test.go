package assistant

import (
	"context"
	"fmt"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestPendingNodeFiltersBeforePageLimitAndKeepsAssistantRoutes(t *testing.T) {
	ctx := context.Background()
	mail, _ := mailbox(t, "source")
	local, peer := address("source", "alice"), address("target", "bob")
	for index := 0; index < protocol.MailPageSize+1; index++ {
		sendMail(t, mail, fmt.Sprintf("other-node-%d", index), local, address("elsewhere", "bob"), nil)
	}
	task := local
	task.Kind = "task"
	wanted := []protocol.Envelope{
		sendMail(t, mail, "alice-bob", local, peer, nil),
		sendMail(t, mail, "other-bob", address("source", "other"), peer, nil),
		sendMail(t, mail, "alice-other", local, address("target", "other"), nil),
		sendMail(t, mail, "task-bob", task, peer, nil),
	}
	pending, err := mail.PendingNode(ctx, "source", "target")
	if err != nil || len(pending) != len(wanted) {
		t.Fatal(pending, err)
	}
	for i, message := range wanted {
		if !sameEnvelope(pending[i], message) {
			t.Fatal("node delivery changed assistant route", pending[i])
		}
		if err = mail.Acknowledge(ctx, protocol.Receipt{Message: message}); err != nil {
			t.Fatal(err)
		}
	}
	pending, err = mail.PendingNode(ctx, "source", "target")
	if err != nil || len(pending) != 0 {
		t.Fatal(pending, err)
	}
	other, err := mail.PendingNode(ctx, "source", "elsewhere")
	if err != nil || len(other) != protocol.MailPageSize {
		t.Fatal("another node lost its backlog", other, err)
	}
}
