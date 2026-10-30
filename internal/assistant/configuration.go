package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

const configurationKey = "assistant.configuration.v1"

type configurationState struct {
	Revision    uint64             `json:"revision"`
	Intent      json.RawMessage    `json:"intent"`
	PendingSpec *core.ResolvedSpec `json:"pending_spec"`
}
type ConfigurationState struct {
	Owner      string             `json:"owner"`
	Generation uint64             `json:"generation"`
	Config     *core.ResolvedSpec `json:"config"`
	Revision   uint64             `json:"revision"`
	Intent     json.RawMessage    `json:"intent"`
	Pending    bool               `json:"pending"`
}
type ConfigurationChange struct {
	RequestID, InputHash                 string
	ExpectedGeneration, ExpectedRevision uint64
	Spec                                 core.ResolvedSpec
	Intent                               json.RawMessage
}

func (c *Conversation) configurationLocked(ctx context.Context) (configurationState, error) {
	var value configurationState
	raw, exists, err := c.store.Get(ctx, configurationKey)
	if err != nil || !exists {
		return value, err
	}
	err = core.DecodeJSON([]byte(raw), &value)
	return value, err
}
func (c *Conversation) configurationViewLocked(state configurationState) (ConfigurationState, error) {
	result := ConfigurationState{Owner: c.options.Owner, Revision: state.Revision, Intent: state.Intent, Pending: state.PendingSpec != nil}
	if c.thread != nil {
		result.Generation = c.thread.Generation
		result.Config = &c.thread.Config
	}
	body, err := json.Marshal(result)
	if err != nil {
		return result, err
	}
	var copy ConfigurationState
	err = core.DecodeJSON(body, &copy)
	return copy, err
}
func (c *Conversation) Configuration(ctx context.Context) (ConfigurationState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, err := c.configurationLocked(ctx)
	if err != nil {
		return ConfigurationState{}, err
	}
	return c.configurationViewLocked(value)
}

// NextConfiguration snapshots editable intent together with the configuration
// which the next accepted turn will inherit, including a pending UI change.
func (c *Conversation) NextConfiguration(ctx context.Context) (ConfigurationState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state, err := c.configurationLocked(ctx)
	if err != nil {
		return ConfigurationState{}, err
	}
	view, err := c.configurationViewLocked(state)
	if err != nil {
		return view, err
	}
	if state.PendingSpec != nil {
		view.Config = state.PendingSpec
	}
	return view, nil
}

func (c *Conversation) configurationReceiptLocked(ctx context.Context, id, hash string) (*ConfigurationState, error) {
	var oldHash, body string
	err := c.store.DB().QueryRowContext(ctx, "SELECT input_hash,response FROM assistant_configuration_requests WHERE id=?", id).Scan(&oldHash, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if oldHash != hash {
		return nil, errors.New("configuration request identity reused with different input")
	}
	var result ConfigurationState
	if err = core.DecodeJSON([]byte(body), &result); err != nil {
		return nil, err
	}
	return &result, nil
}
func (c *Conversation) ConfigurationReceipt(ctx context.Context, id, hash string) (*ConfigurationState, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.configurationReceiptLocked(ctx, id, hash)
}
func writeConfiguration(ctx context.Context, tx *sql.Tx, state configurationState) error {
	body, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO metadata VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", configurationKey, string(body))
	return err
}
func writeConfiguredThread(ctx context.Context, tx *sql.Tx, thread core.Thread) error {
	body, err := json.Marshal(thread)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO metadata VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", ThreadKey, string(body)); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO metadata VALUES (?,?)", ContextKey, emptyContext); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO metadata VALUES ('agent.setup.pending','false') ON CONFLICT(key) DO UPDATE SET value='false'")
	return err
}

