// Package systemnotifications stores device-local native preferences and event
// claims. It is independent of Wails so persistence is testable on all hosts.
package systemnotifications

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Store struct {
	mu    sync.Mutex
	path  string
	state state
}
type state struct {
	Enabled bool             `json:"enabled"`
	Seen    map[string]int64 `json:"seen"`
}

func Open(path string) (*Store, error) {
	s := &Store{path: path, state: state{Enabled: true, Seen: map[string]int64{}}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(data, &s.state); err != nil {
		return nil, err
	}
	if s.state.Seen == nil {
		s.state.Seen = map[string]int64{}
	}
	return s, nil
}
func (s *Store) Enabled() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.state.Enabled }
func (s *Store) SetEnabled(enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.state.Enabled
	s.state.Enabled = enabled
	if err := s.save(); err != nil {
		s.state.Enabled = previous
		return err
	}
	return nil
}

// Claim consumes even suppressed events, so leaving a conversation or enabling
// notifications later cannot turn an old projection into a new notification.
func (s *Store) Claim(id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.state.Seen[id]; ok {
		return false, nil
	}
	now := time.Now()
	s.state.Seen[id] = now.UnixMilli()
	for key, at := range s.state.Seen {
		if at < now.Add(-7*24*time.Hour).UnixMilli() {
			delete(s.state.Seen, key)
		}
	}
	if err := s.save(); err != nil {
		delete(s.state.Seen, id)
		return false, err
	}
	return true, nil
}
func (s *Store) save() error {
	data, err := json.Marshal(s.state)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".system-notifications-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.path)
}
