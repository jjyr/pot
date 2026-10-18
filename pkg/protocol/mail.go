package protocol

import (
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"slices"
	"strings"
)

const (
	AssistantMailAPI = "/api/v1/assistant-mail"
	MaxMailText      = 65536
	MailPageSize     = 32
	MaxMailHops      = 16
)

type AgentRef struct {
	ControlID string `json:"control_id" required:"true"`
	Kind      string `json:"kind" required:"true"`
	AgentID   string `json:"agent_id" required:"true"`
}

func (r AgentRef) Valid() bool {
	return mailID(r.ControlID) && mailID(r.AgentID) && (r.Kind == "assistant" || r.Kind == "task" || r.Kind == "user" || r.Kind == "runtime")
}
func mailID(value string) bool { return core.Identifier(value) && !strings.Contains(value, ".") }

type MailAssistant struct {
	Address AgentRef `json:"address"`
	Name    string   `json:"name"`
}
type Directory struct {
	ControlID  string          `json:"control_id"`
	Assistants []MailAssistant `json:"assistants"`
}
type Envelope struct {
	Kind           string           `json:"kind"`
	Owner          string           `json:"owner,omitempty"`
	Digest         string           `json:"digest,omitempty"`
	RequestID      string           `json:"request_id,omitempty"`
	Result         *TaskResultEvent `json:"result,omitempty"`
	ID             string           `json:"id" required:"true"`
	From           AgentRef         `json:"from" required:"true"`
	To             AgentRef         `json:"to" required:"true"`
	ConversationID string           `json:"conversation_id" required:"true"`
	ReplyTo        *string          `json:"reply_to"`
	Text           string           `json:"text" required:"true"`
	Hops           uint32           `json:"hops" required:"true"`
}

func (e Envelope) Validate() error {
	if e.Kind != "message" && e.Kind != "input" && e.Kind != "guidance" && e.Kind != "user_guidance" && e.Kind != "reference" && e.Kind != "task_result" {
		return fmt.Errorf("invalid agent message kind")
	}
	if e.Kind == "message" && (e.To.Kind != "assistant" || (e.From.Kind != "assistant" && e.From.Kind != "task") || e.Result != nil || e.Owner != "" || e.RequestID != "" || e.Digest != "") {
		return fmt.Errorf("invalid agent correspondence")
	}
	if e.Kind != "message" && e.Kind != "task_result" && (e.To.Kind != "task" || !mailID(e.Owner) || !core.Identifier(e.RequestID) || e.Result != nil) {
		return fmt.Errorf("invalid Task inbox message")
	}
	if e.Kind == "task_result" && (e.To.Kind != "assistant" || e.From.Kind != "runtime" || e.Result == nil || !core.Identifier(e.Result.TaskID) || !validUUID(e.Result.RunID)) {
		return fmt.Errorf("invalid Runtime task event")
	}
	if e.Result != nil {
		if err := e.Result.Origin.Validate(); err != nil {
			return err
		}
		switch e.Result.Phase {
		case TaskComplete, TaskFailed, TaskCancelled, TaskInterrupted:
		default:
			return fmt.Errorf("task event is not terminal")
		}
		if strings.TrimSpace(e.Result.Title) == "" {
			return fmt.Errorf("task event title missing")
		}
	}
	if !mailID(e.ID) || !e.From.Valid() || !e.To.Valid() || !validUUID(e.ConversationID) || e.ReplyTo != nil && !mailID(*e.ReplyTo) || strings.TrimSpace(e.Text) == "" || len(e.Text) > MaxMailText || e.Hops > MaxMailHops {
		return fmt.Errorf("invalid assistant mail envelope")
	}
	return nil
}

type TaskResultEvent struct {
	TaskID            string     `json:"task_id"`
	RunID             string     `json:"run_id"`
	Title             string     `json:"title"`
	Phase             TaskPhase  `json:"phase"`
	Text              string     `json:"text"`
	Error             string     `json:"error,omitempty"`
	Origin            TaskOrigin `json:"origin"`
	Evidence          []string   `json:"evidence,omitempty"`
	Artifacts         []string   `json:"artifacts,omitempty"`
	SuppressionReason string     `json:"suppression_reason,omitempty"`
}

// TaskOrigin is assigned by Control, never by model-supplied task arguments.
type TaskOrigin struct {
	Kind         string `json:"kind"`
	ScheduleID   string `json:"schedule_id,omitempty"`
	OccurrenceID string `json:"occurrence_id,omitempty"`
}

func (o TaskOrigin) Validate() error {
	if o.Kind == "manual" && o.ScheduleID == "" && o.OccurrenceID == "" {
		return nil
	}
	if o.Kind == "schedule" && core.Identifier(o.ScheduleID) && o.OccurrenceID != "" && len(o.OccurrenceID) <= 256 && !strings.ContainsRune(o.OccurrenceID, 0) {
		return nil
	}
	return fmt.Errorf("invalid Task origin")
}

type SendMail struct {
	To   AgentRef `json:"to"`
	Text string   `json:"text"`
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