// QueueConfiguration records editable intent and a resolved immutable pending
// copy together. Active/waiting executions retain their existing generation/spec.
// An unchanged retry returns its original durable receipt before checking CAS.
func (c *Conversation) QueueConfiguration(ctx context.Context, change ConfigurationChange) (ConfigurationState, error) {
	if !core.Identifier(change.RequestID) || len(change.InputHash) != 64 || !json.Valid(change.Intent) {
		return ConfigurationState{}, errors.New("invalid configuration request identity or intent")
	}
	if err := change.Spec.Validate(); err != nil {
		return ConfigurationState{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ConfigurationState{}, errors.New("conversation closed")
	}
	if old, err := c.configurationReceiptLocked(ctx, change.RequestID, change.InputHash); err != nil {
		return ConfigurationState{}, err
	} else if old != nil {
		return *old, nil
	}
	state, err := c.configurationLocked(ctx)
	if err != nil {
		return ConfigurationState{}, err
	}
	generation := uint64(0)
	if c.thread != nil {
		generation = c.thread.Generation
	}
	if generation != change.ExpectedGeneration || state.Revision != change.ExpectedRevision {
		return ConfigurationState{}, errors.New("assistant configuration changed; reload before saving")
	}
	if state.Revision >= core.MaxSafeInteger || generation >= core.MaxSafeInteger {
		return ConfigurationState{}, errors.New("configuration generation limit reached")
	}
	raw, _ := json.Marshal(change.Spec)
	var spec core.ResolvedSpec
	if err = core.DecodeJSON(raw, &spec); err != nil {
		return ConfigurationState{}, err
	}
	state.Revision++
	state.Intent = append(json.RawMessage(nil), change.Intent...)
	state.PendingSpec = &spec
	var nextThread *core.Thread
	if c.turn == nil && !c.blocked {
		nextThread = &core.Thread{Owner: c.options.Owner, Generation: generation + 1, Config: spec}
		state.PendingSpec = nil
	}
	previous := c.thread
	if nextThread != nil {
		c.thread = nextThread
	}
	view, err := c.configurationViewLocked(state)
	c.thread = previous
	if err != nil {
		return ConfigurationState{}, err
	}
	err = storage.Transaction(ctx, c.store.DB(), func(tx *sql.Tx) error {
		if nextThread != nil {
			if err := writeConfiguredThread(ctx, tx, *nextThread); err != nil {
				return err
			}
		}
		if err := writeConfiguration(ctx, tx, state); err != nil {
			return err
		}
		body, err := json.Marshal(view)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO assistant_configuration_requests VALUES (?,?,?)", change.RequestID, change.InputHash, string(body))
		return err
	})
	if err != nil {
		return ConfigurationState{}, err
	}
	if nextThread != nil {
		c.thread = nextThread
	}
	if err = c.appendTurnLocked(ctx); err != nil {
		return ConfigurationState{}, err
	}
	return view, nil
}

// Called only before accepting a fresh turn. Resuming/queued/running turns never
// enter this path and their provider, credentials, parameters and spec stay fixed.
func (c *Conversation) applyConfigurationBoundaryLocked(ctx context.Context) error {
	if c.turn != nil || c.blocked {
		return nil
	}
	state, err := c.configurationLocked(ctx)
	if err != nil {
		return err
	}
	base := c.thread
	if state.PendingSpec != nil {
		generation := uint64(0)
		if base != nil {
			generation = base.Generation
		}
		base = &core.Thread{Owner: c.options.Owner, Generation: generation, Config: *state.PendingSpec}
	}
	if base == nil {
		return nil
	}
	spec := base.Config
	if c.options.RefreshConfiguration != nil {
		next, err := c.options.RefreshConfiguration(ctx, *base, append(json.RawMessage(nil), state.Intent...))
		if err != nil {
			return err
		}
		if next != nil {
			spec = *next
		}
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	if state.PendingSpec == nil && c.thread != nil && c.thread.Config.SpecHash == spec.SpecHash {
		return nil
	}
	generation := uint64(0)
	if c.thread != nil {
		generation = c.thread.Generation
	}
	if generation >= core.MaxSafeInteger {
		return errors.New("configuration generation limit reached")
	}
	next := core.Thread{Owner: c.options.Owner, Generation: generation + 1, Config: spec}
	state.PendingSpec = nil
	if err = storage.Transaction(ctx, c.store.DB(), func(tx *sql.Tx) error {
		if err := writeConfiguredThread(ctx, tx, next); err != nil {
			return err
		}
		return writeConfiguration(ctx, tx, state)
	}); err != nil {
		return err
	}
	c.thread = &next
	return nil
}
