package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	m "github.com/jjyr/pot/pkg/modelprotocol"
	"sort"
	"strings"
)

func fingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
func renderSection(key, text string) m.Message {
	category := "reference_data"
	if key == "instructions" || key == "plugins" || key == "skills" {
		category = "instructions"
	}
	active := strings.TrimSpace(text) != ""
	body := text
	if !active {
		body = "This section is no longer available; its previous contents no longer apply."
	}
	message := m.TextMessage("user", fmt.Sprintf("<pot_context key=\"%s\" category=\"%s\">\nThis replaces all earlier contents of this section.\n%s\n</pot_context>", key, category, body))
	message.Kind = m.MessageKind{Type: "context", Key: key, Fingerprint: fingerprint(text), Active: active}
	return message
}

// SynchronizeSections only appends changed classified context. Disappearing
// sections receive explicit revocations; conversation input is never rewritten.
func SynchronizeSections(messages []m.Message, sections []core.ContextSection) ([]m.Message, error) {
	current := map[string]string{}
	previous := map[string]m.MessageKind{}
	for _, section := range sections {
		if section.Key == "" || len(section.Key) > 64 {
			return nil, fmt.Errorf("invalid context section key")
		}
		for _, c := range section.Key {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return nil, fmt.Errorf("invalid context section key")
			}
		}
		if _, exists := current[section.Key]; exists {
			return nil, fmt.Errorf("duplicate context section key")
		}
		current[section.Key] = strings.TrimSpace(strings.ReplaceAll(section.Text, "\r\n", "\n"))
	}
	for _, message := range messages {
		if message.Kind.Type == "context" {
			previous[message.Kind.Key] = message.Kind
		}
	}
	for key := range previous {
		if _, exists := current[key]; !exists {
			current[key] = ""
		}
	}
	keys := []string{}
	for key := range current {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := append([]m.Message(nil), messages...)
	for _, key := range keys {
		text := current[key]
		prior, exists := previous[key]
		if exists && prior.Fingerprint == fingerprint(text) || text == "" && (!exists || !prior.Active) {
			continue
		}
		result = append(result, renderSection(key, text))
	}
	return result, nil
}
func route(spec core.ResolvedSpec) string {
	return spec.Provider + ":" + spec.Model + ":" + spec.GatewayProtocol
}
func portable(spec core.ResolvedSpec, messages []m.Message, vision bool) []m.Message {
	result := make([]m.Message, len(messages))
	for i, message := range messages {
		if !vision {
			message.Images = nil
		}
		if message.ReplayRoute == nil || *message.ReplayRoute != route(spec) {
			message.Replay = nil
			message.ReplayRoute = nil
		}
		result[i] = message
	}
	return result
}

// ReconcileContext only installs confirmed immutable results. Missing receipts
// never become successes and no model or tool is invoked by reconciliation.
// Once all started tools are reconciled, an interrupted model sample can be
// abandoned: none of its uncommitted output has been dispatched. The caller must
// independently confirm original execution cleanup before installing this state.
func ReconcileContext(raw json.RawMessage, receipts map[string]json.RawMessage, vision bool) (json.RawMessage, error) {
	var state Checkpoint
	if err := core.DecodeJSON(raw, &state); err != nil {
		return nil, err
	}
	if err := state.validate(); err != nil {
		return nil, err
	}
	pending := []m.ToolCall{}
	for _, call := range state.Pending {
		result, found := receipts[call.ID]
		if !found {
			pending = append(pending, call)
			continue
		}
		message, err := toolResultMessage(call.ID, result, vision)
		if err != nil {
			return nil, err
		}
		state.Messages = append(state.Messages, message)
		state.Receipts = append(state.Receipts, ToolReceipt{call.ID, call.Name, call.Arguments, result})
		if state.Started != nil && *state.Started == call.ID {
			state.Started = nil
		}
	}
	state.Pending = pending
	if state.Started == nil {
		state.ModelRequestID = nil
	}
	return json.Marshal(state)
}
func (state Checkpoint) validate() error {
	if state.Type != "context" || state.Version != 2 || state.Messages == nil {
		return fmt.Errorf("invalid Pot context")
	}
	if state.Started != nil {
		found := false
		for _, call := range state.Pending {
			if call.ID == *state.Started {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("started operation missing from pending context")
		}
	}
	return nil
}
func toolResultMessage(call string, raw json.RawMessage, vision bool) (m.Message, error) {
	var value map[string]any
	if err := core.DecodeJSON(raw, &value); err != nil {
		return m.Message{}, err
	}
	images := []m.ModelImage{}
	if content, ok := value["content"].([]any); ok {
		filtered := []any{}
		for _, item := range content {
			object, _ := item.(map[string]any)
			if object["type"] != "image" {
				filtered = append(filtered, item)
				continue
			}
			if !vision {
				return m.Message{}, fmt.Errorf("image result unavailable for text-only model")
			}
			data, _ := json.Marshal(object)
			var metadata struct {
				MIMEType string `json:"mimeType"`
				Data     string `json:"data"`
				Width    uint32 `json:"width"`
				Height   uint32 `json:"height"`
			}
			if err := json.Unmarshal(data, &metadata); err != nil {
				return m.Message{}, err
			}
			image := m.ModelImage{MIMEType: metadata.MIMEType, Data: metadata.Data, Width: metadata.Width, Height: metadata.Height}
			if err := image.Validate(); err != nil {
				return m.Message{}, err
			}
			images = append(images, image)
		}
		value["content"] = filtered
	}
	text, err := json.Marshal(value)
	if err != nil {
		return m.Message{}, err
	}
	message := m.TextMessage("tool", string(text))
	message.CallID = call
	message.Images = images
	return message, nil
}

// toolFailure retains structured execution diagnostics and any partial result.
func toolFailure(raw json.RawMessage, err error) json.RawMessage {
	value := map[string]any{}
	_ = core.DecodeJSON(raw, &value)
	if value == nil {
		value = map[string]any{}
	}
	value["isError"] = true
	value["error"] = err.Error()
	result, _ := json.Marshal(value)
	return result
}
