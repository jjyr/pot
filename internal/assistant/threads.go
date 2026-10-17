package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

const (
	ProfileKey = "agent.profiles.v1"
	ThreadKey  = "thread"
	ContextKey = "thread.context"
)
const emptyContext = `{"type":"context","version":2,"messages":[]}`

type Rules = storage.Rules

func LoadInstructions(ctx context.Context, store *storage.Store) (Rules, error) {
	return store.Instructions(ctx)
}
func SaveInstructions(ctx context.Context, store *storage.Store, text, revision string) (Rules, error) {
	return store.SaveInstructions(ctx, text, revision)
}
func Profiles(ctx context.Context, store *storage.Store) ([]core.Profile, error) {
	body, exists, err := store.Get(ctx, ProfileKey)
	if err != nil {
		return nil, err
	}
	if !exists {
		return []core.Profile{}, nil
	}
	var profiles []core.Profile
	if err := core.DecodeJSON([]byte(body), &profiles); err != nil {
		return nil, err
	}
	for _, p := range profiles {
		if err := p.Validate(); err != nil {
			return nil, err
		}
	}
	return profiles, nil
}
func decodeThread(body string) (core.Thread, error) {
	var t core.Thread
	if err := core.DecodeJSON([]byte(body), &t); err != nil {
		return t, err
	}
	if !core.Identifier(t.Owner) || t.Generation == 0 || t.Generation > core.MaxSafeInteger {
		return t, errors.New("invalid thread identity or generation")
	}
	return t, t.Config.Validate()
}
func CurrentThread(ctx context.Context, store *storage.Store) (*core.Thread, error) {
	body, exists, err := store.Get(ctx, ThreadKey)
	if err != nil || !exists {
		return nil, err
	}
	thread, err := decodeThread(body)
	return &thread, err
}

// InitializeThread atomically records the fixed initial configuration and empty
// model context. It never replaces an existing conversation or imports old files.
func InitializeThread(ctx context.Context, store *storage.Store, owner string, config core.ResolvedSpec) error {
	if !core.Identifier(owner) {
		return errors.New("invalid thread owner")
	}
	if err := config.Validate(); err != nil {
		return err
	}
	return storage.Transaction(ctx, store.DB(), func(tx *sql.Tx) error {
		var raw string
		err := tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key=?", ThreadKey).Scan(&raw)
		if err == nil {
			thread, err := decodeThread(raw)
			if err != nil {
				return err
			}
			if thread.Owner != owner {
				return errors.New("thread owner mismatch")
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		body, err := json.Marshal(core.Thread{Owner: owner, Generation: 1, Config: config})
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO metadata VALUES (?,?)", ThreadKey, string(body)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO metadata VALUES (?,?) ON CONFLICT(key) DO NOTHING", ContextKey, emptyContext); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO metadata VALUES ('agent.setup.pending','false') ON CONFLICT(key) DO UPDATE SET value='false'")
		return err
	})
}
func AssignInitial(ctx context.Context, store *storage.Store, config core.ResolvedSpec) error {
	if err := config.Validate(); err != nil {
		return err
	}
	return storage.Transaction(ctx, store.DB(), func(tx *sql.Tx) error {
		var raw, contextBody string
		if err := tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key=?", ThreadKey).Scan(&raw); err != nil {
			return err
		}
		thread, err := decodeThread(raw)
		if err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key=?", ContextKey).Scan(&contextBody); err != nil {
			return err
		}
		if err := storage.CheckContext(json.RawMessage(contextBody)); err != nil {
			return err
		}
		var checkpoint struct {
			Messages    []json.RawMessage `json:"messages"`
			LastRequest string            `json:"last_request"`
		}
		if err := json.Unmarshal([]byte(contextBody), &checkpoint); err != nil {
			return err
		}
		if len(checkpoint.Messages) != 0 || checkpoint.LastRequest != "" {
			return errors.New("cannot change a started task's initial configuration")
		}
		thread.Config = config
		body, err := json.Marshal(thread)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE metadata SET value=? WHERE key=?", string(body), ThreadKey)
		return err
	})
}

// ConfigureNextGeneration is a turn-boundary compare-and-set. Context and all
// earlier attribution remain unchanged. The caller must settle active work first.
func ConfigureNextGeneration(ctx context.Context, store *storage.Store, expected uint64, config core.ResolvedSpec) (core.Thread, error) {
	if err := config.Validate(); err != nil {
		return core.Thread{}, err
	}
	var thread core.Thread
	err := storage.Transaction(ctx, store.DB(), func(tx *sql.Tx) error {
		var body string
		if err := tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key=?", ThreadKey).Scan(&body); err != nil {
			return err
		}
		var err error
		thread, err = decodeThread(body)
		if err != nil {
			return err
		}
		if thread.Generation != expected || expected >= core.MaxSafeInteger {
			return errors.New("thread generation changed")
		}
		thread.Config = config
		thread.Generation++
		raw, err := json.Marshal(thread)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE metadata SET value=? WHERE key=?", string(raw), ThreadKey)
		return err
	})
	return thread, err
}
