package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/rollout"
)

const DefaultRolloutCompressionRate int64 = 8 << 20

type contextCompression interface {
	Commit(context.Context) error
	Abort() error
}
type prepareContextCompression func(context.Context, string, rollout.Ref, int64) (contextCompression, error)

func prepareContextArchive(ctx context.Context, path string, ref rollout.Ref, rate int64) (contextCompression, error) {
	return rollout.PrepareCompression(ctx, path, ref, rate)
}

// CompressContext explicitly checks one cold idle conversation. Expensive work
// is staged outside SQLite; publication rechecks its activity and commit cursor.
func (s *Store) CompressContext(ctx context.Context, before time.Time) error {
	_, err := s.CompressContextBounded(ctx, before, DefaultRolloutCompressionRate)
	return err
}
func (s *Store) CompressContextBounded(ctx context.Context, before time.Time, bytesPerSecond int64) (bool, error) {
	return compressContextArchive(ctx, s.db, s.rolloutPath, before, bytesPerSecond, prepareContextArchive)
}

// CompressConversationContext opens an existing, fully migrated database without
// running Store.Open's recovery/export behavior. Absent or older-format sessions
// are left for their normal owner to initialize. It never creates a database.
func CompressConversationContext(ctx context.Context, path string, before time.Time, bytesPerSecond int64) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, errors.New("conversation database is not a regular file")
	}
	existing, err := OpenDatabaseReadOnly(path, ConversationDatabase)
	if err != nil {
		return false, err
	}
	definitions, err := loadMigrations(migrationFiles, ConversationDatabase)
	complete := false
	if err == nil {
		complete, err = checkMigrationHistory(ctx, existing, ConversationDatabase, definitions)
	}
	closeErr := existing.Close()
	if err != nil {
		return false, err
	}
	if closeErr != nil {
		return false, closeErr
	}
	if !complete {
		return false, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	u := url.URL{Scheme: "file", Path: abs}
	q := u.Query()
	q.Set("mode", "rw")
	q.Set("_txlock", "immediate")
	q.Add("_pragma", "busy_timeout(250)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return false, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	journal := filepath.Join(filepath.Dir(path), "sessions", strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+".jsonl")
	return compressContextArchive(ctx, db, journal, before, bytesPerSecond, prepareContextArchive)
}

func idleContext(ctx context.Context, tx *sql.Tx, before time.Time) (bool, error) {
	var activity int64
	if err := tx.QueryRowContext(ctx, "SELECT activity_at FROM context_rollout WHERE singleton=1").Scan(&activity); err != nil {
		return false, err
	}
	if activity >= before.UnixMilli() {
		return false, nil
	}
	rows, err := tx.QueryContext(ctx, "SELECT value FROM metadata WHERE key IN ('conversation.turn','conversation.completion')")
	if err != nil {
		return false, err
	}
	busy := false
	for rows.Next() {
		var value string
		if err = rows.Scan(&value); err != nil {
			rows.Close()
			return false, err
		}
		raw := bytes.TrimSpace([]byte(value))
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			continue
		}
		var object map[string]json.RawMessage
		if err = json.Unmarshal(raw, &object); err != nil || object == nil {
			rows.Close()
			return false, errors.New("invalid conversation activity metadata")
		}
		busy = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return false, err
	}
	if busy {
		return false, nil
	}
	var queued bool
	err = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM chat_user_inbox)").Scan(&queued)
	return !queued, err
}

func compressContextArchive(ctx context.Context, db *sql.DB, path string, before time.Time, rate int64, prepare prepareContextCompression) (bool, error) {
	if rate <= 0 {
		rate = DefaultRolloutCompressionRate
	}
	var ref rollout.Ref
	eligible := false
	err := Transaction(ctx, db, func(tx *sql.Tx) error {
		var err error
		ref, err = contextRef(ctx, tx)
		if err != nil {
			return err
		}
		if ref.IsZero() {
			return nil
		}
		idle, err := idleContext(ctx, tx, before)
		if err != nil || !idle {
			return err
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			compressed, e := os.Lstat(path + ".zst")
			if e != nil {
				return e
			}
			if !compressed.Mode().IsRegular() {
				return errors.New("compressed rollout is not a regular file")
			}
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("rollout is not a regular file")
		}
		eligible = info.ModTime().Before(before)
		return nil
	})
	if err != nil || !eligible {
		return false, err
	}
	staged, err := prepare(ctx, path, ref, rate)
	if errors.Is(err, rollout.ErrChanged) || errors.Is(err, rollout.ErrNotEligible) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer staged.Abort()
	published := false
	err = Transaction(ctx, db, func(tx *sql.Tx) error {
		current, err := contextRef(ctx, tx)
		if err != nil {
			return err
		}
		if current != ref {
			return nil
		}
		idle, err := idleContext(ctx, tx, before)
		if err != nil || !idle {
			return err
		}
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || !info.ModTime().Before(before) {
			return nil
		}
		if err = staged.Commit(ctx); errors.Is(err, rollout.ErrChanged) || errors.Is(err, rollout.ErrNotEligible) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("publish context archive: %w", err)
		}
		published = true
		return nil
	})
	return published, err
}
