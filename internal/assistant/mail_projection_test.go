package assistant

import (
	"context"
	"sync"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestCorrespondenceReceiptSurvivesCompletionAndRestart(t *testing.T) {
	ctx := context.Background()
	m, path := mailbox(t, "local")
	seed := sendMail(t, m, "mail", address("local", "tony"), address("local", "main"), nil)
	if _, err := m.ClaimMailBatch(ctx, seed); err != nil {
		t.Fatal(err)
	}
	if err := m.ReleaseEvent(ctx, seed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimMailBatch(ctx, seed); err != nil {
		t.Fatal("unaccepted turn could not release its claim", err)
	}
	turn := "pot-result-" + seed.ID
	if err := m.FinishChat(ctx, "main", turn, seed.ID, true); err != nil {
		t.Fatal(err)
	}
	m.Close()
	reopened, err := OpenMailbox(path, "local", MailboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	mail, err := reopened.DeliveryMail(ctx, "main", turn)
	if err != nil || len(mail) != 1 || !sameEnvelope(mail[0], seed) {
		t.Fatal("history lost the immutable incoming receipt", mail, err)
	}
	if other, err := reopened.DeliveryMail(ctx, "other", turn); err != nil || len(other) != 0 {
		t.Fatal("receipt escaped its recipient", other, err)
	}
	if _, err := reopened.ClaimMailBatch(ctx, seed); err == nil {
		t.Fatal("completed mail replayed")
	}
}

func TestResumeDeliveryRestoresExactBatchAndNeverCompletedMail(t *testing.T) {
	ctx := context.Background()
	m, _ := mailbox(t, "local")
	a := sendMail(t, m, "a", address("local", "tony"), address("local", "main"), nil)
	b := sendMail(t, m, "b", a.From, a.To, nil)
	if _, err := m.ClaimMailBatch(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := m.FinishChat(ctx, "main", "pot-result-a", a.ID, false); err != nil {
		t.Fatal(err)
	}
	later := sendMail(t, m, "later", a.From, a.To, nil)
	if err := m.ResumeDelivery(ctx, "other", a.ID); err == nil {
		t.Fatal("foreign owner resumed mail")
	}
	if err := m.ResumeDelivery(ctx, "main", a.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeDelivery(ctx, "main", a.ID); err != nil {
		t.Fatal("resume not idempotent", err)
	}
	for _, message := range []protocol.Envelope{a, b} {
		if err := m.Processing(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Processing(ctx, later); err == nil {
		t.Fatal("later mail absorbed by continuation")
	}
	if err := m.FinishChat(ctx, "main", "pot-result-a", a.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := m.ResumeDelivery(ctx, "main", a.ID); err == nil {
		t.Fatal("completed delivery replayed")
	}
}

func TestAcceptedMailReceiptSurvivesFailedEventAndRetiredParent(t *testing.T) {
	ctx := context.Background()
	m, _ := mailbox(t, "local")
	parent := sendMail(t, m, "parent", address("local", "tony"), address("local", "main"), nil)
	if err := m.Claim(ctx, parent.ID); err != nil {
		t.Fatal(err)
	}
	send := protocol.SendMail{To: parent.From, Text: "reply"}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := m.Send(ctx, "stable-id", parent.To, send, &parent); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	// A failure in a later transcript write cannot roll back the committed inbox.
	if _, err := m.db.Exec(`CREATE TEMP TRIGGER fail_event BEFORE INSERT ON assistant_mail_events BEGIN SELECT RAISE(FAIL,'controlled event failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := m.db.Exec("INSERT INTO assistant_mail_events(message,event) VALUES(?,?)", parent.ID, `{}`); err == nil {
		t.Fatal("event failure was not exercised")
	}
	if err := m.Finish(ctx, parent.ID, true, ""); err != nil {
		t.Fatal(err)
	}
	receipt, err := m.Send(ctx, "stable-id", parent.To, send, &parent)
	if err != nil || receipt.ID != "stable-id" {
		t.Fatal("accepted receipt lost after event failure", receipt, err)
	}
	if _, err = m.Send(ctx, "new-id", parent.To, send, &parent); err == nil {
		t.Fatal("retired sender accepted new mail")
	}
	send.Text = "conflicting payload"
	if _, err = m.Send(ctx, "stable-id", parent.To, send, &parent); err == nil {
		t.Fatal("ID conflict accepted")
	}
	var count int
	if err = m.db.QueryRow("SELECT COUNT(*) FROM agent_messages WHERE id='stable-id'").Scan(&count); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func TestInterruptedCorrespondenceRetainsReceiptWithoutReactivation(t *testing.T) {
	ctx := context.Background()
	m, path := mailbox(t, "local")
	seed := sendMail(t, m, "interrupted", address("local", "tony"), address("local", "main"), nil)
	if _, err := m.ClaimMailBatch(ctx, seed); err != nil {
		t.Fatal(err)
	}
	m.Close()
	reopened, err := OpenMailbox(path, "local", MailboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.Processing(ctx, seed); err == nil {
		t.Fatal("uncertain execution was reactivated")
	}
	mail, err := reopened.DeliveryMail(ctx, "main", "pot-result-"+seed.ID)
	if err != nil || len(mail) != 1 {
		t.Fatal("uncertain receipt disappeared", mail, err)
	}
	if candidates, err := reopened.Candidates(ctx, "main"); err != nil || len(candidates) != 0 {
		t.Fatal("uncertain mail became executable", candidates, err)
	}
}

func TestConfirmedFailureDoesNotStarveQueuedMailAfterRestart(t *testing.T) {
	ctx := context.Background()
	m, path := mailbox(t, "local")
	old := sendMail(t, m, "failed", address("local", "tony"), address("local", "main"), nil)
	if err := m.Claim(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	if err := m.Finish(ctx, old.ID, false, "confirmed failure"); err != nil {
		t.Fatal(err)
	}
	next := sendMail(t, m, "new", old.From, old.To, nil)
	m.Close()
	reopened, err := OpenMailbox(path, "local", MailboxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	candidates, err := reopened.Candidates(ctx, "main")
	if err != nil || len(candidates) != 1 || candidates[0].ID != next.ID {
		t.Fatal("failed history starved queued mail after restart", candidates, err)
	}
	if _, err = reopened.ClaimMailBatch(ctx, next); err != nil {
		t.Fatal(err)
	}
	if err = reopened.Processing(ctx, old); err == nil {
		t.Fatal("new mail revived old sender authority")
	}
}
