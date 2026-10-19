package bridge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Listen     string            `json:"listen"`
	Database   string            `json:"database"`
	Codex      string            `json:"codex"`
	Workspaces map[string]string `json:"workspaces"`
	TLSCert    string            `json:"tls_cert,omitempty"`
	TLSKey     string            `json:"tls_key,omitempty"`
}
type Server struct {
	Store     *Store
	Config    Config
	Worker    []string
	Heartbeat time.Duration
	ctx       context.Context
	mu        sync.Mutex
	active    map[string]*appSession
	streams   map[string]int
	rates     map[string]*rate
	wake      chan struct{}
	wg        sync.WaitGroup
}
type rate struct {
	at    time.Time
	count int
}

func NewServer(ctx context.Context, s *Store, c Config, worker []string) *Server {
	return &Server{Store: s, Config: c, Worker: worker, ctx: ctx, Heartbeat: 15 * time.Second, active: map[string]*appSession{}, streams: map[string]int{}, rates: map[string]*rate{}, wake: make(chan struct{}, 1)}
}
func validID(s string) bool {
	if len(s) < 1 || len(s) > 128 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}
func body(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		fail(w, 400, "invalid_payload")
		return false
	}
	return true
}
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" || len(token) > 512 {
		fail(w, 401, "authentication_required")
		return
	}
	p, local, err := s.Store.auth(token)
	if err != nil {
		fail(w, 401, "pairing_invalid")
		return
	}
	// Bound requests per pair; long lived streams have a separate admission limit.
	s.mu.Lock()
	v := s.rates[p.ID]
	if v == nil || time.Since(v.at) > time.Minute {
		v = &rate{at: time.Now()}
		s.rates[p.ID] = v
	}
	v.count++
	limited := v.count > 180
	s.mu.Unlock()
	if limited {
		w.Header().Set("Retry-After", "60")
		fail(w, 429, "rate_limited")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1/")
	parts := strings.Split(path, "/")
	switch {
	case path == "identity" && r.Method == "GET":
		role := "pot"
		if local {
			role = "codex"
		}
		writeJSON(w, 200, map[string]any{"id": p.ID, "device": p.Device, "control": p.Control, "assistant": p.Assistant, "workspaces": p.Workspaces, "write": p.Write, "role": role})
	case !local && path == "tasks" && r.Method == "POST":
		var req Request
		if !body(w, r, &req) {
			return
		}
		if !validID(req.RequestID) || !slices.Contains(p.Workspaces, req.Workspace) || s.Config.Workspaces[req.Workspace] == "" || !(req.Mode == "read-only" || req.Mode == "workspace-write" && p.Write) || len(req.Prompt) == 0 || len(req.Prompt) > 24000 {
			fail(w, 403, "workspace_mode_or_payload_denied")
			return
		}
		task, err := s.Store.submit(p, req)
		if handleError(w, err) {
			return
		}
		writeJSON(w, 202, task)
		s.signal()
	case !local && len(parts) == 2 && parts[0] == "tasks" && validID(parts[1]) && r.Method == "GET":
		var data string
		err = s.Store.db.QueryRow("SELECT data FROM tasks WHERE pair=? AND id=?", p.ID, parts[1]).Scan(&data)
		if handleError(w, err) {
			return
		}
		writeJSON(w, 200, json.RawMessage(data))
	case !local && len(parts) == 3 && parts[0] == "tasks" && parts[2] == "cancel" && validID(parts[1]) && r.Method == "POST":
		if r.ContentLength != 0 && !body(w, r, &struct{}{}) {
			return
		}
		err = s.cancel(p.ID, parts[1])
		if handleError(w, err) {
			return
		}
		writeJSON(w, 202, map[string]string{"status": "cancellation_requested"})
	case !local && path == "events" && r.Method == "GET":
		s.events(w, r, p)
	case !local && path == "snapshot" && r.Method == "GET":
		s.snapshot(w, p)
	case !local && len(parts) == 2 && parts[0] == "receipts" && validID(parts[1]) && r.Method == "POST":
		if r.ContentLength != 0 && !body(w, r, &struct{}{}) {
			return
		}
		res, e := s.Store.db.Exec("UPDATE notices SET accepted=1 WHERE id=? AND pair=?", parts[1], p.ID)
		if handleError(w, e) {
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fail(w, 404, "not_found")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "received"})
	case local && path == "messages" && r.Method == "POST":
		var req struct {
			RequestID   string `json:"request_id"`
			Kind        string `json:"kind"`
			Body        string `json:"body"`
			Correlation string `json:"correlation"`
			Depth       int    `json:"depth"`
		}
		if !body(w, r, &req) {
			return
		}
		if !validID(req.RequestID) || !(req.Kind == "notify" || req.Kind == "request") || len(req.Body) == 0 || len(req.Body) > 12000 || len(req.Correlation) > 128 || req.Depth < 0 || req.Depth > 1 {
			fail(w, 400, "invalid_message")
			return
		}
		var m Message
		err = s.Store.mutate(func(tx *sql.Tx) error {
			var old, payload string
			e := tx.QueryRow("SELECT data,payload FROM messages WHERE pair=? AND request=?", p.ID, req.RequestID).Scan(&old, &payload)
			if e == nil {
				if payload != encoded(req) {
					return errConflict
				}
				return json.Unmarshal([]byte(old), &m)
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			if e = capacity(tx, p.ID, "messages"); e != nil {
				return e
			}
			m = Message{ID: identifier(), RequestID: req.RequestID, Kind: req.Kind, Body: req.Body, Correlation: req.Correlation, Depth: req.Depth, Status: "accepted", Updated: now()}
			if _, e = tx.Exec("INSERT INTO messages VALUES(?,?,?,?,?)", m.ID, p.ID, req.RequestID, encoded(req), encoded(m)); e != nil {
				return e
			}
			return s.Store.publish(tx, p.ID, "message", m, true)
		})
		if handleError(w, err) {
			return
		}
		writeJSON(w, 202, m)
	case len(parts) == 2 && parts[0] == "messages" && validID(parts[1]) && r.Method == "GET":
		var data string
		err = s.Store.db.QueryRow("SELECT data FROM messages WHERE pair=? AND id=?", p.ID, parts[1]).Scan(&data)
		if handleError(w, err) {
			return
		}
		var m Message
		_ = json.Unmarshal([]byte(data), &m)
		if m.Status == "accepted" {
			var accepted int
			_ = s.Store.db.QueryRow("SELECT COUNT(*) FROM notices WHERE pair=? AND accepted=1 AND json_extract(data,'$.data.id')=?", p.ID, m.ID).Scan(&accepted)
			if accepted > 0 {
				m.Status = "received"
			}
		}
		writeJSON(w, 200, m)
	case !local && len(parts) == 3 && parts[0] == "messages" && parts[2] == "reply" && validID(parts[1]) && r.Method == "POST":
		var req struct {
			Result string `json:"result"`
		}
		if !body(w, r, &req) {
			return
		}
		if len(req.Result) == 0 || len(req.Result) > 12000 {
			fail(w, 400, "invalid_result")
			return
		}
		err = s.Store.mutate(func(tx *sql.Tx) error {
			var data string
			if e := tx.QueryRow("SELECT data FROM messages WHERE pair=? AND id=?", p.ID, parts[1]).Scan(&data); e != nil {
				return e
			}
			var m Message
			_ = json.Unmarshal([]byte(data), &m)
			if m.Status == "answered" {
				if m.Result != req.Result {
					return errConflict
				}
				return nil
			}
			m.Status = "answered"
			m.Result = req.Result
			m.Updated = now()
			_, e := tx.Exec("UPDATE messages SET data=? WHERE id=?", encoded(m), m.ID)
			return e
		})
		if handleError(w, err) {
			return
		}
		writeJSON(w, 200, map[string]string{"status": "answered"})
	default:
		fail(w, 404, "not_found")
	}
}
func handleError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		fail(w, 404, "not_found")
	case errors.Is(err, errConflict):
		fail(w, 409, "request_id_conflict")
	case errors.Is(err, errUncertain):
		fail(w, 503, "cleanup_unconfirmed_requires_operator_reconciliation")
	case errors.Is(err, errCapacity):
		fail(w, 429, "capacity_exceeded")
	default:
		fail(w, 500, "storage_failed")
	}
	return true
}
func (s *Server) snapshot(w http.ResponseWriter, p Pair) {
	s.Store.mu.Lock()
	defer s.Store.mu.Unlock()
	var cursor int64
	_ = s.Store.db.QueryRow("SELECT COALESCE(MAX(cursor),0) FROM events").Scan(&cursor)
	data := map[string]any{"cursor": cursor, "pair": p.ID, "route": p.Control + "/" + p.Assistant}
	for _, table := range []string{"notices"} {
		query := "SELECT data FROM " + table + " WHERE pair=?"
		if table == "notices" {
			query += " AND accepted=0 ORDER BY rowid LIMIT 8"
		}
		rows, err := s.Store.db.Query(query, p.ID)
		if handleError(w, err) {
			return
		}
		values := []json.RawMessage{}
		for rows.Next() {
			var raw string
			if err = rows.Scan(&raw); err != nil {
				break
			}
			values = append(values, json.RawMessage(raw))
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if handleError(w, err) {
			return
		}
		data[table] = values
	}
	taskRows, taskErr := s.Store.db.Query("SELECT data FROM tasks WHERE pair=? ORDER BY rowid LIMIT 256", p.ID)
	if handleError(w, taskErr) {
		return
	}
	tasks := []any{}
	for taskRows.Next() {
		var raw string
		var task Task
		if taskErr = taskRows.Scan(&raw); taskErr != nil {
			break
		}
		if taskErr = json.Unmarshal([]byte(raw), &task); taskErr != nil {
			break
		}
		tasks = append(tasks, map[string]any{"id": task.ID, "request_id": task.RequestID, "workspace": task.Workspace, "mode": task.Mode, "status": task.Status, "thread_id": task.ThreadID, "turn_id": task.TurnID, "error": task.Error, "updated": task.Updated})
	}
	if taskErr == nil {
		taskErr = taskRows.Err()
	}
	taskRows.Close()
	if handleError(w, taskErr) {
		return
	}
	data["tasks"] = tasks
	var pending int
	_ = s.Store.db.QueryRow("SELECT COUNT(*) FROM notices WHERE pair=? AND accepted=0", p.ID).Scan(&pending)
	data["more"] = pending > 8
	writeJSON(w, 200, data)
}
func (s *Server) events(w http.ResponseWriter, r *http.Request, p Pair) {
	s.mu.Lock()
	if s.streams[p.ID] >= 2 {
		s.mu.Unlock()
		fail(w, 429, "stream_limit")
		return
	}
	s.streams[p.ID]++
	s.mu.Unlock()
	defer func() { s.mu.Lock(); s.streams[p.ID]--; s.mu.Unlock() }()
	raw := r.Header.Get("Last-Event-ID")
	if raw == "" {
		raw = r.URL.Query().Get("after")
	}
	if raw == "" {
		raw = "0"
	}
	cursor, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || cursor < 0 {
		fail(w, 400, "invalid_cursor")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, "stream_unavailable")
		return
	}
	started := false
	for {
		// Subscribe and read the durable log under the same lock used by commits.
		s.Store.mu.Lock()
		changed := s.Store.changed
		var low, high int64
		err = s.Store.db.QueryRow("SELECT COALESCE((SELECT MIN(cursor) FROM events),0),COALESCE((SELECT MAX(cursor) FROM events),0)").Scan(&low, &high)
		if err != nil {
			s.Store.mu.Unlock()
			return
		}
		if cursor < low-1 || cursor > high {
			s.Store.mu.Unlock()
			if !started {
				fail(w, 409, "resync_required")
			} else {
				_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(5 * time.Second))
				fmt.Fprint(w, "event: resync\ndata: {}\n\n")
				flusher.Flush()
			}
			return
		}
		rows, e := s.Store.db.Query("SELECT cursor,data FROM events WHERE pair=? AND cursor>? ORDER BY cursor LIMIT 32", p.ID, cursor)
		batch := []Event{}
		if e == nil {
			for rows.Next() {
				var ev Event
				var data string
				if e = rows.Scan(&ev.Cursor, &data); e != nil {
					break
				}
				if e = json.Unmarshal([]byte(data), &ev.Notice); e != nil {
					break
				}
				batch = append(batch, ev)
			}
			if e == nil {
				e = rows.Err()
			}
			rows.Close()
		}
		s.Store.mu.Unlock()
		if e != nil {
			return
		}
		rc := http.NewResponseController(w)
		_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Accel-Buffering", "no")
			fmt.Fprintf(w, "event: hello\ndata: {\"cursor\":%d}\n\n", cursor)
			started = true
			flusher.Flush()
		}
		for _, ev := range batch {
			if _, e = fmt.Fprintf(w, "id: %d\nevent: update\ndata: %s\n\n", ev.Cursor, encoded(ev)); e != nil {
				return
			}
			cursor = ev.Cursor
		}
		if len(batch) > 0 {
			flusher.Flush()
			continue
		}
		// Advance across other pairs' entries without exposing their data.
		cursor = high
		select {
		case <-r.Context().Done():
			return
		case <-s.ctx.Done():
			return
		case <-changed:
		case <-time.After(s.Heartbeat):
			_ = rc.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, e = fmt.Fprint(w, ": heartbeat\n\n"); e != nil {
				return
			}
			flusher.Flush()
		}
	}
}
func (s *Server) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
func (s *Server) cancel(pair, id string) error {
	err := s.Store.update(pair, id, func(t *Task) bool {
		if terminal(t.Status) || t.Status == "cancelling" {
			return false
		}
		if t.Status == "queued" {
			t.Status = "cancelled"
		} else {
			t.Status = "cancelling"
		}
		return true
	}, "cancellation", true)
	if err == nil {
		s.mu.Lock()
		a := s.active[id]
		s.mu.Unlock()
		if a != nil {
			a.interrupt()
		}
	}
	return err
}
func (s *Server) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
			}
			var pair, id string
			err := s.Store.db.QueryRow("SELECT pair,id FROM tasks WHERE json_extract(data,'$.status')='queued' AND NOT EXISTS(SELECT 1 FROM tasks WHERE json_extract(data,'$.error')='codex_cleanup_unconfirmed_outcome_unknown') ORDER BY rowid LIMIT 1").Scan(&pair, &id)
			if err == nil {
				s.execute(pair, id)
				continue
			}
			select {
			case <-s.ctx.Done():
				return
			case <-s.wake:
			}
		}
	}()
}
func (s *Server) Wait() { s.wg.Wait() }
func workspacePath(path string) (string, error) {
	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(p) {
		return "", errors.New("workspace_requires_absolute_directory")
	}
	return p, nil
}
