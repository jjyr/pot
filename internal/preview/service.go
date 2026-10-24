// Package preview supplies a disposable UI fixture shared by the migration hosts.
// It owns no production data, model credentials, tools or execution authority.
package preview

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const ConversationID = "preview-conversation"

type Message struct {
	RequestID  string                  `json:"request_id,omitempty"`
	References []core.MessageReference `json:"references,omitempty"`
	ID         string                  `json:"id"`
	Role       string                  `json:"role"`
	Text       string                  `json:"text"`
	Status     string                  `json:"status"`
}

// Cursor is a decimal string so no frontend loses precision as event counts grow.
type Assistant struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Preview   string `json:"preview"`
	SpaceKind string `json:"space_kind"`
}

type Snapshot struct {
	Assistants     []Assistant       `json:"assistants,omitempty"`
	RuntimeID      string            `json:"runtime_id"`
	BootID         string            `json:"boot_id"`
	OwnerID        string            `json:"owner_id"`
	ConversationID string            `json:"conversation_id"`
	Cursor         string            `json:"cursor"`
	Messages       []Message         `json:"messages"`
	Running        bool              `json:"running"`
	TurnID         *string           `json:"turn_id"`
	Configuration  map[string]string `json:"configuration,omitempty"`
	Usage          map[string]uint64 `json:"usage,omitempty"`
}

type SendRequest struct {
	References     []core.MessageReference `json:"references,omitempty"`
	RequestID      string                  `json:"request_id"`
	BootID         string                  `json:"boot_id"`
	ConversationID string                  `json:"conversation_id"`
	Text           string                  `json:"text"`
}

type Receipt struct {
	MessageID string `json:"message_id"`
	Duplicate bool   `json:"duplicate"`
}

