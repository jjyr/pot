package storage

import (
	"bytes"
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

func TestConversationRolloutUpgrade(t *testing.T) {
	for _, skipped := range []bool{false, true} {
		t.Run(map[bool]string{false: "preceding", true: "skipped"}[skipped], func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "conversation.sqlite")
			db, err := OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			paths := taskDefaultMigrationPaths(t, ConversationDatabase)
			prior := taskDefaultBefore(paths, "migrations/conversation/0005_session_rollouts.sql")
			if skipped {
				prior = prior[:1]
			}
			migrateTaskDefaultPaths(t, db, ConversationDatabase, prior)
			first := `{"type":"context","version":2,"messages":[{"role":"user","content":"hello"}],"pending":[]}`
			last := `{"type":"context","version":2,"messages":[{"role":"user","content":"hello"},{"role":"assistant","native":{"opaque":"preserve"}}],"pending":[],"receipts":[{"call":"exact"}]}`
			for i, body := range []string{first, last} {
				if _, err = db.Exec("INSERT INTO context_history(classification,body,at) VALUES('context',?,?)", body, 100+i); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = db.Exec("INSERT INTO metadata VALUES('thread.context',?)", last); err != nil {
				t.Fatal(err)
			}
			db.Close()
			s, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := s.LoadContext(ctx)
			if err != nil || !sameContextJSON(loaded, json.RawMessage(last)) {
				t.Fatal(string(loaded), err)
			}
			var count int
			if err = s.db.QueryRow("SELECT count(*) FROM rollout_legacy_context").Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
			if err = s.db.QueryRow("SELECT count(*) FROM metadata WHERE key='thread.context'").Scan(&count); err != nil || count != 0 {
				t.Fatal(count, err)
			}
			data, err := os.ReadFile(s.rolloutPath)
			if err != nil {
				t.Fatal(err)
			}
			var entry struct {
				At int64 `json:"at_ms"`
			}
			if err := json.Unmarshal(bytes.SplitN(data, []byte("\n"), 2)[0], &entry); err != nil || entry.At != 100 {
				t.Fatal("legacy timestamp lost", entry.At, err)
			}
			s.Close()
			s, err = Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			after, err := os.ReadFile(s.rolloutPath)
			if err != nil || string(after) != string(data) {
				t.Fatal("reopen duplicated import", err)
			}
			if err = s.CompressContext(ctx, time.Now().Add(time.Hour)); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Stat(s.rolloutPath + ".zst"); err != nil {
				t.Fatal(err)
			}
			loaded, err = s.LoadContext(ctx)
			if err != nil || !sameContextJSON(loaded, json.RawMessage(last)) {
				t.Fatal(err)
			}
			if err = s.SaveContext(ctx, json.RawMessage(first)); err != nil {
				t.Fatal(err)
			}
			loaded, err = s.LoadContext(ctx)
			if err != nil || !sameContextJSON(loaded, json.RawMessage(first)) {
				t.Fatal(err)
			}
		})
	}
}

func TestConversationRolloutReferenceRollback(t *testing.T) {
	ctx := context.Background()
	s, _ := testStore(t)
	original := json.RawMessage(`{"type":"context","version":2,"messages":[]}`)
	if err := s.SaveContext(ctx, original); err != nil {
		t.Fatal(err)
	}
	failed := json.RawMessage(`{"type":"context","version":2,"messages":[{"text":"uncommitted"}]}`)
	rollback := errors.New("rollback")
	err := Transaction(ctx, s.db, func(tx *sql.Tx) error {
		if err := s.SaveContextTx(ctx, tx, failed); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	got, err := s.LoadContext(ctx)
	if err != nil || !sameContextJSON(got, original) {
		t.Fatal("orphan became committed", string(got), err)
	}
	if err = s.SaveContext(ctx, original); err != nil {
		t.Fatal(err)
	}
	if err = s.CompressContext(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestConversationFailedImportPreservesLegacy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	db, err := OpenSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	migrateTaskDefaultPaths(t, db, ConversationDatabase, taskDefaultBefore(taskDefaultMigrationPaths(t, ConversationDatabase), "migrations/conversation/0005_session_rollouts.sql"))
	_, err = db.Exec("INSERT INTO context_history(classification,body,at) VALUES('context','invalid',1); INSERT INTO metadata VALUES('thread.context','{\"type\":\"context\",\"version\":2,\"messages\":[]}')")
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if s, err := Open(path); err == nil {
		s.Close()
		t.Fatal("invalid history imported")
	}
	db, err = OpenDatabase(path, ConversationDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err = db.QueryRow("SELECT count(*) FROM rollout_legacy_context").Scan(&n); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	var ref rollout.Ref
	var imported bool
	if err = db.QueryRow("SELECT sequence,hash,imported FROM context_rollout").Scan(&ref.Sequence, &ref.Hash, &imported); err != nil || imported || !ref.IsZero() {
		t.Fatal(ref, imported, err)
	}
}

func TestConversationColdRollbackTailDoesNotBlockRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "conversation.sqlite")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	committed := json.RawMessage(`{"type":"context","version":2,"messages":[{"text":"committed"}]}`)
	if err := s.SaveContext(ctx, committed); err != nil {
		s.Close()
		t.Fatal(err)
	}
	rollback := errors.New("SQL rollback after journal sync")
	err = Transaction(ctx, s.db, func(tx *sql.Tx) error {
		if err := s.SaveContextTx(ctx, tx, json.RawMessage(`{"type":"context","version":2,"messages":[{"text":"uncommitted"}]}`)); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		s.Close()
		t.Fatal(err)
	}
	journalPath := s.rolloutPath
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	cold := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(journalPath, cold, cold); err != nil {
		t.Fatal(err)
	}
	// Startup must neither compress nor discard an uncommitted tail.
	reopened, err := Open(path)
	if err != nil {
		t.Fatal("recoverable uncommitted tail blocked cold startup", err)
	}
	defer reopened.Close()
	got, err := reopened.LoadContext(ctx)
	if err != nil || !sameContextJSON(got, committed) {
		t.Fatal("startup lost committed context or exposed rolled-back state", string(got), err)
	}
	if _, err := os.Stat(journalPath); err != nil {
		t.Fatal("startup changed plain representation", err)
	}
	if _, err := os.Stat(journalPath + ".zst"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("startup performed compression", err)
	}
	if err := reopened.CompressContext(ctx, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err = reopened.LoadContext(ctx)
	if err != nil || !sameContextJSON(got, committed) {
		t.Fatal("explicit compression changed committed state", string(got), err)
	}
}
