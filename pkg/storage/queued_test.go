package storage

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestQueuedUserProjectionAndSelectionAreAtomicAcrossRestart(t *testing.T) {
	ctx := context.Background()
	s, path := testStore(t)
	request := "pending-user"
	m := Message{ID: "user", ConversationID: "main", Role: protocol.User, Text: "unstarted", Status: protocol.Accepted, Queued: true, CreatedAtMS: Now(), RequestID: &request}
	if _, err := s.Save(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	var err error
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	saved, err := s.Duplicate(ctx, request)
	if err != nil || saved == nil || !saved.Queued || saved.Status != protocol.Accepted {
		t.Fatal("safe pending request became interrupted", saved, err)
	}
	m.Queued = false
	_, err = s.SaveWithMetadataMutation(ctx, m, map[string]string{"selected": "user"}, func(*sql.Tx) error { return errors.New("selection failed") })
	if err == nil {
		t.Fatal("failed mutation was committed")
	}
	saved, err = s.Duplicate(ctx, request)
	if err != nil || !saved.Queued {
		t.Fatal("projection escaped rollback", saved, err)
	}
	if _, exists, err := s.Get(ctx, "selected"); err != nil || exists {
		t.Fatal("turn metadata escaped rollback", exists, err)
	}
}
