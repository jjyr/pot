package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/rollout"
)

type testContextCompression struct{ committed, aborted bool }

func (c *testContextCompression) Commit(context.Context) error { c.committed = true; return nil }
func (c *testContextCompression) Abort() error                 { c.aborted = true; return nil }
func coldContextStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "conversation.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err = s.SaveContext(context.Background(), json.RawMessage(`{"type":"context","version":2,"messages":[]}`)); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err = os.Chtimes(s.rolloutPath, old, old); err != nil {
		t.Fatal(err)
	}
	return s
}
func TestColdFileRecentActivityIsNotCompressed(t *testing.T) {
	s := coldContextStore(t)
	did, err := s.CompressContextBounded(context.Background(), time.Now().Add(-7*24*time.Hour), 1<<20)
	if err != nil || did {
		t.Fatal("recent activity compressed", did, err)
	}
	if _, err = os.Stat(s.rolloutPath); err != nil {
		t.Fatal(err)
	}
}
func TestCompressionPreparesWithoutTransactionAndRechecksOwner(t *testing.T) {
	for _, change := range []string{"turn", "completion", "checkpoint", "activity", "inbox"} {
		t.Run(change, func(t *testing.T) {
			s := coldContextStore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if _, err := s.db.Exec("UPDATE context_rollout SET activity_at=0"); err != nil {
				t.Fatal(err)
			}
			staged := &testContextCompression{}
			before := time.Now().Add(-7 * 24 * time.Hour)
			did, err := compressContextArchive(ctx, s.db, s.rolloutPath, before, 1<<20, func(ctx context.Context, _ string, _ rollout.Ref, _ int64) (contextCompression, error) {
				// These writes use the same single-connection pool. They must complete
				// during preparation; a held transaction would block until the deadline.
				switch change {
				case "turn":
					if err := s.Set(ctx, "conversation.turn", `{"state":"running"}`); err != nil {
						return nil, err
					}
				case "completion":
					if err := s.Set(ctx, "conversation.completion", `{"phase":"succeeded"}`); err != nil {
						return nil, err
					}
				case "checkpoint":
					if err := s.SaveContext(ctx, json.RawMessage(`{"type":"context","version":2,"messages":[{"text":"new"}]}`)); err != nil {
						return nil, err
					}
				case "activity":
					if _, err := s.db.ExecContext(ctx, "UPDATE context_rollout SET activity_at=?", Now()); err != nil {
						return nil, err
					}
				case "inbox":
					if _, err := s.db.ExecContext(ctx, "INSERT INTO chat_user_inbox(request,message) VALUES('{}','pending')"); err != nil {
						return nil, err
					}
				}
				return staged, nil
			})
			if err != nil || did || staged.committed || !staged.aborted {
				t.Fatal(did, err, staged)
			}
		})
	}
}
func TestBusyContextSkipsPreparation(t *testing.T) {
	s := coldContextStore(t)
	if err := s.Set(context.Background(), "conversation.turn", `{"state":"waiting"}`); err != nil {
		t.Fatal(err)
	}
	called := false
	did, err := compressContextArchive(context.Background(), s.db, s.rolloutPath, time.Now().Add(time.Hour), 0, func(context.Context, string, rollout.Ref, int64) (contextCompression, error) {
		called = true
		return &testContextCompression{}, nil
	})
	if err != nil || did || called {
		t.Fatal(did, called, err)
	}
}
func TestUnloadedCompressionDoesNotCreateOrRecoverConversation(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "missing.sqlite")
	did, err := CompressConversationContext(ctx, path, time.Now(), 0)
	if err != nil || did {
		t.Fatal(did, err)
	}
	if _, err = os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created missing DB", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveContext(ctx, json.RawMessage(`{"type":"context","version":2,"messages":[]}`)); err != nil {
		t.Fatal(err)
	}
	raw := `{"owner":"main","state":"running"}`
	if err = s.Set(ctx, "conversation.turn", raw); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	did, err = CompressConversationContext(ctx, path, time.Now().Add(time.Hour), 0)
	if err != nil || did {
		t.Fatal(did, err)
	}
	db, err := OpenDatabaseReadOnly(path, ConversationDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got string
	if err = db.QueryRow("SELECT value FROM metadata WHERE key='conversation.turn'").Scan(&got); err != nil || got != raw {
		t.Fatal("background maintenance changed owner state", got, err)
	}
}
func TestUnloadedCompressionSkipsPendingMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	paths := taskDefaultBefore(taskDefaultMigrationPaths(t, ConversationDatabase), "migrations/conversation/0006_rollout_activity.sql")
	migrateTaskDefaultPaths(t, db, ConversationDatabase, paths)
	var before int
	if err = db.QueryRow("SELECT count(*) FROM pot_migrations").Scan(&before); err != nil {
		t.Fatal(err)
	}
	db.Close()
	did, err := CompressConversationContext(context.Background(), path, time.Now(), 0)
	if err != nil || did {
		t.Fatal(did, err)
	}
	db, err = OpenDatabaseReadOnly(path, ConversationDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var after int
	if err = db.QueryRow("SELECT count(*) FROM pot_migrations").Scan(&after); err != nil || after != before {
		t.Fatal("maintenance ran migration", before, after, err)
	}
}
func TestRolloutActivityMigrationAndTransactionRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	migrateTaskDefaultPaths(t, db, ConversationDatabase, taskDefaultBefore(taskDefaultMigrationPaths(t, ConversationDatabase), "migrations/conversation/0006_rollout_activity.sql"))
	db.Close()
	db, err = OpenDatabase(path, ConversationDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var at int64
	if err = db.QueryRow("SELECT activity_at FROM context_rollout").Scan(&at); err != nil || at != 0 {
		t.Fatal(at, err)
	}
	marker := errors.New("rollback")
	err = Transaction(context.Background(), db, func(tx *sql.Tx) error {
		if err := touchContextActivity(context.Background(), tx); err != nil {
			return err
		}
		return marker
	})
	if !errors.Is(err, marker) {
		t.Fatal(err)
	}
	if err = db.QueryRow("SELECT activity_at FROM context_rollout").Scan(&at); err != nil || at != 0 {
		t.Fatal("rolled back activity leaked", at, err)
	}
}