type CancelRequest struct {
	RequestID string `json:"request_id"`
	BootID    string `json:"boot_id"`
	TurnID    string `json:"turn_id"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

type accepted struct {
	request SendRequest
	receipt Receipt
}

// Service is intentionally an in-memory preview, not a partial Control or Agent.
// Hosts expose only its Snapshot, Send and Cancel operations to their clients.
type Service struct {
	mu          sync.Mutex
	ctx         context.Context
	stop        context.CancelFunc
	wg          sync.WaitGroup
	boot        string
	cursor      uint64
	messages    []Message
	receipts    map[string]accepted
	cancelled   map[string]CancelRequest
	subscribers map[chan Snapshot]struct{}
	active      context.CancelFunc
	activeID    string
	closed      bool
}

func New(parent context.Context) *Service {
	ctx, cancel := context.WithCancel(parent)
	return &Service{
		ctx: ctx, stop: cancel, boot: rand.Text(),
		receipts: make(map[string]accepted), cancelled: make(map[string]CancelRequest), subscribers: make(map[chan Snapshot]struct{}),
		messages: []Message{
			{ID: "preview-user", Role: "user", Text: "Please check the shared UI.\n保留桌面样式。", Status: "complete"},
			{ID: "preview-reply", Role: "assistant", Text: "## Shared UI verification\n\nA **bold** reply with `inline code` and a [link](https://example.com).\n\n- Chat and tasks remain available.\n- 中文内容保持原样。\n\n```go\nshared := true\n```", Status: "complete"},
		},
	}
}

func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Service) snapshotLocked() Snapshot {
	var turn *string
	if s.activeID != "" {
		id := s.activeID
		turn = &id
	}
	catalogue := []Assistant{}
	for index, name := range []string{"Pot", "Research", "Code", "旅行计划", "Design", "Writing", "Reading", "Analytics", "Long assistant name for layout", "Assistant 10", "Assistant 11", "Assistant 12"} {
		id := "main"
		if index > 0 {
			id = "assistant-" + strconv.Itoa(index+1)
		}
		catalogue = append(catalogue, Assistant{ID: id, Name: name, Preview: "Continue our conversation · 继续对话", SpaceKind: "remote"})
	}
	return Snapshot{
		Assistants: catalogue,
		RuntimeID:  "pot-ui-preview", BootID: s.boot, OwnerID: "preview-assistant",
		ConversationID: ConversationID, Cursor: strconv.FormatUint(s.cursor, 10),
		Messages: slices.Clone(s.messages), Running: s.active != nil, TurnID: turn,
		Configuration: map[string]string{"name": "Fixture connection", "model": "fixture-model"}, Usage: map[string]uint64{"input": 12000, "output": 3500},
	}
}

// Each subscriber has at most one pending projection; slow hosts see the newest
// snapshot instead of an unbounded backlog. Registration and initial state are atomic.
func (s *Service) Subscribe() (<-chan Snapshot, func()) {
	s.mu.Lock()
	ch := make(chan Snapshot, 1)
	ch <- s.snapshotLocked()
	s.subscribers[ch] = struct{}{}
	s.mu.Unlock()
	var once sync.Once
	remove := func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subscribers, ch)
			s.mu.Unlock()
		})
	}
	// The adapter owns calling remove when it stops reading; no goroutine is needed.
	return ch, remove
}

func (s *Service) changedLocked() {
	s.cursor++
	value := s.snapshotLocked()
	for ch := range s.subscribers {
		select {
		case <-ch:
		default:
		}
		ch <- value
	}
}

func (s *Service) Send(request SendRequest) (Receipt, error) {
	if strings.TrimSpace(request.RequestID) == "" || len(request.RequestID) > 128 ||
		request.ConversationID != ConversationID || strings.TrimSpace(request.Text) == "" || len(request.Text) > 32768 {
		return Receipt{}, &Error{"invalid_request", "Invalid preview message or conversation"}
	}
	if err := core.ValidateMessageReferences(request.References); err != nil {
		return Receipt{}, &Error{"invalid_request", err.Error()}
	}
	request.References = slices.Clone(request.References)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.ctx.Err() != nil {
		return Receipt{}, &Error{"closed", "Preview is shutting down"}
	}
	if request.BootID != s.boot {
		return Receipt{}, &Error{"stale_runtime", "Reconnect to the current preview before sending"}
	}
	if old, ok := s.receipts[request.RequestID]; ok {
		if old.request.RequestID != request.RequestID || old.request.BootID != request.BootID || old.request.ConversationID != request.ConversationID || old.request.Text != request.Text || !slices.Equal(old.request.References, request.References) {
			return Receipt{}, &Error{"request_conflict", "Request identity already has different content"}
		}
		receipt := old.receipt
		receipt.Duplicate = true
		return receipt, nil
	}
	if s.active != nil {
		return Receipt{}, &Error{"busy", "A preview reply is already running"}
	}
	if len(s.receipts) >= 128 {
		return Receipt{}, &Error{"capacity", "Restart the disposable preview to clear its sample history"}
	}
	id := rand.Text()
	receipt := Receipt{MessageID: id}
	s.receipts[request.RequestID] = accepted{request, receipt}
	s.messages = append(s.messages,
		Message{ID: id, RequestID: request.RequestID, Role: "user", Text: request.Text, References: request.References, Status: "complete"},
		Message{ID: id + "-reply", Role: "assistant", Status: "streaming"},
	)
	ctx, cancel := context.WithCancel(s.ctx)
	s.active = cancel
	s.activeID = request.RequestID
	s.changedLocked()
	s.wg.Add(1)
	go s.reply(ctx, id+"-reply")
	return receipt, nil
}

func (s *Service) reply(ctx context.Context, id string) {
	defer s.wg.Done()
	text := []rune("This is a UI preview. No model is connected and no tools are executed.\n\n这是界面预览：主窗口、托盘面板和浏览器共用前端。此处不会调用模型或执行命令。")
	status := "complete"
	for len(text) > 0 {
		select {
		case <-ctx.Done():
			status = "cancelled"
			text = nil
		case <-time.After(25 * time.Millisecond):
			n := min(6, len(text))
			s.mu.Lock()
			for i := range s.messages {
				if s.messages[i].ID == id {
					s.messages[i].Text += string(text[:n])
				}
			}
			s.changedLocked()
			s.mu.Unlock()
			text = text[n:]
		}
	}
	s.mu.Lock()
	for i := range s.messages {
		if s.messages[i].ID == id {
			s.messages[i].Status = status
		}
	}
	s.active()
	s.active = nil
	s.activeID = ""
	s.changedLocked()
	s.mu.Unlock()
}

func (s *Service) Cancel(request CancelRequest) error {
	if strings.TrimSpace(request.RequestID) == "" || len(request.RequestID) > 128 || request.TurnID == "" {
		return &Error{"invalid_request", "Cancellation needs a request identity"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if request.BootID != s.boot {
		return &Error{"stale_runtime", "Cancellation belongs to another preview boot"}
	}
	if old, ok := s.cancelled[request.RequestID]; ok {
		if old != request {
			return &Error{"request_conflict", "Cancellation identity already has a different target"}
		}
		return nil
	}
	if s.active != nil && request.TurnID != s.activeID {
		return &Error{"stale_turn", "Cancellation belongs to another reply"}
	}
	if len(s.cancelled) >= 256 {
		return &Error{"capacity", "Restart the disposable preview to clear its cancellation history"}
	}
	s.cancelled[request.RequestID] = request
	if s.active != nil {
		s.active()
	}
	return nil
}

// Close waits for the fixture producer to stop before its host exits.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	s.stop()
	s.mu.Unlock()
	s.wg.Wait()
}

func HTTPStatus(err error) int {
	var e *Error
	if errors.As(err, &e) {
		switch e.Code {
		case "invalid_request":
			return 400
		case "busy", "request_conflict", "stale_runtime", "stale_turn":
			return 409
		case "capacity":
			return 429
		case "closed":
			return 503
		}
	}
	return 500
}

func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{"internal", fmt.Sprintf("Preview failed: %s", err)}
}
