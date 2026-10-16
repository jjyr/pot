package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/protocol"
)

func ID() string { return rand.Text() }
func Now() int64 { return time.Now().UnixMilli() }

type Message = protocol.Message
type Event = protocol.Event
type Notification struct {
	Sequence int64   `json:"sequence,string"`
	Message  Message `json:"message"`
}
type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	db, err := OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	err = Transaction(context.Background(), db, func(tx *sql.Tx) error {
		var version int
		if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version > 1 {
			return errors.New("database schema is newer than this runtime")
		}
		_, err := tx.Exec(`CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,request_id TEXT UNIQUE,body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS events(cursor INTEGER PRIMARY KEY AUTOINCREMENT,body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS metadata(key TEXT PRIMARY KEY,value TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS main_message_outbox(sequence INTEGER PRIMARY KEY AUTOINCREMENT,message_id TEXT UNIQUE NOT NULL,body TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS context_history(sequence INTEGER PRIMARY KEY AUTOINCREMENT,classification TEXT NOT NULL,body TEXT NOT NULL,at INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS mail(scope TEXT NOT NULL,id TEXT NOT NULL,body TEXT NOT NULL,state TEXT NOT NULL,sequence INTEGER PRIMARY KEY AUTOINCREMENT,UNIQUE(scope,id));
PRAGMA user_version=1;`)
		return err
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	// Reopening never replays uncertain model/tool effects. Preserve partial text.
	messages, err := s.Messages(context.Background())
	if err == nil {
		for _, message := range messages {
			if message.Status == "accepted" || message.Status == "streaming" {
				message.Status = "failed"
				if _, err = s.Save(context.Background(), message); err != nil {
					break
				}
			}
		}
	}
	if err == nil {
		_, err = db.Exec("UPDATE mail SET state='interrupted' WHERE state='processing'")
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }

// DB permits a domain owner to add its own tables/transactions without putting
// business logic in storage. Never hold an active transaction and call Store methods.
func (s *Store) DB() *sql.DB { return s.db }
func (s *Store) Get(ctx context.Context, key string) (string, bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key=?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return value, err == nil, err
}
func (s *Store) Set(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, "INSERT INTO metadata VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value)
	return err
}
func (s *Store) Messages(ctx context.Context) ([]Message, error) {
	return s.messages(ctx, "SELECT body FROM messages ORDER BY rowid")
}
func (s *Store) TaskReports(ctx context.Context) ([]Message, error) {
	return s.messages(ctx, "SELECT body FROM messages WHERE id LIKE 'task-report-%' ORDER BY rowid")
}
func (s *Store) messages(ctx context.Context, query string, args ...any) ([]Message, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var body []byte
		var m Message
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := protocol.DecodeStrict(body, &m); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) one(ctx context.Context, query string, args ...any) (*Message, error) {
	values, err := s.messages(ctx, query, args...)
	if err != nil || len(values) == 0 {
		return nil, err
	}
	return &values[0], nil
}
func (s *Store) Message(ctx context.Context, id string) (*Message, error) {
	return s.one(ctx, "SELECT body FROM messages WHERE id=?", id)
}
func (s *Store) Duplicate(ctx context.Context, request string) (*Message, error) {
	return s.one(ctx, "SELECT body FROM messages WHERE request_id=?", request)
}
func (s *Store) LastCompletedReply(ctx context.Context) (*Message, error) {
	return s.one(ctx, "SELECT body FROM messages WHERE json_extract(body,'$.role')='assistant' AND json_extract(body,'$.status')='complete' AND trim(json_extract(body,'$.text'))<>'' ORDER BY rowid DESC LIMIT 1")
}
func (s *Store) Cursor(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(cursor),0) FROM events").Scan(&n)
	return n, err
}
func (s *Store) Events(ctx context.Context, after int64) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT cursor,body FROM events WHERE cursor>? ORDER BY cursor", after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Event{}
	for rows.Next() {
		var n int64
		var body []byte
		var e Event
		if err := rows.Scan(&n, &body); err != nil {
			return nil, err
		}
		if err := protocol.DecodeStrict(body, &e); err != nil {
			return nil, err
		}
		e.Cursor = &n
		out = append(out, e)
	}
	return out, rows.Err()
}
func (s *Store) Save(ctx context.Context, m Message) (Event, error) {
	return s.save(ctx, m, nil, true, nil)
}
func (s *Store) SaveWithMetadata(ctx context.Context, m Message, metadata map[string]string) (Event, error) {
	return s.save(ctx, m, metadata, true, nil)
}
func (s *Store) SaveNonNotifying(ctx context.Context, m Message) (Event, error) {
	return s.save(ctx, m, nil, false, nil)
}

