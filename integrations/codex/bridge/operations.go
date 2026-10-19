package bridge

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
)

// Operations are transport independent. Both MCP entry points use these same
// pair-scoped mutations, including durable notices and exact-retry checks.
func (s *Server) submit(p Pair, req Request) (any, error) {
	if !validID(req.RequestID) || !slices.Contains(p.Workspaces, req.Workspace) || s.Config.Workspaces[req.Workspace] == "" || !(req.Mode == "read-only" || req.Mode == "workspace-write" && p.Write) || len(req.Prompt) == 0 || len(req.Prompt) > 24000 {
		return nil, errors.New("workspace_mode_or_payload_denied")
	}
	task, err := s.Store.submit(p, req)
	if err == nil {
		s.signal()
	}
	return task, err
}

func (s *Server) task(p Pair, id string) (any, error) {
	var data string
	err := s.Store.db.QueryRow("SELECT data FROM tasks WHERE pair=? AND id=?", p.ID, id).Scan(&data)
	return json.RawMessage(data), err
}

type messageInput struct {
	RequestID   string `json:"request_id"`
	Body        string `json:"body"`
	Correlation string `json:"correlation,omitempty"`
	Depth       int    `json:"depth,omitempty"`
}

func (s *Server) message(p Pair, kind string, input messageInput) (any, error) {
	if !validID(input.RequestID) || len(input.Body) == 0 || len(input.Body) > 12000 || len(input.Correlation) > 128 || input.Depth < 0 || input.Depth > 1 {
		return nil, errors.New("invalid_message")
	}
	req := struct {
		RequestID   string `json:"request_id"`
		Kind        string `json:"kind"`
		Body        string `json:"body"`
		Correlation string `json:"correlation"`
		Depth       int    `json:"depth"`
	}{input.RequestID, kind, input.Body, input.Correlation, input.Depth}
	var m Message
	err := s.Store.mutate(func(tx *sql.Tx) error {
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
		m = Message{ID: identifier(), RequestID: req.RequestID, Kind: kind, Body: req.Body, Correlation: req.Correlation, Depth: req.Depth, Status: "accepted", Updated: now()}
		if _, e = tx.Exec("INSERT INTO messages VALUES(?,?,?,?,?)", m.ID, p.ID, req.RequestID, encoded(req), encoded(m)); e != nil {
			return e
		}
		return s.Store.publish(tx, p.ID, "message", m, true)
	})
	return m, err
}

func (s *Server) receipt(p Pair, id string) (any, error) {
	var data string
	if err := s.Store.db.QueryRow("SELECT data FROM messages WHERE pair=? AND id=?", p.ID, id).Scan(&data); err != nil {
		return nil, err
	}
	var m Message
	if err := json.Unmarshal([]byte(data), &m); err != nil {
		return nil, err
	}
	if m.Status == "accepted" {
		var accepted int
		if err := s.Store.db.QueryRow("SELECT COUNT(*) FROM notices WHERE pair=? AND accepted=1 AND json_extract(data,'$.data.id')=?", p.ID, m.ID).Scan(&accepted); err != nil {
			return nil, err
		}
		if accepted > 0 {
			m.Status = "received"
		}
	}
	return m, nil
}

func (s *Server) reply(p Pair, id, result string) (any, error) {
	if len(result) == 0 || len(result) > 12000 {
		return nil, errors.New("invalid_result")
	}
	err := s.Store.mutate(func(tx *sql.Tx) error {
		var data string
		if e := tx.QueryRow("SELECT data FROM messages WHERE pair=? AND id=?", p.ID, id).Scan(&data); e != nil {
			return e
		}
		var m Message
		if e := json.Unmarshal([]byte(data), &m); e != nil {
			return e
		}
		if m.Status == "answered" {
			if m.Result != result {
				return errConflict
			}
			return nil
		}
		m.Status, m.Result, m.Updated = "answered", result, now()
		_, e := tx.Exec("UPDATE messages SET data=? WHERE id=? AND pair=?", encoded(m), m.ID, p.ID)
		return e
	})
	return map[string]string{"status": "answered"}, err
}

func operationError(err error) string {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "not_found"
	case errors.Is(err, errConflict):
		return "request_id_conflict"
	case errors.Is(err, errUncertain):
		return "cleanup_unconfirmed_requires_operator_reconciliation"
	case errors.Is(err, errCapacity):
		return "capacity_exceeded"
	default:
		// Do not expose storage diagnostics or credentials in tool results.
		switch err.Error() {
		case "workspace_mode_or_payload_denied", "invalid_message", "invalid_result":
			return err.Error()
		}
		return "storage_failed"
	}
}
