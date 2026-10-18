package modelprotocol

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Responses text belongs to an output item, not to the whole response. In
// particular a commentary item may contain only a copied transport annotation.
// Retain native item boundaries and phases until that item can be classified.
type responseText struct {
	item      map[string]any
	parts     map[int]*strings.Builder
	done      bool
	streaming bool
}

// NewRequestDecoder classifies standalone commentary annotations only when the
// request uses Pot metadata. Explicit discussion of the tag in user content or
// reference quotes disables classification; prose/code/final items stay content.
func NewRequestDecoder(r Request) *Decoder {
	d := NewDecoder(r.Protocol)
	if r.Protocol != OpenAIResponses {
		return d
	}
	for _, m := range r.Messages {
		if m.Role == "user" {
			if strings.Contains(m.Text, "<pot_message_metadata>") {
				return d
			}
			for _, ref := range m.References {
				if strings.Contains(ref.Quote, "<pot_message_metadata>") {
					return d
				}
			}
		}
	}
	d.annotations = map[string]bool{}
	for _, m := range r.Messages {
		if m.Role == "assistant" {
			var replay []any
			if len(m.Replay) > 0 {
				_ = json.Unmarshal(m.Replay, &replay)
			}
			if replay != nil {
				if responseMessage(replay) == nil {
					continue
				}
			} else if m.Text == "" {
				continue
			}
		}
		if value := strings.TrimSpace(messageMetadata(m)); value != "" {
			d.annotations[value] = true
		}
	}
	return d
}

func (d *Decoder) responseDelta(e map[string]any) (string, error) {
	// Responses sequence numbers survive SSE reconnection. Equal token text
	// without a sequence number is still legitimate content, not a duplicate.
	if sequence := uintValue(e["sequence_number"]); sequence != nil {
		if d.responseSequence != nil && *sequence <= *d.responseSequence {
			return "", nil
		}
		d.responseSequence = sequence
	}
	if d.responses == nil {
		d.responses = map[int]*responseText{}
	}
	index := func(v any) (int, error) {
		n := uintValue(v)
		if n == nil {
			return 0, nil
		}
		if *n > 4096 {
			return 0, fmt.Errorf("model block index exceeds limit")
		}
		return int(*n), nil
	}
	i, err := index(e["output_index"])
	if err != nil {
		return "", err
	}
	state := func(i int) *responseText {
		s := d.responses[i]
		if s == nil {
			s = &responseText{parts: map[int]*strings.Builder{}}
			d.responses[i] = s
		}
		return s
	}
	switch e["type"] {
	case "response.output_item.added", "response.output_item.done":
		item := obj(e["item"])
		if item == nil {
			return "", fmt.Errorf("invalid Responses output item")
		}
		s := state(i)
		s.item = item
		s.done = e["type"] == "response.output_item.done"
		d.blocks[i] = item
	case "response.output_text.delta":
		s := state(i)
		if !s.done {
			part, err := index(e["content_index"])
			if err != nil {
				return "", err
			}
			if s.parts[part] == nil {
				s.parts[part] = &strings.Builder{}
			}
			s.parts[part].WriteString(str(e["delta"]))
			if s.streaming && part == 0 && len(s.parts) == 1 {
				return str(e["delta"]), nil
			}
		}
	case "response.completed":
		if output := array(get(e, "response", "output")); output != nil {
			d.blocks = map[int]map[string]any{}
			d.responses = map[int]*responseText{}
			for i, v := range output {
				item := obj(v)
				if item == nil {
					return "", fmt.Errorf("invalid Responses output item")
				}
				s := state(i)
				s.item = item
				s.done = true
				d.blocks[i] = item
			}
		} else {
			for _, s := range d.responses {
				s.done = true
			}
		}
		d.terminal = true
		d.reason = "stop"
	case "response.incomplete", "response.failed":
		d.terminal = true
		d.failed = fmt.Errorf("model response incomplete or failed")
	}
	var text strings.Builder
	keys := make([]int, 0, len(d.responses))
	for i := range d.responses {
		keys = append(keys, i)
	}
	sort.Ints(keys)
	for _, i := range keys {
		s := d.responses[i]
		value := s.text()
		if s.item != nil && s.item["type"] != "message" {
			continue
		}
		if d.annotationItem(s, value) {
			continue
		}
		// A possible annotation must be held across all delta/chunk boundaries until
		// its phase and complete item are known. Other prose continues streaming.
		if !s.done && d.annotationPrefix(value) {
			break
		}
		if value != "" {
			if text.Len() > 0 {
				text.WriteString("\n\n")
			}
			text.WriteString(value)
		}
	}
	next := text.String()
	previous := d.text.String()
	if strings.HasPrefix(next, previous) {
		// Once ordinary text is released, forward its common single-part
		// deltas without rebuilding the entire accumulated response each time.
		if e["type"] == "response.output_text.delta" && len(keys) > 0 && keys[len(keys)-1] == i {
			ready := true
			for _, key := range keys[:len(keys)-1] {
				if !d.responses[key].done {
					ready = false
				}
			}
			if ready && !d.annotationPrefix(d.responses[i].text()) {
				d.responses[i].streaming = true
			}
		}
		return strings.TrimPrefix(next, previous), nil
	}
	// Terminal snapshots remain authoritative for Finish. Never replay an already
	// emitted prefix after a reconnect or duplicate output_item.done event.
	return "", nil
}

