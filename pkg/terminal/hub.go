// Package terminal owns ephemeral, user-operated PTYs. Bytes never enter a
// product database, transcript, usage record or log.
package terminal

import (
	"context"
	"crypto/rand"
	"errors"
	"os"
	"os/exec"

	"sync"
	"time"
)

const HistoryLimit = 65536

type Size struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func (s Size) Validate() error {
	if s.Cols < 1 || s.Cols > 500 || s.Rows < 1 || s.Rows > 300 {
		return errors.New("invalid terminal dimensions")
	}
	return nil
}

type Create struct {
	Cols    uint16  `json:"cols"`
	Rows    uint16  `json:"rows"`
	Agent   *string `json:"agent,omitempty"`
	Purpose string  `json:"purpose,omitempty"`
}
type Info struct {
	ID       string `json:"id"`
	ExitCode *int   `json:"exit_code"`
	Attached bool   `json:"attached"`
	Purpose  string `json:"purpose"`
}
type Hub struct {
	mu       sync.Mutex
	sessions map[string]*Session
	closed   bool
}

func New() *Hub { return &Hub{sessions: map[string]*Session{}} }

type Session struct {
	mu                 sync.Mutex
	writeMu            sync.Mutex
	owner, id, purpose string
	master             *os.File
	fd                 int
	native             *nativeTerminal
	closed             bool
	cmd                *exec.Cmd
	history            []byte
	output             chan struct{}
	pending            []byte
	lagged             bool
	attached           bool
	exit               *int
	done               chan struct{}
	stop               sync.Once
	cleanupErr         error
}

func (h *Hub) Create(owner, cwd string, r Create) (Info, error) {
	if err := (Size{r.Cols, r.Rows}).Validate(); err != nil {
		return Info{}, err
	}
	if r.Agent != nil {
		return Info{}, errors.New("terminal opens a shell only; enter commands interactively")
	}
	if r.Purpose == "" {
		r.Purpose = "shell"
	}
	if r.Purpose != "shell" && r.Purpose != "login" {
		return Info{}, errors.New("invalid terminal purpose")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return Info{}, errors.New("terminal service stopped")
	}
	count := 0
	for _, s := range h.sessions {
		if s.owner == owner {
			count++
		}
	}
	if count >= 8 {
		return Info{}, errors.New("close an existing terminal first")
	}
	cmd, f, fd, native, err := startTerminal(cwd, r)
	if err != nil {
		return Info{}, err
	}
	s := &Session{owner: owner, id: rand.Text(), purpose: r.Purpose, master: f, fd: fd, native: native, cmd: cmd, done: make(chan struct{})}
	h.sessions[s.id] = s
	readDone := make(chan struct{})
	go func() { defer close(readDone); s.read() }()
	go func() {
		err := cmd.Wait()
		s.kill()
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		cleanupErr := s.finishNative()
		if cleanupErr != nil {
			err = cleanupErr
		}
		select {
		case <-readDone:
		case <-time.After(time.Second):
		}
		s.mu.Lock()
		s.closed = true
		f.Close()
		s.mu.Unlock()
		<-readDone
		code := 0
		if err != nil {
			code = -1
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
		}
		s.mu.Lock()
		s.cleanupErr = cleanupErr
		s.exit = &code
		close(s.done)
		s.mu.Unlock()
	}()
	return s.Info(), nil
}
func (s *Session) Info() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	var code *int
	if s.exit != nil {
		v := *s.exit
		code = &v
	}
	return Info{s.id, code, s.attached, s.purpose}
}
func (h *Hub) List(owner string) []Info {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := []Info{}
	for _, s := range h.sessions {
		if s.owner == owner {
			out = append(out, s.Info())
		}
	}
	return out
}
func (h *Hub) Get(owner, id string) (*Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sessions[id]
	if s == nil || s.owner != owner {
		return nil, errors.New("terminal ended or Runtime restarted")
	}
	return s, nil
}
func (s *Session) read() {
	buf := make([]byte, 4096)
	for {
		n, err := s.master.Read(buf)
		if n > 0 {
			data := append([]byte{}, buf[:n]...)
			s.mu.Lock()
			s.history = append(s.history, data...)
			if len(s.history) > HistoryLimit {
				s.history = append([]byte{}, s.history[len(s.history)-HistoryLimit:]...)
			}
			if s.output != nil && !s.lagged {
				if len(s.pending)+len(data) > 128*1024 {
					s.lagged = true
					s.pending = nil
				} else {
					s.pending = append(s.pending, data...)
				}
				select {
				case s.output <- struct{}{}:
				default:
				}
			}
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

// Attach atomically captures history and subscribes. Slow clients detach;
// reconnecting replays the bounded tail without stopping the shell.
func (s *Session) Attach() ([]byte, <-chan struct{}, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attached {
		return nil, nil, nil, errors.New("terminal already connected")
	}
	s.attached = true
	output := make(chan struct{}, 1)
	s.output = output
	s.pending = nil
	s.lagged = false
	return append([]byte{}, s.history...), output, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.output == output {
			s.output = nil
			s.pending = nil
		}
		s.attached = false
	}, nil
}

// Drain coalesces PTY fragments into one bounded write; fragment count cannot
// make a healthy client appear slow while a shell echoes individual bytes.
func (s *Session) Drain() ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lagged {
		return nil, false
	}
	data := s.pending
	s.pending = nil
	return data, true
}
func (s *Session) Done() <-chan struct{} { return s.done }
func (s *Session) Write(data []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.input().SetWriteDeadline(time.Now().Add(15 * time.Second))
	defer s.input().SetWriteDeadline(time.Time{})
	for len(data) > 0 {
		n, err := s.input().Write(data)
		if err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}
func (s *Session) Resize(size Size) error {
	if err := size.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("terminal closed")
	}
	return s.resize(size)
}
func (s *Session) kill() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.killNative()
	}
}
func (s *Session) Close(ctx context.Context) error {
	s.stop.Do(s.kill)
	select {
	case <-s.done:
		return s.cleanupErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (h *Hub) CloseSession(ctx context.Context, owner, id string) error {
	s, err := h.Get(owner, id)
	if err != nil {
		return err
	}
	if err = s.Close(ctx); err != nil {
		return err
	}
	h.mu.Lock()
	delete(h.sessions, id)
	h.mu.Unlock()
	return nil
}
func (h *Hub) Close(ctx context.Context) error {
	h.mu.Lock()
	h.closed = true
	sessions := []*Session{}
	for _, s := range h.sessions {
		sessions = append(sessions, s)
	}
	h.mu.Unlock()
	var err error
	for _, s := range sessions {
		err = errors.Join(err, s.Close(ctx))
	}
	return err
}
