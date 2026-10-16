// Package attention keeps device-local read baselines, never conversation state.
package attention

import (
	"errors"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/instance"
	"maps"
	"os"
	"sync"
)

type Receipt struct {
	ControlID string `json:"control_id"`
	Owner     string `json:"owner"`
	ReplyID   string `json:"reply_id"`
	Count     uint64 `json:"reply_count"`
}
type Row struct {
	Receipt
	Connected                           bool
	Running, Failed, NeedsConfiguration bool
}
type Status struct {
	Unread                              uint64
	Running, Failed, NeedsConfiguration bool
}
type Store struct {
	mu   sync.Mutex
	path string
	seen map[string]Receipt
	rows map[string]Row
}

func key(control, owner string) string { return control + "/" + owner }
func Open(path string) (*Store, error) {
	s := &Store{path: path, seen: map[string]Receipt{}, rows: map[string]Row{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 4<<20 {
		return nil, errors.New("read baselines exceed limit")
	}
	var records []Receipt
	if err = core.DecodeJSON(data, &records); err != nil {
		return nil, err
	}
	for _, r := range records {
		if !core.Identifier(r.ControlID) || !core.Identifier(r.Owner) || r.Count > core.MaxSafeInteger || len(r.ReplyID) > 256 {
			return nil, errors.New("invalid read baseline")
		}
		s.seen[key(r.ControlID, r.Owner)] = r
	}
	return s, nil
}
func (s *Store) records() []Receipt {
	out := make([]Receipt, 0, len(s.seen))
	for _, r := range s.seen {
		out = append(out, r)
	}
	return out
}
func (s *Store) Observe(rows []Row) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := map[string]Row{}
	previousSeen := s.seen
	s.seen = maps.Clone(s.seen)
	committed := false
	defer func() {
		if !committed {
			s.seen = previousSeen
		}
	}()
	changed := false
	for _, r := range rows {
		if !core.Identifier(r.ControlID) || !core.Identifier(r.Owner) || r.Count > core.MaxSafeInteger || len(r.ReplyID) > 256 {
			return Status{}, errors.New("invalid assistant attention state")
		}
		k := key(r.ControlID, r.Owner)
		next[k] = r
		previous, exists := s.seen[k]
		if !exists || r.Count < previous.Count {
			s.seen[k] = r.Receipt
			changed = true
		}
	}
	if changed {
		if err := instance.WriteJSON(s.path, s.records()); err != nil {
			return Status{}, err
		}
	}
	s.rows = next
	committed = true
	return s.status(), nil
}
func (s *Store) status() Status {
	var out Status
	for k, r := range s.rows {
		baseline := s.seen[k]
		if r.ReplyID != "" && baseline.ReplyID != r.ReplyID && r.Count > baseline.Count {
			out.Unread = min(core.MaxSafeInteger, out.Unread+r.Count-baseline.Count)
		}
		if r.Connected {
			out.Running = out.Running || r.Running
			out.Failed = out.Failed || r.Failed
			out.NeedsConfiguration = out.NeedsConfiguration || r.NeedsConfiguration
		}
	}
	return out
}
func (s *Store) Read(r Receipt) (Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(r.ControlID, r.Owner)
	row, ok := s.rows[k]
	if !ok || !row.Connected || row.ReplyID != r.ReplyID || row.Count != r.Count {
		return Receipt{}, errors.New("assistant reply changed before read acknowledgement")
	}
	old := s.seen[k]
	s.seen[k] = r
	if err := instance.WriteJSON(s.path, s.records()); err != nil {
		s.seen[k] = old
		return Receipt{}, err
	}
	return r, nil
}
func (s *Store) Records() []Receipt { s.mu.Lock(); defer s.mu.Unlock(); return s.records() }
