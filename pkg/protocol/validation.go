package protocol

import (
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
)

func (p *AgentPhase) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	switch AgentPhase(value) {
	case NeedsConfiguration, Ready, AgentFailed:
		*p = AgentPhase(value)
		return nil
	}
	return fmt.Errorf("unknown agent phase")
}
func (r *Role) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	switch Role(value) {
	case User, Assistant, Tool, System:
		*r = Role(value)
		return nil
	}
	return fmt.Errorf("unknown message role")
}
func (s *MessageStatus) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	switch MessageStatus(value) {
	case Accepted, Streaming, Complete, Failed, MessageCancelled:
		*s = MessageStatus(value)
		return nil
	}
	return fmt.Errorf("unknown message status")
}
func (p *TaskPhase) UnmarshalJSON(raw []byte) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if TaskPhase(value).Scope() == "" {
		return fmt.Errorf("unknown task phase")
	}
	*p = TaskPhase(value)
	return nil
}
func (b RequestBudget) Validate() error {
	if b.Used > core.MaxSafeInteger || b.Limit > core.MaxSafeInteger || b.MaxOutputTokens == 0 || b.MaxOutputTokens > core.MaxSafeInteger {
		return fmt.Errorf("budget outside safe integer range")
	}
	return nil
}
func (s Snapshot) Validate() error {
	if s.APIVersion != APIVersion || s.BootID == "" || s.Cursor < 0 || uint64(s.Cursor) > core.MaxSafeInteger || !core.Identifier(s.ConversationID) {
		return fmt.Errorf("invalid snapshot identity/version")
	}
	if s.Budget != nil {
		if err := s.Budget.Validate(); err != nil {
			return err
		}
	}
	for _, message := range s.Messages {
		if err := message.Validate(); err != nil {
			return err
		}
		if message.ConversationID != s.ConversationID {
			return fmt.Errorf("cross-conversation snapshot message")
		}
	}
	return nil
}
func (e *Event) UnmarshalJSON(raw []byte) error {
	type plain Event
	var value plain
	if err := core.DecodeJSON(raw, &value); err != nil {
		return err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	expected := map[string]string{"budget": "budget", "agent": "status", "message": "message", "turn": "turn"}[value.Type]
	if expected == "" {
		return fmt.Errorf("unknown event type")
	}
	if _, present := fields[expected]; !present {
		return fmt.Errorf("missing event payload")
	}
	for _, key := range []string{"budget", "status", "message", "turn"} {
		if _, present := fields[key]; present && key != expected {
			return fmt.Errorf("unexpected event payload %s", key)
		}
	}
	*e = Event(value)
	return e.Validate()
}
