package assistant

import (
	"context"
	"testing"
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
	reopened, err := OpenMailbox(path, "local")
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

func TestInterruptedCorrespondenceRetainsReceiptWithoutReactivation(t *testing.T) {
	ctx := context.Background()
	m, path := mailbox(t, "local")
	seed := sendMail(t, m, "interrupted", address("local", "tony"), address("local", "main"), nil)
	if _, err := m.ClaimMailBatch(ctx, seed); err != nil {
		t.Fatal(err)
	}
	m.Close()
	reopened, err := OpenMailbox(path, "local")
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
