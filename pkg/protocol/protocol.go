// Package protocol owns public client DTOs. Model-provider messages are in
// modelprotocol, never persisted by interpreting these UI messages as history.
package protocol

import (
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"slices"
)

const (
	APIVersion          = 3
	RuntimePort         = 17700
	RuntimeEndpointFile = "runtime.json"
	PathSnapshot        = "/api/v1/snapshot"
	PathEvents          = "/api/v1/events"
	PathMessages        = "/api/v1/messages"
	PathCancel          = "/api/v1/turn/cancel"
	PathTasks           = "/api/v1/tasks"
)

func DecodeStrict(data []byte, destination any) error {
	if err := core.DecodeJSON(data, destination); err != nil {
		return err
	}
	if v, ok := destination.(interface{ Validate() error }); ok {
		return v.Validate()
	}
	return nil
}
func CanonicalJSON(value any) ([]byte, error) { return core.CanonicalJSON(value) }

type RuntimeEndpoint struct {
	APIVersion uint32 `json:"api_version" required:"true"`
	Port       uint16 `json:"port" required:"true"`
	Token      string `json:"token" required:"true"`
	BootID     string `json:"boot_id" required:"true"`
}
type AgentPhase string

const (
	NeedsConfiguration AgentPhase = "needs_configuration"
	Ready              AgentPhase = "ready"
	AgentFailed        AgentPhase = "failed"
)

type AgentStatus struct {
	Attachments core.AttachmentCapabilities `json:"attachments"`
	Model       string                      `json:"model" required:"true"`
	Phase       AgentPhase                  `json:"phase" required:"true"`
	Detail      *string                     `json:"detail"`
}
type Role string

const (
	User      Role = "user"
	Assistant Role = "assistant"
	Tool      Role = "tool"
	System    Role = "system"
)

type MessageStatus string

const (
	Accepted         MessageStatus = "accepted"
	Streaming        MessageStatus = "streaming"
	Complete         MessageStatus = "complete"
	Failed           MessageStatus = "failed"
	MessageCancelled MessageStatus = "cancelled"
)

type Message struct {
	References     []core.MessageReference `json:"references,omitempty"`
	Attachments    []core.Attachment       `json:"attachments,omitempty"`
	Queued         bool                    `json:"queued,omitempty"`
	Tool           *ToolCall               `json:"tool,omitempty"`
	ID             string                  `json:"id" required:"true"`
	ConversationID string                  `json:"conversation_id" required:"true"`
	Role           Role                    `json:"role" required:"true"`
	Text           string                  `json:"text" required:"true"`
	Status         MessageStatus           `json:"status" required:"true"`
	CreatedAtMS    int64                   `json:"created_at_ms" required:"true"`
	RequestID      *string                 `json:"request_id"`
	Steering       bool                    `json:"steering,omitempty"`
	ReplyTo        string                  `json:"reply_to,omitempty"`
	Error          string                  `json:"error,omitempty"`
}

func (m Message) Validate() error {
	if err := core.ValidateMessageReferences(m.References); err != nil {
		return err
	}
	if len(m.References) > 0 && m.Role != User {
		return fmt.Errorf("references require user role")
	}
	if err := core.ValidateAttachments(m.Attachments); err != nil {
		return err
	}
	if len(m.Attachments) > 0 && m.Role != User {
		return fmt.Errorf("attachments require user role")
	}
	if m.Queued && (m.Role != User || m.Status != Accepted) {
		return fmt.Errorf("only accepted user requests may be queued")
	}
	if m.Tool != nil {
		if m.Role != Tool {
			return fmt.Errorf("tool details require tool role")
		}
		if err := m.Tool.Validate(); err != nil {
			return err
		}
	}
	if !core.Identifier(m.ID) || !core.Identifier(m.ConversationID) || m.RequestID != nil && !core.Identifier(*m.RequestID) {
		return fmt.Errorf("invalid message identity")
	}
	switch m.Role {
	case User, Assistant, Tool, System:
	default:
		return fmt.Errorf("invalid message role")
	}
	switch m.Status {
	case Accepted, Streaming, Complete, Failed, MessageCancelled:
	default:
		return fmt.Errorf("invalid message status")
	}
	if m.CreatedAtMS < 0 || uint64(m.CreatedAtMS) > core.MaxSafeInteger {
		return fmt.Errorf("invalid message timestamp")
	}
	if m.ReplyTo != "" && (m.Role != Assistant || !core.Identifier(m.ReplyTo)) {
		return fmt.Errorf("invalid assistant reply binding")
	}
	if m.Error != "" && (m.Role != Assistant || m.Status != Failed) {
		return fmt.Errorf("error detail requires a failed assistant reply")
	}
	return nil
}

