// Package bridge implements the authenticated, durable Pot/Codex task bridge.
package bridge

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const maxBody = 64 * 1024
const maxResult = 16000
const retention = 4096

type Pair struct {
	ID         string   `json:"id"`
	Device     string   `json:"device"`
	Control    string   `json:"control"`
	Assistant  string   `json:"assistant"`
	Workspaces []string `json:"workspaces"`
	Write      bool     `json:"write"`
}
type ClientConfig struct {
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
	ID       string `json:"id"`
	Route    string `json:"route"`
	CA       string `json:"ca,omitempty"`
}
type Request struct {
	RequestID string `json:"request_id"`
	Workspace string `json:"workspace"`
	Mode      string `json:"mode"`
	Prompt    string `json:"prompt"`
}
type Task struct {
	ID string `json:"id"`
	Request
	Status   string   `json:"status"`
	ThreadID string   `json:"thread_id,omitempty"`
	TurnID   string   `json:"turn_id,omitempty"`
	Result   string   `json:"result,omitempty"`
	Files    []string `json:"files,omitempty"`
	Error    string   `json:"error,omitempty"`
	Updated  string   `json:"updated"`
}
type Message struct {
	ID          string `json:"id"`
	RequestID   string `json:"request_id"`
	Kind        string `json:"kind"`
	Body        string `json:"body"`
	Correlation string `json:"correlation,omitempty"`
	Depth       int    `json:"depth"`
	Status      string `json:"status"`
	Result      string `json:"result,omitempty"`
	Updated     string `json:"updated"`
}
type Notice struct {
	ID   string          `json:"id"`
	Kind string          `json:"kind"`
	Data json.RawMessage `json:"data"`
	At   string          `json:"at"`
}
type Event struct {
	Cursor int64 `json:"cursor"`
	Notice
}
type Store struct {
	db      *sql.DB
	mu      sync.Mutex // serializes projection, journal and subscriber registration
	changed chan struct{}
}

func identifier() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func hash(s string) string { b := sha256.Sum256([]byte(s)); return hex.EncodeToString(b[:]) }
func now() string          { return time.Now().UTC().Format(time.RFC3339Nano) }
func encoded(v any) string { b, _ := json.Marshal(v); return string(b) }
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	f.Close()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, changed: make(chan struct{})}
	_, err = db.Exec(`PRAGMA journal_mode=WAL; PRAGMA busy_timeout=5000;
 CREATE TABLE IF NOT EXISTS pairs(id TEXT PRIMARY KEY, remote_hash TEXT UNIQUE, local_hash TEXT UNIQUE, data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS tasks(id TEXT PRIMARY KEY,pair TEXT NOT NULL,request TEXT NOT NULL,payload TEXT NOT NULL,data TEXT NOT NULL,UNIQUE(pair,request));
 CREATE TABLE IF NOT EXISTS messages(id TEXT PRIMARY KEY,pair TEXT NOT NULL,request TEXT NOT NULL,payload TEXT NOT NULL,data TEXT NOT NULL,UNIQUE(pair,request));
 CREATE TABLE IF NOT EXISTS events(cursor INTEGER PRIMARY KEY AUTOINCREMENT,pair TEXT NOT NULL,data TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS notices(id TEXT PRIMARY KEY,pair TEXT NOT NULL,data TEXT NOT NULL,accepted INTEGER NOT NULL DEFAULT 0);
 CREATE INDEX IF NOT EXISTS events_pair ON events(pair,cursor);`)
	if err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}
func (s *Store) Close() error { return s.db.Close() }
func (s *Store) Pair(p Pair) (ClientConfig, ClientConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.ID == "" {
		p.ID = identifier()
	}
	r, l := identifier(), identifier()
	_, err := s.db.Exec("INSERT INTO pairs VALUES(?,?,?,?)", p.ID, hash(r), hash(l), encoded(p))
	return ClientConfig{Token: r, ID: p.ID, Route: p.Control + "/" + p.Assistant}, ClientConfig{Token: l, ID: p.ID, Route: p.Control + "/" + p.Assistant}, err
}
func (s *Store) auth(token string) (Pair, bool, error) {
	var p Pair
	var data string
	var local bool
	err := s.db.QueryRow("SELECT data,local_hash=? FROM pairs WHERE remote_hash=? OR local_hash=?", hash(token), hash(token), hash(token)).Scan(&data, &local)
	if err == nil {
		err = json.Unmarshal([]byte(data), &p)
	}
	return p, local, err
}