func (s *responseText) text() string {
	if s.done && s.item != nil {
		var b strings.Builder
		for _, part := range array(s.item["content"]) {
			if obj(part)["type"] == "output_text" {
				b.WriteString(str(obj(part)["text"]))
			}
		}
		return b.String()
	}
	if len(s.parts) == 1 {
		for _, part := range s.parts {
			return part.String()
		}
	}
	keys := make([]int, 0, len(s.parts))
	for i := range s.parts {
		keys = append(keys, i)
	}
	sort.Ints(keys)
	var b strings.Builder
	for _, i := range keys {
		b.WriteString(s.parts[i].String())
	}
	return b.String()
}
func (d *Decoder) annotationItem(s *responseText, value string) bool {
	return s.done && s.item["type"] == "message" && s.item["role"] == "assistant" && s.item["phase"] == "commentary" && d.annotationValue(value)
}
func (d *Decoder) annotationPrefix(value string) bool {
	if len(d.annotations) == 0 {
		return false
	}
	value = strings.TrimLeft(value, " \t\r\n")
	const open = "<pot_message_metadata>"
	const close = "</pot_message_metadata>"
	if strings.HasPrefix(open, value) {
		return true
	}
	if !strings.HasPrefix(value, open) {
		return false
	}
	at := strings.Index(value, close)
	if at < 0 {
		return true
	}
	tail := strings.TrimSpace(value[at+len(close):])
	return tail == "" || tail == `\` || tail == `\n`
}

func (d *Decoder) annotationValue(value string) bool {
	if len(d.annotations) == 0 {
		return false
	}
	value = annotationText(value)
	if d.annotations[value] {
		return true
	}
	// An upstream may generate a new ID in the internal annotation format.
	// Recognize only the ID-only schema here; a generated ID does not establish
	// the identity of any real transcript message.
	const open = "<pot_message_metadata>"
	const close = "</pot_message_metadata>"
	if !strings.HasPrefix(value, open) || !strings.HasSuffix(value, close) {
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(value[len(open):len(value)-len(close)]), &fields) != nil || len(fields) != 1 {
		return false
	}
	var id string
	if json.Unmarshal(fields["message_id"], &id) != nil || len(id) != 26 {
		return false
	}
	for _, c := range id {
		if !(c >= 'A' && c <= 'Z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}

// A copied serialized annotation can contain its trailing newline as
// literal backslash-n. Accept that suffix only for a complete annotation.
func annotationText(value string) string {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(value), `\n`))
}

// Only a message with visible text can carry a transcript annotation. Empty
// placeholders and native reasoning/tool calls must not become tag-only turns.
func responseMessage(replay []any) map[string]any {
	for _, item := range replay {
		b := obj(item)
		if b["type"] == "message" && (&responseText{item: b, done: true}).text() != "" {
			return b
		}
	}
	return nil
}
