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
	db      *sql.DB
	nodeID  string
	options MailboxOptions
	Changes core.Changes
}

type MailboxOptions struct {
	// OwnerAvailable runs inside the inbox writer transaction for new local
	// assistant input. Retirement must take the same database writer lock.
	// Exact retries and direct-user reply projections retain their receipts.
	OwnerAvailable func(context.Context, string) error
}

// OpenMailbox borrows an already migrated Tasks database. The caller owns Close.
func OpenMailbox(db *sql.DB, nodeID string, options MailboxOptions) (*Mailbox, error) {
	if !(protocol.AgentRef{NodeID: nodeID, Kind: "assistant", AgentID: "identity"}).Valid() {
		return nil, errors.New("invalid Control identity")
	}
	if db == nil {
		return nil, errors.New("mailbox requires a Tasks database")
	}
	err := storage.Transaction(context.Background(), db, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT OR IGNORE INTO assistant_mail_identity VALUES (1,?)", nodeID); err != nil {
			return err
		}
		var existing string
		if err := tx.QueryRow("SELECT node_id FROM assistant_mail_identity WHERE singleton=1").Scan(&existing); err != nil {
			return err
		}
		if existing != nodeID {
			return errors.New("mailbox belongs to another Control")
		}
		_, err := tx.Exec("UPDATE agent_messages SET state='interrupted',detail='Execution outcome requires attention after restart' WHERE state='processing'")
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Mailbox{db: db, nodeID: nodeID, options: options}, nil
}
func (m *Mailbox) NodeID() string { return m.nodeID }
func (m *Mailbox) Register(ctx context.Context, directory protocol.Directory) error {
	if directory.NodeID == m.nodeID || !(protocol.AgentRef{NodeID: directory.NodeID, Kind: "assistant", AgentID: "identity"}).Valid() {
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
		if !assistant.Address.Valid() || assistant.Address.Kind != "assistant" || assistant.Address.NodeID != directory.NodeID || seen[assistant.Address] {
			return errors.New("invalid assistant directory")
		}
		seen[assistant.Address] = true
	}
	body, err := json.Marshal(directory)
	if err != nil {
		return err
	}
	_, err = m.db.ExecContext(ctx, "INSERT INTO assistant_contacts VALUES (?,?) ON CONFLICT(node) DO UPDATE SET directory=excluded.directory WHERE assistant_contacts.directory!=excluded.directory", directory.NodeID, string(body))
	return err
}
func (m *Mailbox) Contacts(ctx context.Context) ([]protocol.MailAssistant, error) {
	rows, err := m.db.QueryContext(ctx, "SELECT directory FROM assistant_contacts ORDER BY node")
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
	return samePlugin(a.Plugin, b.Plugin) && a.Kind == b.Kind && a.RequestID == b.RequestID && sameJSONResult(a.Result, b.Result) && a.ID == b.ID && a.From == b.From && a.To == b.To && a.ConversationID == b.ConversationID && sameOptional(a.ReplyTo, b.ReplyTo) && a.Text == b.Text && a.Hops == b.Hops
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
// currently processed inbound message. Replies to its sender are linked
// automatically; forwarding to another agent retains the causal hop bound.
func (m *Mailbox) Send(ctx context.Context, id string, from protocol.AgentRef, send protocol.SendMail, parent *protocol.Envelope) (message protocol.Envelope, err error) {
	err = storage.Transaction(ctx, m.db, func(tx *sql.Tx) error { var e error; message, e = m.SendTx(ctx, tx, id, from, send, parent); return e })
	if err == nil {
		m.Changes.Notify("assistant-mail")
	}
	return
}

// SendTx retains the existing immutable envelope and processing-parent fences
// while allowing an authenticated gateway receipt to share its transaction.
// Callers notify Changes only after their transaction commits.
func (m *Mailbox) SendTx(ctx context.Context, tx *sql.Tx, id string, from protocol.AgentRef, send protocol.SendMail, parent *protocol.Envelope) (message protocol.Envelope, err error) {
	return m.sendTx(ctx, tx, id, from, send, parent, false)
}

// ReceiveExecutionTx accepts only a gateway-authenticated remote execution. The
// gateway verifies its persisted current claim/run; this method preserves the
// Mailbox transaction, causal parent, immutable envelope and duplicate fence.
func (m *Mailbox) ReceiveExecutionTx(ctx context.Context, tx *sql.Tx, id string, from protocol.AgentRef, send protocol.SendMail, parent *protocol.Envelope) (protocol.Envelope, error) {
	if from.NodeID == m.nodeID || (from.Kind != "assistant" && from.Kind != "task") || (send.To.Kind != "assistant" && !(send.To.Kind == "user" && from.Kind == "assistant" && parent != nil && parent.From == send.To)) {
		return protocol.Envelope{}, errors.New("invalid remote execution delivery")
	}
	return m.sendTx(ctx, tx, id, from, send, parent, true)
}
func (m *Mailbox) sendTx(ctx context.Context, tx *sql.Tx, id string, from protocol.AgentRef, send protocol.SendMail, parent *protocol.Envelope, supervised bool) (message protocol.Envelope, err error) {
	if (!supervised && from.NodeID != m.nodeID) || from == send.To {
		return message, errors.New("invalid sender or self-message")
	}
	err = func() error {
		var replyTo *string
		if parent != nil && send.To == parent.From {
			reply := parent.ID
			replyTo = &reply
		}
		old, err := findMail(ctx, tx, id)
		if err != nil {
			return err
		}
		if old != nil {
			if old.From != from || old.To != send.To || old.Text != send.Text || !sameOptional(old.ReplyTo, replyTo) {
				return errors.New("message ID reused with different content")
			}
			message = *old
			return nil
		}
		hops := uint32(0)
		conversation := newConversation()
		if parent != nil {
			stored, err := findMail(ctx, tx, parent.ID)
			if err != nil {
				return err
			}
			if stored == nil || !sameEnvelope(*stored, *parent) || parent.To != from {
				return errors.New("invalid processing parent binding")
			}
			if from.Kind == "assistant" && !supervised {
				// Fence and enqueue share the writer transaction. Completion cannot
				// retire this parent between the check and durable acceptance.
				var state, direction string
				if err := tx.QueryRowContext(ctx, "SELECT state,direction FROM agent_messages WHERE id=?", parent.ID).Scan(&state, &direction); err != nil {
					return err
				}
				if state != "processing" || direction != "in" {
					return errors.New("mail processing attempt is fenced")
				}
			}
			hops, err = protocol.NextMailHop(parent.Hops)
			if err != nil {
				return err
			}
			if replyTo != nil {
				conversation = parent.ConversationID
			}
		}
		message = protocol.Envelope{Kind: "message", ID: id, From: from, To: send.To, ConversationID: conversation, ReplyTo: replyTo, Text: send.Text, Hops: hops}
		if err := message.Validate(); err != nil {
			return err
		}
		direction := "out"
		if message.To.NodeID == m.nodeID {
			direction = "in"
			if message.To.Kind == "assistant" && m.options.OwnerAvailable != nil {
				if err := m.options.OwnerAvailable(ctx, message.To.AgentID); err != nil {
					return err
				}
			}
		}
		return insertMail(ctx, tx, message, direction)
	}()
	return
}
func (m *Mailbox) Deliver(ctx context.Context, message protocol.Envelope) (protocol.Receipt, error) {
	return m.deliver(ctx, message, "queued")
}

// DeliverChat durably accepts a direct-user reply without making it eligible
// for autonomous agent consumption. Projection failure can retry the same receipt.
func (m *Mailbox) DeliverChat(ctx context.Context, message protocol.Envelope) (protocol.Receipt, error) {
	return m.deliver(ctx, message, "processed")
}

func (m *Mailbox) deliver(ctx context.Context, message protocol.Envelope, state string) (protocol.Receipt, error) {
	err := storage.Transaction(ctx, m.db, func(tx *sql.Tx) error { return m.deliverTx(ctx, tx, message, state) })
	if err == nil {
		m.Changes.Notify("assistant-mail")
	}
	return protocol.Receipt{Message: message}, err
}

// DeliverTx keeps authenticated node mail and its transport receipt in the same
// Tasks transaction. It retains the existing remote reply and mailbox fences.
func (m *Mailbox) DeliverTx(ctx context.Context, tx *sql.Tx, message protocol.Envelope) error {
	return m.deliverTx(ctx, tx, message, "queued")
}

// DeliverChatTx records an exact direct-user reply without scheduling it as
// autonomous assistant input. Authentication and parent ownership stay with the
// calling gateway; the existing remote reply validation remains mandatory.
func (m *Mailbox) DeliverChatTx(ctx context.Context, tx *sql.Tx, message protocol.Envelope) error {
	return m.deliverTx(ctx, tx, message, "processed")
}
func (m *Mailbox) deliverTx(ctx context.Context, tx *sql.Tx, message protocol.Envelope, state string) error {
	if err := message.Validate(); err != nil {
		return err
	}
	assistantDelivery := message.To.Kind == "assistant" && (message.From.Kind == "assistant" || message.From.Kind == "user")
	userReply := state == "processed" && message.To.Kind == "user" && message.From.Kind == "assistant" && message.ReplyTo != nil
	if message.Kind != "message" || !(assistantDelivery || userReply) || message.To.NodeID != m.nodeID || message.From.NodeID == m.nodeID {
		return errors.New("invalid remote delivery")
	}

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
	if state == "queued" && m.options.OwnerAvailable != nil {
		if err := m.options.OwnerAvailable(ctx, message.To.AgentID); err != nil {
			return err
		}
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
	return inbox.EnqueueTx(ctx, tx, message, "in", state)
}

func (m *Mailbox) Pending(ctx context.Context, target string) ([]protocol.Envelope, error) {
	return m.envelopes(ctx, "SELECT envelope FROM agent_messages WHERE direction='out' AND state='queued' AND json_extract(envelope,'$.to.node_id')=? ORDER BY sequence LIMIT ?", target, protocol.MailPageSize)
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
		if receipt.Message.From.NodeID != m.nodeID || receipt.Message.To.NodeID == m.nodeID {
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
	return m.envelopes(ctx, `SELECT envelope FROM agent_messages WHERE json_extract(envelope,'$.kind')='message' AND conversation=? AND ((json_extract(envelope,'$.from.node_id')=? AND json_extract(envelope,'$.from.kind')='assistant' AND json_extract(envelope,'$.from.agent_id')=?) OR (json_extract(envelope,'$.to.node_id')=? AND json_extract(envelope,'$.to.kind')='assistant' AND recipient=?)) ORDER BY sequence LIMIT 32`, conversation, m.nodeID, assistant, m.nodeID, assistant)
}
func (m *Mailbox) History(ctx context.Context, assistant string, after int64) ([]protocol.MailRecord, error) {
	rows, err := m.db.QueryContext(ctx, `SELECT sequence,envelope,state,detail FROM agent_messages WHERE json_extract(envelope,'$.kind')='message' AND sequence>? AND ((json_extract(envelope,'$.from.node_id')=? AND json_extract(envelope,'$.from.kind')='assistant' AND json_extract(envelope,'$.from.agent_id')=?) OR (json_extract(envelope,'$.to.node_id')=? AND json_extract(envelope,'$.to.kind')='assistant' AND recipient=?)) ORDER BY sequence LIMIT ?`, after, m.nodeID, assistant, m.nodeID, assistant, protocol.MailPageSize)
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

func samePlugin(a, b *protocol.PluginNotification) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}
