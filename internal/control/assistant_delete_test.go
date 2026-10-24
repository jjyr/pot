package control

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"testing"
)

func TestDeleteAssistantRetiresIdentityAndPreventsResurrection(t *testing.T) {
	t.Parallel()
	s, _ := configurationService(t)
	ctx := context.Background()
	first, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "first", Name: "First"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "other", Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}
	assertCode := func(err error, code string) {
		t.Helper()
		var api protocol.APIError
		if !errors.As(err, &api) || api.Code != code {
			t.Fatal(err, code)
		}
	}
	assertCode(s.DeleteAssistant(ctx, first.ID, 2), "assistant_changed")
	// A selection prepared before retirement must not enqueue after deletion.
	request := protocol.CreateTask{RequestID: "late", Text: "late task"}
	selection, err := s.Tasks.Prepare(ctx, first.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteAssistant(ctx, first.ID, first.Revision); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteAssistant(ctx, first.ID, first.Revision); err != nil {
		t.Fatal("retry", err)
	}
	if _, err = s.Conversation(ctx, first.ID); err == nil {
		t.Fatal("deleted cached conversation reopened")
	}
	if _, err = s.CreateAssistant(ctx, CreateAssistant{RequestID: "first", Name: "First"}); err == nil {
		t.Fatal("creation retry resurrected deleted identity")
	}
	err = storage.Transaction(ctx, s.Tasks.DB(), func(tx *sql.Tx) error {
		_, err := s.Tasks.EnqueueTx(ctx, tx, first.ID, request, selection, protocol.TaskOrigin{Kind: "manual"}, storage.Now())
		return err
	})
	assertCode(err, "assistant_unavailable")
	if ready, err := s.taskReady(ctx, first.ID); err != nil || ready {
		t.Fatal("deleted schedules remain ready", ready, err)
	}
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil || len(definitions) != 1 || definitions[0].ID != other.ID {
		t.Fatal(definitions, err)
	}
	if _, err = s.Conversation(ctx, other.ID); err != nil {
		t.Fatal("other assistant changed", err)
	}
	if err = s.DeleteAssistant(ctx, other.ID, other.Revision); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := s.BootstrapAssistant(ctx, "en")
	if err != nil || bootstrap["assistant"] != nil || bootstrap["initialized"] != true {
		t.Fatal("bootstrap resurrected assistant", bootstrap, err)
	}
	fresh, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "fresh", Name: "Fresh"})
	if err != nil || fresh.ID == first.ID || fresh.ID == other.ID {
		t.Fatal("reused deleted identity", fresh, err)
	}
}

func TestDeleteAssistantRejectsActiveTasks(t *testing.T) {
	t.Parallel()
	s, _ := configurationService(t)
	ctx := context.Background()
	a, err := s.CreateAssistant(ctx, CreateAssistant{RequestID: "owner", Name: "Owner"})
	if err != nil {
		t.Fatal(err)
	}
	// Stop the fixture's dispatcher before inserting a queued Task.
	s.cancel()
	s.taskLife.wg.Wait()
	_, err = s.Tasks.AcceptLocal(ctx, a.ID, protocol.CreateTask{RequestID: "queued", Text: "queued task"})
	if err != nil {
		t.Fatal(err)
	}
	err = s.DeleteAssistant(ctx, a.ID, a.Revision)
	var api protocol.APIError
	if !errors.As(err, &api) || api.Code != "assistant_busy" {
		t.Fatal(err)
	}
	definitions, err := s.Registry.Definitions(ctx)
	if err != nil || len(definitions) != 1 {
		t.Fatal(definitions, err)
	}
}
