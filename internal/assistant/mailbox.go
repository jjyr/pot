package assistant

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/internal/inbox"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type Mailbox struct {
	db        *sql.DB
	controlID string
	Changes   core.Changes
}

func OpenMailbox(path, controlID string) (*Mailbox, error) {
	if !(protocol.AgentRef{ControlID: controlID, Kind: "assistant", AgentID: "identity"}).Valid() {
		return nil, errors.New("invalid Control identity")
	}
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	if err = inbox.Initialize(db); err != nil {
		db.Close()
		return nil, err
	}
	err = storage.Transaction(context.Background(), db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS assistant_mail_identity(singleton INTEGER PRIMARY KEY CHECK(singleton=1),control_id TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_mail_sessions(assistant TEXT NOT NULL,conversation TEXT NOT NULL,config TEXT NOT NULL,context TEXT,PRIMARY KEY(assistant,conversation));
CREATE TABLE IF NOT EXISTS assistant_mail_events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,message TEXT NOT NULL,event TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_mail_models(message TEXT NOT NULL,request TEXT NOT NULL,PRIMARY KEY(message,request));
CREATE TABLE IF NOT EXISTS assistant_contacts(control TEXT PRIMARY KEY,directory TEXT NOT NULL);`); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR IGNORE INTO assistant_mail_identity VALUES (1,?)", controlID); err != nil {
			return err
		}
		var existing string
		if err := tx.QueryRow("SELECT control_id FROM assistant_mail_identity WHERE singleton=1").Scan(&existing); err != nil {
			return err
		}
		if existing != controlID {
			return errors.New("mailbox belongs to another Control")
		}
		_, err := tx.Exec("UPDATE agent_messages SET state='interrupted',detail='Execution outcome requires attention after restart' WHERE state='processing'")
		return err
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Mailbox{db: db, controlID: controlID}, nil
}
func (m *Mailbox) Close() error      { return m.db.Close() }
func (m *Mailbox) ControlID() string { return m.controlID }
func (m *Mailbox) Register(ctx context.Context, directory protocol.Directory) error {
	if directory.ControlID == m.controlID || !(protocol.AgentRef{ControlID: directory.ControlID, Kind: "assistant", AgentID: "identity"}).Valid() {
		return errors.New("invalid peer Control")
	}
	if len(directory.Assistants) > 1024 {
		return errors.New("assistant directory too large")
	}
	seen := map[protocol.AgentRef]bool{}
	for _, assistant := range directory.Assistants {
		if err := ValidateName(assistant.Name); err != nil {
			return err
		}
		if !assistant.Address.Valid() || assistant.Address.Kind != "assistant" || assistant.Address.ControlID != directory.ControlID || seen[assistant.Address] {
			return errors.New("invalid assistant directory")
		}
		seen[assistant.Address] = true
	}
	body, err := json.Marshal(directory)
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, "INSERT INTO assistant_contacts VALUES (?,?) ON CONFLICT(control) DO UPDATE SET directory=excluded.directory WHERE assistant_contacts.directory!=excluded.directory", directory.ControlID, string(body))
	return err
}
func (m *Mailbox) Contacts(ctx context.Context) ([]protocol.MailAssistant, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT directory FROM assistant_contacts ORDER BY control")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.MailAssistant{}
	for rows.Next() {
		var body []byte
		var directory protocol.Directory
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := protocol.DecodeStrict(body, &directory); err != nil {
			return nil, err
		}
		out = append(out, directory.Assistants...)
	}
	return out, rows.Err()
}

type mailQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func findMail(ctx context.Context, db mailQuery, id string) (*protocol.Envelope, error) {
	var body []byte
	err := db.QueryRowContext(ctx, "SELECT envelope FROM agent_messages WHERE id=?", id).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var message protocol.Envelope
	if err := protocol.DecodeStrict(body, &message); err != nil {
		return nil, err
	}
	return &message, nil
}
func insertMail(ctx context.Context, tx *sql.Tx, message protocol.Envelope, direction string) error {
	return inbox.EnqueueTx(ctx, tx, message, direction, "queued")
}

func sameOptional(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func sameEnvelope(a, b protocol.Envelope) bool {
	return a.Kind == b.Kind && a.RequestID == b.RequestID && sameJSONResult(a.Result, b.Result) && a.ID == b.ID && a.From == b.From && a.To == b.To && a.ConversationID == b.ConversationID && sameOptional(a.ReplyTo, b.ReplyTo) && a.Text == b.Text && a.Hops == b.Hops
}
func newConversation() string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	value[6] = value[6]&0x0f | 0x40
	value[8] = value[8]&0x3f | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:]
}

// Send receives sender identity only from the owning adapter. parent is the
// currently processed inbound message, including for a new conversation, so a
// model cannot escape the automatic hop bound by omitting reply_to.
func (m *Mailbox) Send(ctx context.Context, id string, from protocol.AgentRef, send protocol.SendMail, parent *protocol.Envelope) (protocol.Envelope, error) {
	if from.ControlID != m.controlID || from == send.To {
		return protocol.Envelope{}, errors.New("invalid sender or self-message")
	}
	var message protocol.Envelope
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		old, err := findMail(ctx, tx, id)
		if err != nil {
			return err
		}
		if old != nil {
			if old.From != from || old.To != send.To || old.Text != send.Text || !sameOptional(old.ReplyTo, send.ReplyTo) {
				return errors.New("message ID reused with different content")
			}
			message = *old
			return nil
		}
		var reply *protocol.Envelope
		if send.ReplyTo != nil {
			reply, err = findMail(ctx, tx, *send.ReplyTo)
			if err != nil {
				return err
			}
			if reply == nil {
				return errors.New("reply message missing")
			}
			if reply.To != from || reply.From != send.To {
				return errors.New("reply participants do not match")
			}
		}
		hops := uint32(0)
		if parent != nil {
			stored, err := findMail(ctx, tx, parent.ID)
			if err != nil {
				return err
			}
			if stored == nil || !sameEnvelope(*stored, *parent) || parent.To != from {
				return errors.New("invalid processing parent binding")
			}
			hops = parent.Hops + 1
		}
		conversation := newConversation()
		if reply != nil {
			hops = max(hops, reply.Hops+1)
			conversation = reply.ConversationID
		}
		message = protocol.Envelope{Kind: "message", ID: id, From: from, To: send.To, ConversationID: conversation, ReplyTo: send.ReplyTo, Text: send.Text, Hops: hops}
		if err := message.Validate(); err != nil {
			return err
		}
		direction := "out"
		if message.To.ControlID == m.controlID {
			direction = "in"
		}
		return insertMail(ctx, tx, message, direction)
	})
	if err == nil {
		m.Changes.Notify("assistant-mail")
	}
	return message, err
}
func (m *Mailbox) Deliver(ctx context.Context, message protocol.Envelope) (protocol.Receipt, error) {
	if err := message.Validate(); err != nil {
		return protocol.Receipt{}, err
	}
	if message.Kind != "message" || message.From.Kind != "assistant" || message.To.Kind != "assistant" || message.To.ControlID != m.controlID || message.From.ControlID == m.controlID {
		return protocol.Receipt{}, errors.New("invalid remote delivery")
	}
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		old, err := findMail(ctx, tx, message.ID)
		if err != nil {
			return err
		}
		if old != nil {
			if !sameEnvelope(*old, message) {
				return errors.New("conflicting delivery")
			}
			return nil
		}
		if message.ReplyTo != nil {
			parent, err := findMail(ctx, tx, *message.ReplyTo)
			if err != nil {
				return err
			}
			if parent == nil || parent.From != message.To || parent.To != message.From || parent.ConversationID != message.ConversationID || message.Hops <= parent.Hops {
				return errors.New("invalid reply binding")
			}
		} else {
			var exists bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM agent_messages WHERE conversation=?)", message.ConversationID).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return errors.New("conversation already exists")
			}
		}
		return insertMail(ctx, tx, message, "in")
	})
	if err == nil {
		m.Changes.Notify("assistant-mail")
	}
	return protocol.Receipt{Message: message}, err
}
func (m *Mailbox) Pending(ctx context.Context, target string) ([]protocol.Envelope, error) {
	return m.envelopes(ctx, "SELECT envelope FROM agent_messages WHERE direction='out' AND state='queued' AND json_extract(envelope,'$.to.control_id')=? ORDER BY sequence LIMIT ?", target, protocol.MailPageSize)
}
func (m *Mailbox) Acknowledge(ctx context.Context, receipt protocol.Receipt) error {
	return storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		old, err := findMail(ctx, tx, receipt.Message.ID)
		if err != nil {
			return err
		}
		if old == nil || !sameEnvelope(*old, receipt.Message) {
			return errors.New("receipt does not match immutable message")
		}
		if receipt.Message.From.ControlID != m.controlID || receipt.Message.To.ControlID == m.controlID {
			return errors.New("not an outbound message")
		}
		_, err = tx.ExecContext(ctx, "UPDATE agent_messages SET state='delivered' WHERE id=? AND direction='out'", receipt.Message.ID)
		return err
	})
}
func (m *Mailbox) Next(ctx context.Context, recipient string) (*protocol.Envelope, error) {
	values, err := m.Candidates(ctx, recipient)
	if err != nil || len(values) == 0 {
		return nil, err
	}
	return &values[0], nil
}
func (m *Mailbox) envelopes(ctx context.Context, query string, args ...any) ([]protocol.Envelope, error) {
	rows, err := m.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.Envelope{}
	for rows.Next() {
		var body []byte
		var envelope protocol.Envelope
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := protocol.DecodeStrict(body, &envelope); err != nil {
			return nil, err
		}
		out = append(out, envelope)
	}
	return out, rows.Err()
}
func (m *Mailbox) Conversation(ctx context.Context, assistant, conversation string) ([]protocol.Envelope, error) {
	return m.envelopes(ctx, `SELECT envelope FROM agent_messages WHERE json_extract(envelope,'$.kind')='message' AND conversation=? AND ((json_extract(envelope,'$.from.control_id')=? AND json_extract(envelope,'$.from.kind')='assistant' AND json_extract(envelope,'$.from.agent_id')=?) OR (json_extract(envelope,'$.to.control_id')=? AND json_extract(envelope,'$.to.kind')='assistant' AND recipient=?)) ORDER BY sequence LIMIT 32`, conversation, m.controlID, assistant, m.controlID, assistant)
}
func (m *Mailbox) History(ctx context.Context, assistant string, after int64) ([]protocol.MailRecord, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT sequence,envelope,state,detail FROM agent_messages WHERE json_extract(envelope,'$.kind')='message' AND sequence>? AND ((json_extract(envelope,'$.from.control_id')=? AND json_extract(envelope,'$.from.kind')='assistant' AND json_extract(envelope,'$.from.agent_id')=?) OR (json_extract(envelope,'$.to.control_id')=? AND json_extract(envelope,'$.to.kind')='assistant' AND recipient=?)) ORDER BY sequence LIMIT ?`, after, m.controlID, assistant, m.controlID, assistant, protocol.MailPageSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []protocol.MailRecord{}
	for rows.Next() {
		var record protocol.MailRecord
		var body []byte
		var detail sql.NullString
		if err := rows.Scan(&record.Sequence, &body, &record.State, &detail); err != nil {
			return nil, err
		}
		if err := protocol.DecodeStrict(body, &record.Message); err != nil {
			return nil, err
		}
		if detail.Valid {
			record.Detail = &detail.String
		}
		out = append(out, record)
	}
	return out, rows.Err()
}
func (m *Mailbox) Claim(ctx context.Context, id string) error {
	message, err := findMail(ctx, m.db, id)
	if err != nil {
		return err
	}
	if message == nil {
		return errors.New("mail missing")
	}
	_, err = m.ClaimMailBatch(ctx, *message)
	return err
}
func (m *Mailbox) Finish(ctx context.Context, id string, success bool, detail string) error {
	state := "failed"
	if success {
		state = "processed"
	}
	return m.FinishAttempt(ctx, id, state, detail)
}
func (m *Mailbox) FinishAttempt(ctx context.Context, id, state, detail string) error {
	if state != "processed" && state != "failed" && state != "interrupted" {
		return errors.New("invalid mail terminal state")
	}
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error {
		var previous string
		var oldDetail sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT state,detail FROM agent_messages WHERE id=?", id).Scan(&previous, &oldDetail); err != nil {
			return err
		}
		if previous == state && oldDetail.Valid && oldDetail.String == detail {
			return nil
		}
		if previous != "processing" {
			return errors.New("mail attempt is no longer processing")
		}
		_, err := tx.ExecContext(ctx, "UPDATE agent_messages SET state=?,detail=? WHERE (id=? OR id IN (SELECT message FROM agent_delivery_items WHERE batch=?)) AND state='processing'", state, detail, id, id)
		return err
	})
	if err == nil {
		m.Changes.Notify("assistant-mail")
	}
	return err
}

func sameJSONResult(a, b *protocol.TaskResultEvent) bool {
	left, _ := json.Marshal(a)
	right, _ := json.Marshal(b)
	return string(left) == string(right)
}

// LatestTaskInput binds outgoing worker messages to the last input actually
// available to that run. Pending, unconsumed reference messages are excluded.
func (m *Mailbox) LatestTaskInput(ctx context.Context, task, run string) (*protocol.Envelope, error) {
	values, err := m.envelopes(ctx, `SELECT envelope FROM agent_messages WHERE recipient=? AND conversation=? AND json_extract(envelope,'$.to.kind')='task' AND state IN ('active','consumed') ORDER BY sequence DESC LIMIT 1`, task, run)
	if err != nil || len(values) == 0 {
		return nil, err
	}
	return &values[0], nil
}