// Events carry a bounded projection; full inputs/results stay queryable by ID.
// JSON escaping counts toward the limit, not just the source string length.
func noticeData(value any) json.RawMessage {
	var view map[string]any
	switch v := value.(type) {
	case Task:
		view = map[string]any{"id": v.ID, "request_id": v.RequestID, "workspace": v.Workspace, "mode": v.Mode, "status": v.Status, "thread_id": v.ThreadID, "turn_id": v.TurnID, "updated": v.Updated}
		if terminal(v.Status) || v.Status == "waiting_approval" {
			view["result"] = v.Result
			view["error"] = v.Error
			view["files"] = v.Files
		}
	case Message:
		view = map[string]any{"id": v.ID, "request_id": v.RequestID, "kind": v.Kind, "body": v.Body, "correlation": v.Correlation, "depth": v.Depth, "status": v.Status, "updated": v.Updated}
	default:
		return json.RawMessage(encoded(value))
	}
	for len(encoded(view)) > 20000 {
		key := "result"
		if _, ok := view["body"]; ok {
			key = "body"
		}
		text, _ := view[key].(string)
		if len(text) == 0 {
			delete(view, "files")
			break
		}
		view[key] = trimBytes(text, len(text)*3/4)
		view["truncated"] = true
	}
	return json.RawMessage(encoded(view))
}
func (s *Store) publish(tx *sql.Tx, pair, kind string, v any, critical bool) error {
	n := Notice{ID: identifier(), Kind: kind, Data: noticeData(v), At: now()}
	if _, err := tx.Exec("INSERT INTO events(pair,data) VALUES(?,?)", pair, encoded(n)); err != nil {
		return err
	}
	if critical {
		if _, err := tx.Exec("INSERT INTO notices(id,pair,data) VALUES(?,?,?)", n.ID, pair, encoded(n)); err != nil {
			return err
		}
	}
	_, err := tx.Exec("DELETE FROM events WHERE cursor <= (SELECT COALESCE(MAX(cursor),0)-? FROM events)", retention)
	return err
}
func (s *Store) mutate(fn func(*sql.Tx) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = fn(tx); err != nil {
		return err
	}
	if err = tx.Commit(); err == nil {
		close(s.changed)
		s.changed = make(chan struct{})
	}
	return err
}
func taskTx(tx *sql.Tx, pair, id string) (Task, error) {
	var t Task
	var data string
	err := tx.QueryRow("SELECT data FROM tasks WHERE pair=? AND id=?", pair, id).Scan(&data)
	if err == nil {
		err = json.Unmarshal([]byte(data), &t)
	}
	return t, err
}
func (s *Store) update(pair, id string, fn func(*Task) bool, kind string, critical bool) error {
	return s.mutate(func(tx *sql.Tx) error {
		t, err := taskTx(tx, pair, id)
		if err != nil {
			return err
		}
		if !fn(&t) {
			return nil
		}
		t.Updated = now()
		if _, err = tx.Exec("UPDATE tasks SET data=? WHERE id=?", encoded(t), id); err != nil {
			return err
		}
		return s.publish(tx, pair, kind, t, critical)
	})
}
func terminal(status string) bool {
	return status == "completed" || status == "failed" || status == "cancelled"
}

var errConflict = errors.New("request_id_conflict")
var errCapacity = errors.New("capacity_exceeded")
var errUncertain = errors.New("cleanup_unconfirmed_requires_operator_reconciliation")

func capacity(tx *sql.Tx, pair, table string) error {
	var count int
	if err := tx.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE pair=?", pair).Scan(&count); err != nil {
		return err
	}
	if count >= 256 {
		return errCapacity
	}
	if err := tx.QueryRow("SELECT COUNT(*) FROM notices WHERE pair=? AND accepted=0", pair).Scan(&count); err != nil {
		return err
	}
	if count >= 64 {
		return errCapacity
	}
	return nil
}
func (s *Store) submit(p Pair, r Request) (Task, error) {
	var t Task
	err := s.mutate(func(tx *sql.Tx) error {
		var payload, data string
		err := tx.QueryRow("SELECT payload,data FROM tasks WHERE pair=? AND request=?", p.ID, r.RequestID).Scan(&payload, &data)
		if err == nil {
			if payload != encoded(r) {
				return errConflict
			}
			return json.Unmarshal([]byte(data), &t)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var uncertain int
		if err = tx.QueryRow("SELECT COUNT(*) FROM tasks WHERE json_extract(data,'$.error')='codex_cleanup_unconfirmed_outcome_unknown'").Scan(&uncertain); err != nil {
			return err
		}
		if uncertain > 0 {
			return errUncertain
		}
		if err = capacity(tx, p.ID, "tasks"); err != nil {
			return err
		}
		var active int
		if err = tx.QueryRow("SELECT COUNT(*) FROM tasks WHERE json_extract(data,'$.status') IN ('queued','running','waiting_approval','cancelling')").Scan(&active); err != nil {
			return err
		}
		if active >= 32 {
			return errCapacity
		}
		t = Task{ID: identifier(), Request: r, Status: "queued", Updated: now()}
		if _, err = tx.Exec("INSERT INTO tasks VALUES(?,?,?,?,?)", t.ID, p.ID, r.RequestID, encoded(r), encoded(t)); err != nil {
			return err
		}
		return s.publish(tx, p.ID, "task", t, false)
	})
	return t, err
}

// Interrupted executions are fenced on restart. No queued/running work is replayed.
func (s *Store) Recover() error {
	rows, err := s.db.Query("SELECT pair,id FROM tasks WHERE json_extract(data,'$.status') NOT IN ('completed','failed','cancelled')")
	if err != nil {
		return err
	}
	var ids [][2]string
	for rows.Next() {
		var p, id string
		if err = rows.Scan(&p, &id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, [2]string{p, id})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, v := range ids {
		if err = s.update(v[0], v[1], func(t *Task) bool {
			if t.Status == "queued" {
				t.Error = "bridge_restarted_before_execution"
			} else {
				t.Error = "bridge_restarted_outcome_unknown"
			}
			t.Status = "failed"
			return true
		}, "terminal", true); err != nil {
			return err
		}
	}
	return nil
}
