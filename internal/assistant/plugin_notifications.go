package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

// BackgroundBinding is owner configuration; no plugin-supplied address is used.
type BackgroundBinding struct {
	// Diagnostic is optional host projection data, never plugin state or config.
	Diagnostic string          `json:"diagnostic,omitempty"`
	ID         string          `json:"id"`
	Enabled    bool            `json:"enabled"`
	Assistant  string          `json:"assistant"`
	Config     json.RawMessage `json:"config"`
	Revision   int64           `json:"revision"`
	State      json.RawMessage `json:"-"`
	Next       int64           `json:"next"`
	Status     string          `json:"status"`
	Queued     int             `json:"queued"`
	Attention  int             `json:"attention"`
}

func (m *Mailbox) Backgrounds(ctx context.Context) ([]BackgroundBinding, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT id,enabled,assistant,config,revision,state,next,status,diagnostic,
 (SELECT COUNT(*) FROM agent_messages WHERE json_extract(envelope,'$.plugin.identity.id')=p.id AND state='queued'),
 (SELECT COUNT(*) FROM agent_messages WHERE json_extract(envelope,'$.plugin.identity.id')=p.id AND state IN ('failed','interrupted')) FROM plugin_background p ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []BackgroundBinding{}
	for rows.Next() {
		var b BackgroundBinding
		var config, state []byte
		if err = rows.Scan(&b.ID, &b.Enabled, &b.Assistant, &config, &b.Revision, &state, &b.Next, &b.Status, &b.Diagnostic, &b.Queued, &b.Attention); err != nil {
			return nil, err
		}
		b.Config = config
		b.State = state
		out = append(out, b)
	}
	return out, rows.Err()
}

// EnsureBackground inserts defaults once. A persisted row, including an explicit
// disabled binding, always wins. No checkpoint or revision is changed on conflict.
func (m *Mailbox) EnsureBackground(ctx context.Context, id, target string) error {
	if !core.Identifier(id) || !core.Identifier(target) {
		return errors.New("invalid background defaults")
	}
	_, err := m.db.ExecContext(ctx, "INSERT INTO plugin_background(id,enabled,assistant,config,revision,state,next,status,conversation) VALUES(?,1,?,'{}',1,'null',0,'pending',?) ON CONFLICT(id) DO NOTHING", id, target, newConversation())
	return err
}

func (m *Mailbox) ConfigureBackground(ctx context.Context, b BackgroundBinding) error {
	if !core.Identifier(b.ID) || (b.Assistant != "" && !core.Identifier(b.Assistant)) || (b.Enabled && b.Assistant == "") || len(b.Config) > 16384 || !json.Valid(b.Config) {
		return errors.New("invalid background configuration")
	}
	return storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		var revision int64
		err := tx.QueryRowContext(ctx, "SELECT revision FROM plugin_background WHERE id=?", b.ID).Scan(&revision)
		if errors.Is(err, sql.ErrNoRows) {
			if b.Revision != 0 {
				return errors.New("stale plugin configuration")
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO plugin_background(id,enabled,assistant,config,revision,state,next,status,conversation) VALUES(?,?,?,?,1,'null',0,'disabled',?)", b.ID, b.Enabled, b.Assistant, string(b.Config), newConversation())
			return err
		}
		if err != nil {
			return err
		}
		if b.Revision != revision {
			return errors.New("stale plugin configuration")
		}
		// State belongs to the account, not the target. Retargeting cannot replay history.
		_, err = tx.ExecContext(ctx, "UPDATE plugin_background SET enabled=?,assistant=?,config=?,revision=revision+1 WHERE id=?", b.Enabled, b.Assistant, string(b.Config), b.ID)
		return err
	})
}

// CommitBackground is the trusted host adapter, never a plugin/model tool.
// The host supplies installed-package identity and owner routing; the plugin
// supplies only reference data. Like task-result hooks and Send, acceptance uses
// inbox.EnqueueTx, joining every event to the producer checkpoint atomically.
// A repeated ID with identical content is accepted; conflicting content rolls
// back the entire exchange, leaving the original cursor eligible for retry.
func (m *Mailbox) CommitBackground(ctx context.Context, b BackgroundBinding, identity core.PluginIdentity, r core.BackgroundResponse, now int64) error {
	if identity.ID != b.ID {
		return errors.New("plugin identity mismatch")
	}
	if err := r.Validate(); err != nil {
		return err
	}
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		var revision int64
		var enabled bool
		var owner, conversation string
		if err := tx.QueryRowContext(ctx, "SELECT revision,enabled,assistant,conversation FROM plugin_background WHERE id=?", b.ID).Scan(&revision, &enabled, &owner, &conversation); err != nil {
			return err
		}
		if revision != b.Revision || !enabled || owner != b.Assistant {
			return errors.New("stale background binding")
		}
		var queued int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM agent_messages WHERE json_extract(envelope,'$.plugin.identity.id')=? AND state='queued'`, b.ID).Scan(&queued); err != nil {
			return err
		}
		for _, event := range r.Events {
			id, _ := core.HashJSON([]string{m.nodeID, b.ID, event.ID})
			message := protocol.Envelope{Kind: "plugin_event", ID: id, From: protocol.AgentRef{NodeID: m.nodeID, Kind: "runtime", AgentID: "plugin-" + b.ID}, To: protocol.AgentRef{NodeID: m.nodeID, Kind: "assistant", AgentID: owner}, ConversationID: conversation, Plugin: &protocol.PluginNotification{Identity: identity, Event: event}}
			// Identity/version and owner are bound at first acceptance and kept on retries.
			old, err := findMail(ctx, tx, id)
			if err != nil {
				return err
			}
			if old != nil {
				if old.Plugin == nil || old.Plugin.Identity.ID != b.ID || old.Plugin.Event != event {
					return errors.New("conflicting plugin event")
				}
				continue
			}
			queued++
			if queued > 64 {
				return errors.New("plugin queue capacity exceeded")
			}
			if err = inbox.EnqueueTx(ctx, tx, message, "in", "queued"); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, "UPDATE plugin_background SET state=?,next=?,status=?,diagnostic='' WHERE id=?", string(r.State), now+int64(r.DelaySeconds)*1000, r.Status, b.ID)
		return err
	})
	if err == nil {
		m.Changes.Notify("assistant-mail")
	}
	return err
}
func (m *Mailbox) BackgroundError(ctx context.Context, b BackgroundBinding, status, diagnostic string, now int64) error {
	_, err := m.db.ExecContext(ctx, "UPDATE plugin_background SET next=?,status=?,diagnostic=? WHERE id=? AND revision=?", now+300000, status, diagnostic, b.ID, b.Revision)
	return err
}
