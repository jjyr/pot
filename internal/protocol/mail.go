package protocol

import (
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/internal/core"
	"slices"
	"strings"
)

const (
	AssistantMailAPI = "/api/v1/assistant-mail"
	MaxMailText      = 65536
	MailPageSize     = 32
	MaxMailHops      = 16
)

type AssistantRef struct {
	ControlID   string `json:"control_id" required:"true"`
	AssistantID string `json:"assistant_id" required:"true"`
}

func (r AssistantRef) Valid() bool { return mailID(r.ControlID) && mailID(r.AssistantID) }
func mailID(value string) bool     { return core.Identifier(value) && !strings.Contains(value, ".") }

type MailAssistant struct {
	Address AssistantRef `json:"address"`
	Name    string       `json:"name"`
}
type Directory struct {
	ControlID  string          `json:"control_id"`
	Assistants []MailAssistant `json:"assistants"`
}
type Envelope struct {
	ID             string       `json:"id" required:"true"`
	From           AssistantRef `json:"from" required:"true"`
	To             AssistantRef `json:"to" required:"true"`
	ConversationID string       `json:"conversation_id" required:"true"`
	ReplyTo        *string      `json:"reply_to"`
	Text           string       `json:"text" required:"true"`
	Hops           uint32       `json:"hops" required:"true"`
}

func (e Envelope) Validate() error {
	if !mailID(e.ID) || !e.From.Valid() || !e.To.Valid() || !validUUID(e.ConversationID) || e.ReplyTo != nil && !mailID(*e.ReplyTo) || strings.TrimSpace(e.Text) == "" || len(e.Text) > MaxMailText || e.Hops > MaxMailHops {
		return fmt.Errorf("invalid assistant mail envelope")
	}
	return nil
}

type SendMail struct {
	To      AssistantRef `json:"to"`
	Text    string       `json:"text"`
	ReplyTo *string      `json:"reply_to"`
}
type Receipt struct {
	Message Envelope `json:"message"`
}
type MailRecord struct {
	Sequence int64    `json:"sequence"`
	Message  Envelope `json:"message"`
	State    string   `json:"state"`
	Detail   *string  `json:"detail"`
}
type RelayRequest struct {
	Operation       string     `json:"operation" required:"true"`
	AssistantID     string     `json:"assistant_id,omitempty"`
	After           int64      `json:"after,omitempty"`
	Directory       *Directory `json:"directory,omitempty"`
	TargetControlID string     `json:"target_control_id,omitempty"`
	Message         *Envelope  `json:"message,omitempty"`
	Receipt         *Receipt   `json:"receipt,omitempty"`
}

func (r *RelayRequest) UnmarshalJSON(data []byte) error {
	type plain RelayRequest
	var value plain
	if err := core.DecodeJSON(data, &value); err != nil {
		return err
	}
	fields := map[string]json.RawMessage{}
	_ = json.Unmarshal(data, &fields)
	allowed := []string{"operation"}
	required := []string{}
	switch value.Operation {
	case "directory":
	case "history":
		required = []string{"assistant_id", "after"}
	case "register":
		required = []string{"directory"}
	case "pending":
		required = []string{"target_control_id"}
	case "deliver":
		required = []string{"message"}
	case "acknowledge":
		required = []string{"receipt"}
	default:
		return fmt.Errorf("invalid relay operation")
	}
	allowed = append(allowed, required...)
	for key := range fields {
		if !slices.Contains(allowed, key) {
			return fmt.Errorf("field %s not valid for relay operation", key)
		}
	}
	for _, key := range required {
		if len(fields[key]) == 0 || string(fields[key]) == "null" {
			return fmt.Errorf("missing relay field %s", key)
		}
	}
	*r = RelayRequest(value)
	if r.Message != nil {
		return r.Message.Validate()
	}
	if r.Receipt != nil {
		return r.Receipt.Message.Validate()
	}
	return nil
}