// SaveWithMutation commits the message projection/event with owner-local journal
// state in the same database transaction. The mutation must not open another
// transaction or perform external effects.
func (s *Store) SaveWithMutation(ctx context.Context, m Message, mutation func(*sql.Tx) error) (Event, error) {
	return s.save(ctx, m, nil, true, mutation)
}

// AppendEvent atomically persists a non-message state transition and its owner
// metadata. Message projections must use Save so projection/outbox/event agree.
func (s *Store) AppendEvent(ctx context.Context, event Event, metadata map[string]string) (Event, error) {
	if event.Type == "message" {
		return Event{}, errors.New("message events require Save")
	}
	event.EventID = ID()
	event.Cursor = nil
	if err := event.Validate(); err != nil {
		return Event{}, err
	}
	body, err := json.Marshal(event)
	if err != nil {
		return Event{}, err
	}
	err = Transaction(ctx, s.db, func(tx *sql.Tx) error {
		for key, value := range metadata {
			if _, err := tx.ExecContext(ctx, "INSERT INTO metadata VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value); err != nil {
				return err
			}
		}
		result, err := tx.ExecContext(ctx, "INSERT INTO events(body) VALUES (?)", string(body))
		if err != nil {
			return err
		}
		cursor, err := result.LastInsertId()
		event.Cursor = &cursor
		return err
	})
	if err != nil {
		return Event{}, err
	}
	return event, nil
}
func (s *Store) save(ctx context.Context, m Message, metadata map[string]string, notify bool, mutation func(*sql.Tx) error) (Event, error) {
	if err := m.Validate(); err != nil {
		return Event{}, err
	}
	body, err := json.Marshal(m)
	if err != nil {
		return Event{}, err
	}
	e := Event{EventID: ID(), Type: "message", Message: &m}
	err = Transaction(ctx, s.db, func(tx *sql.Tx) error {
		if mutation != nil {
			if err := mutation(tx); err != nil {
				return err
			}
		}
		var old []byte
		err := tx.QueryRowContext(ctx, "SELECT body FROM messages WHERE id=?", m.ID).Scan(&old)
		if err == nil {
			var previous Message
			if err := protocol.DecodeStrict(old, &previous); err != nil {
				return err
			}
			if previous.ConversationID != m.ConversationID || previous.Role != m.Role || !sameRequest(previous.RequestID, m.RequestID) {
				return errors.New("message identity binding changed")
			}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO messages VALUES (?,?,?) ON CONFLICT(id) DO UPDATE SET body=excluded.body", m.ID, m.RequestID, string(body)); err != nil {
			return err
		}
		for key, value := range metadata {
			if _, err := tx.ExecContext(ctx, "INSERT INTO metadata VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", key, value); err != nil {
				return err
			}
		}
		if notify && m.Role == "assistant" && m.Status == "complete" && strings.TrimSpace(m.Text) != "" && !strings.HasPrefix(m.ID, "task-report-") {
			if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO main_message_outbox(message_id,body) VALUES (?,?)", m.ID, string(body)); err != nil {
				return err
			}
		}
		eventBody, err := json.Marshal(e)
		if err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, "INSERT INTO events(body) VALUES (?)", string(eventBody))
		if err != nil {
			return err
		}
		cursor, err := result.LastInsertId()
		e.Cursor = &cursor
		return err
	})
	if err != nil {
		return Event{}, err
	}
	return e, nil
}
func sameRequest(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func (s *Store) NotificationCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM main_message_outbox").Scan(&n)
	return n, err
}
func (s *Store) NotificationSequence(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(sequence),0) FROM main_message_outbox").Scan(&n)
	return n, err
}
func (s *Store) Notifications(ctx context.Context, after int64) ([]Notification, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT sequence,body FROM main_message_outbox WHERE sequence>? ORDER BY sequence LIMIT 32", after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Notification{}
	for rows.Next() {
		var n Notification
		var body []byte
		if err := rows.Scan(&n.Sequence, &body); err != nil {
			return nil, err
		}
		if err := protocol.DecodeStrict(body, &n.Message); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// CheckContext rejects corrupt/missing state instead of silently substituting
// empty history. Provider-native replay fields remain opaque to persistence.
func CheckContext(body json.RawMessage) error {
	var value struct {
		Type     string          `json:"type"`
		Version  int             `json:"version"`
		Messages json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &value); err != nil {
		return err
	}
	if value.Type != "context" || value.Version != 2 || len(value.Messages) == 0 || value.Messages[0] != '[' {
		return errors.New("invalid thread context")
	}
	return nil
}
func (s *Store) SaveContext(ctx context.Context, body json.RawMessage) error {
	if err := CheckContext(body); err != nil {
		return err
	}
	return Transaction(ctx, s.db, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "INSERT INTO context_history(classification,body,at) VALUES ('context',?,?)", string(body), Now()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO metadata VALUES ('thread.context',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", string(body))
		return err
	})
}
func (s *Store) LoadContext(ctx context.Context) (json.RawMessage, error) {
	value, exists, err := s.Get(ctx, "thread.context")
	if err != nil {
		return nil, err
	}
	if !exists {
		_, started, err := s.Get(ctx, "thread")
		if err != nil {
			return nil, err
		}
		if started {
			return nil, errors.New("thread context missing; cannot resume with empty history")
		}
		return nil, nil
	}
	body := json.RawMessage(value)
	return body, CheckContext(body)
}

type Rules struct {
	SchemaVersion int    `json:"schema_version" required:"true"`
	Text          string `json:"text" required:"true"`
	Revision      string `json:"revision" required:"true"`
}

func rulesIn(ctx context.Context, tx *sql.Tx) (Rules, error) {
	var body string
	err := tx.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='assistant.instructions.v1'").Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		r := Rules{1, "", ID()}
		b, _ := json.Marshal(r)
		_, err = tx.ExecContext(ctx, "INSERT INTO metadata VALUES ('assistant.instructions.v1',?)", string(b))
		return r, err
	}
	if err != nil {
		return Rules{}, err
	}
	var r Rules
	if err := protocol.DecodeStrict([]byte(body), &r); err != nil {
		return r, err
	}
	if r.SchemaVersion != 1 || r.Revision == "" {
		return r, errors.New("unsupported assistant instructions record")
	}
	return r, nil
}
func (s *Store) Instructions(ctx context.Context) (Rules, error) {
	var rules Rules
	err := Transaction(ctx, s.db, func(tx *sql.Tx) error { var err error; rules, err = rulesIn(ctx, tx); return err })
	return rules, err
}
func (s *Store) SaveInstructions(ctx context.Context, text, revision string) (Rules, error) {
	if len(text) > 262144 {
		return Rules{}, errors.New("assistant instructions exceed 256 KiB")
	}
	var rules Rules
	err := Transaction(ctx, s.db, func(tx *sql.Tx) error {
		current, err := rulesIn(ctx, tx)
		if err != nil {
			return err
		}
		if current.Revision != revision {
			return errors.New("assistant instructions changed; reload before saving")
		}
		rules = Rules{1, text, ID()}
		body, _ := json.Marshal(rules)
		_, err = tx.ExecContext(ctx, "UPDATE metadata SET value=? WHERE key='assistant.instructions.v1'", string(body))
		return err
	})
	return rules, err
}

// Mail methods implement generic durable scoped receipts only. Assistant routing,
// envelope validation, participants and automatic hop limits belong to Assistant.
type Mail struct {
	Sequence int64           `json:"sequence,string"`
	Scope    string          `json:"scope"`
	ID       string          `json:"id"`
	Body     json.RawMessage `json:"body"`
	State    string          `json:"state"`
}

func (s *Store) Enqueue(ctx context.Context, scope, id string, body json.RawMessage) (Mail, error) {
	if scope == "" || id == "" || !json.Valid(body) {
		return Mail{}, errors.New("invalid mail identity or body")
	}
	var mail Mail
	err := Transaction(ctx, s.db, func(tx *sql.Tx) error {
		var old string
		err := tx.QueryRowContext(ctx, "SELECT body FROM mail WHERE scope=? AND id=?", scope, id).Scan(&old)
		if err == nil {
			if old != string(body) {
				return errors.New("mail ID reused with different content")
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			if _, err = tx.ExecContext(ctx, "INSERT INTO mail(scope,id,body,state) VALUES (?,?,?,'queued')", scope, id, string(body)); err != nil {
				return err
			}
		} else {
			return err
		}
		var stored string
		err = tx.QueryRowContext(ctx, "SELECT sequence,scope,id,body,state FROM mail WHERE scope=? AND id=?", scope, id).Scan(&mail.Sequence, &mail.Scope, &mail.ID, &stored, &mail.State)
		mail.Body = json.RawMessage(stored)
		return err
	})
	return mail, err
}
func (s *Store) Mail(ctx context.Context, scope string, after int64) ([]Mail, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT sequence,scope,id,body,state FROM mail WHERE scope=? AND sequence>? ORDER BY sequence LIMIT 100", scope, after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mail{}
	for rows.Next() {
		var m Mail
		var body string
		if err := rows.Scan(&m.Sequence, &m.Scope, &m.ID, &body, &m.State); err != nil {
			return nil, err
		}
		m.Body = json.RawMessage(body)
		out = append(out, m)
	}
	return out, rows.Err()
}
func (s *Store) TransitionMail(ctx context.Context, scope, id, from, to string) error {
	if !(from == "queued" && to == "processing" || from == "processing" && (to == "complete" || to == "failed")) {
		return errors.New("invalid mail transition")
	}
	r, err := s.db.ExecContext(ctx, "UPDATE mail SET state=? WHERE scope=? AND id=? AND state=?", to, scope, id, from)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("mail is missing or no longer %s", from)
	}
	return nil
}