type TurnState struct {
	UserMessageID string `json:"user_message_id" required:"true"`
	Cancelling    bool   `json:"cancelling" required:"true"`
	Paused        bool   `json:"paused"`
	State         string `json:"state"`
}
type RequestBudget struct {
	Used            uint64 `json:"used" required:"true"`
	Limit           uint64 `json:"limit" required:"true"`
	MaxOutputTokens uint64 `json:"max_output_tokens" required:"true"`
}

func (b RequestBudget) Remaining() uint64 {
	if b.Used >= b.Limit {
		return 0
	}
	return b.Limit - b.Used
}

type Snapshot struct {
	Budget         *RequestBudget `json:"budget"`
	APIVersion     uint32         `json:"api_version" required:"true"`
	BootID         string         `json:"boot_id" required:"true"`
	Cursor         int64          `json:"cursor" required:"true"`
	Agent          AgentStatus    `json:"agent" required:"true"`
	ConversationID string         `json:"conversation_id" required:"true"`
	Messages       []Message      `json:"messages" required:"true"`
	Turn           *TurnState     `json:"turn"`
}
type Event struct {
	EventID string         `json:"event_id" required:"true"`
	Cursor  *int64         `json:"cursor"`
	Type    string         `json:"type" required:"true"`
	Budget  *RequestBudget `json:"budget,omitempty"`
	Agent   *AgentStatus   `json:"status,omitempty"`
	Message *Message       `json:"message,omitempty"`
	Turn    *TurnState     `json:"turn,omitempty"`
}

func (e Event) MarshalJSON() ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	type base Event
	if e.Type != "turn" {
		return json.Marshal(base(e))
	}
	return json.Marshal(struct {
		EventID string     `json:"event_id"`
		Cursor  *int64     `json:"cursor"`
		Type    string     `json:"type"`
		Turn    *TurnState `json:"turn"`
	}{e.EventID, e.Cursor, e.Type, e.Turn})
}
func (e Event) Validate() error {
	if e.EventID == "" || e.Cursor != nil && (*e.Cursor < 0 || uint64(*e.Cursor) > core.MaxSafeInteger) {
		return fmt.Errorf("invalid event identity/cursor")
	}
	count := 0
	if e.Budget != nil {
		count++
	}
	if e.Agent != nil {
		count++
	}
	if e.Message != nil {
		count++
	}
	if e.Turn != nil {
		count++
	}
	switch e.Type {
	case "budget":
		if e.Budget == nil || count != 1 {
			return fmt.Errorf("invalid budget event")
		}
	case "agent":
		if e.Agent == nil || count != 1 {
			return fmt.Errorf("invalid agent event")
		}
	case "message":
		if e.Message == nil || count != 1 {
			return fmt.Errorf("invalid message event")
		}
		return e.Message.Validate()
	case "turn":
		if count > 1 || count == 1 && e.Turn == nil {
			return fmt.Errorf("invalid turn event")
		}
	default:
		return fmt.Errorf("unknown event type")
	}
	return nil
}

type SendMessage struct {
	References     []core.MessageReference `json:"references,omitempty"`
	Attachments    []core.Attachment       `json:"attachments,omitempty"`
	RequestID      string                  `json:"request_id" required:"true"`
	ConversationID string                  `json:"conversation_id" required:"true"`
	Text           string                  `json:"text" required:"true"`
}

func (m SendMessage) Equal(other SendMessage) bool {
	return m.RequestID == other.RequestID && m.ConversationID == other.ConversationID && m.Text == other.Text && slices.Equal(m.Attachments, other.Attachments) && slices.Equal(m.References, other.References)
}
func (m SendMessage) Validate() error {
	if err := core.ValidateMessageReferences(m.References); err != nil {
		return err
	}
	if err := core.ValidateAttachments(m.Attachments); err != nil {
		return err
	}
	if !core.Identifier(m.RequestID) || !core.Identifier(m.ConversationID) || (m.Text == "" && len(m.Attachments) == 0) || len(m.Text) > 65536 {
		return fmt.Errorf("invalid accepted input")
	}
	return nil
}

type SendMessageAccepted struct {
	MessageID string `json:"message_id"`
	Duplicate bool   `json:"duplicate"`
}
type Authenticate struct {
	RequestID string `json:"request_id" required:"true"`
	MethodID  string `json:"method_id" required:"true"`
}
type CancelTurn struct {
	RequestID     string `json:"request_id" required:"true"`
	UserMessageID string `json:"user_message_id" required:"true"`
}
type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e APIError) Error() string { return e.Code + ": " + e.Message }
