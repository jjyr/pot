package modelprotocol

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type Decoder struct {
	protocol    Protocol
	text        strings.Builder
	blocks      map[int]map[string]any
	arguments   map[int]string
	terminal    bool
	finished    bool
	failed      error
	reason      string
	reasoning   strings.Builder
	observer    UsageObserver
	usage       *TokenUsage
	streamBytes int
}

func NewDecoder(protocol Protocol) *Decoder {
	return &Decoder{protocol: protocol, blocks: map[int]map[string]any{}, arguments: map[int]string{}}
}
func (d *Decoder) Push(raw json.RawMessage) (result StreamEvent, err error) {
	defer func() {
		if err != nil {
			d.failed = err
		}
	}()
	if d.finished {
		return result, fmt.Errorf("model decoder already finished")
	}
	if d.failed != nil {
		return result, d.failed
	}
	if err = d.protocol.Validate(); err != nil {
		return result, err
	}
	d.streamBytes += len(raw)
	if d.streamBytes > 64*1024*1024 {
		return result, fmt.Errorf("model stream exceeds limit")
	}
	e, err := object(raw)
	if err != nil {
		return result, err
	}
	if _, exists := e["error"]; exists || e["type"] == "error" {
		return result, fmt.Errorf("model stream reported an error")
	}
	result.Usage, err = d.observer.observe(d.protocol, e)
	if err != nil {
		return result, err
	}
	if u := result.Usage; u != nil && u.InputTokens != nil && u.OutputTokens != nil {
		total, overflow := sum(*u.InputTokens, *u.OutputTokens)
		if overflow == nil && total > 0 {
			d.usage = &TokenUsage{*u.InputTokens, *u.OutputTokens}
		}
	}
	// Usage can follow terminal Chat data. Repeated terminal snapshots must not
	// append content or duplicate tool calls; their cumulative usage is still seen.
	if d.terminal {
		return result, nil
	}
	var delta string
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
	switch d.protocol {
	case OpenAIChat:
		choices := array(e["choices"])
		if len(choices) > 0 {
			c := obj(choices[0])
			delta = str(get(c, "delta", "content"))
			d.reasoning.WriteString(str(get(c, "delta", "reasoning_content")))
			for _, v := range array(get(c, "delta", "tool_calls")) {
				call := obj(v)
				i, e := index(call["index"])
				if e != nil {
					return result, e
				}
				block := d.blocks[i]
				if block == nil {
					block = map[string]any{"id": "", "type": "function", "function": map[string]any{"name": "", "arguments": ""}}
					d.blocks[i] = block
				}
				block["id"] = str(block["id"]) + str(call["id"])
				function := obj(block["function"])
				for _, key := range []string{"name", "arguments"} {
					function[key] = str(function[key]) + str(get(call, "function", key))
				}
			}
			if reason, ok := c["finish_reason"].(string); ok {
				d.reason = reason
				d.terminal = true
			}
		}
	case OpenAIResponses:
		switch e["type"] {
		case "response.output_text.delta":
			delta = str(e["delta"])
		case "response.output_item.done":
			i, e2 := index(e["output_index"])
			if e2 != nil {
				return result, e2
			}
			if obj(e["item"]) == nil {
				return result, fmt.Errorf("invalid Responses output item")
			}
			d.blocks[i] = obj(e["item"])
		case "response.completed":
			if output := array(get(e, "response", "output")); output != nil {
				d.blocks = map[int]map[string]any{}
				for i, v := range output {
					if obj(v) == nil {
						return result, fmt.Errorf("invalid Responses output item")
					}
					d.blocks[i] = obj(v)
				}
			}
			d.terminal = true
			d.reason = "stop"
		case "response.incomplete", "response.failed":
			d.terminal = true
			d.failed = fmt.Errorf("model response incomplete or failed")
		}
	case AnthropicMessages:
		switch e["type"] {
		case "content_block_start":
			i, e2 := index(e["index"])
			if e2 != nil {
				return result, e2
			}
			block := obj(e["content_block"])
			if block == nil {
				return result, fmt.Errorf("invalid Anthropic block")
			}
			if _, exists := d.blocks[i]; exists {
				return result, fmt.Errorf("duplicate Anthropic block")
			}
			d.blocks[i] = block
			if block["type"] == "text" {
				delta = str(block["text"])
			}
		case "content_block_delta":
			i, e2 := index(e["index"])
			if e2 != nil {
				return result, e2
			}
			block := d.blocks[i]
			if block == nil {
				return result, fmt.Errorf("unknown Anthropic block")
			}
			change := obj(e["delta"])
			switch change["type"] {
			case "text_delta":
				delta = str(change["text"])
				block["text"] = str(block["text"]) + delta
			case "input_json_delta":
				d.arguments[i] += str(change["partial_json"])
			case "thinking_delta":
				block["thinking"] = str(block["thinking"]) + str(change["thinking"])
			case "signature_delta":
				block["signature"] = str(block["signature"]) + str(change["signature"])
			}
		case "message_delta":
			if reason, ok := get(e, "delta", "stop_reason").(string); ok {
				d.reason = reason
			}
		case "message_stop":
			d.terminal = true
		}
	case Gemini:
		if str(get(e, "promptFeedback", "blockReason")) != "" {
			return result, fmt.Errorf("Gemini blocked prompt")
		}
		candidates := array(e["candidates"])
		if len(candidates) > 0 {
			c := obj(candidates[0])
			for _, part := range array(get(c, "content", "parts")) {
				p := obj(part)
				if p == nil {
					return result, fmt.Errorf("invalid Gemini part")
				}
				if p["thought"] != true {
					delta += str(p["text"])
				}
				d.blocks[len(d.blocks)] = p
			}
			if reason, ok := c["finishReason"].(string); ok {
				d.reason = reason
				d.terminal = true
			}
		}
	default:
		return result, fmt.Errorf("unsupported model protocol")
	}
	d.text.WriteString(delta)
	if d.text.Len() > 16*1024*1024 {
		return result, fmt.Errorf("model text exceeds limit")
	}
	result.TextDelta = delta
	return result, nil
}
func (d *Decoder) Finish() (Message, error) {
	if d.finished {
		return Message{}, fmt.Errorf("model decoder already finished")
	}
	d.finished = true
	if d.failed != nil {
		return Message{}, d.failed
	}
	if !d.terminal {
		return Message{}, fmt.Errorf("model stream ended without terminal response")
	}
	switch d.reason {
	case "stop", "tool_calls", "end_turn", "tool_use", "STOP":
	default:
		return Message{}, fmt.Errorf("model response ended with %q", d.reason)
	}
	keys := make([]int, 0, len(d.blocks))
	for i := range d.blocks {
		keys = append(keys, i)
	}
	sort.Ints(keys)
	calls := []ToolCall{}
	seen := map[string]bool{}
	blocks := []any{}
	for _, i := range keys {
		b := d.blocks[i]
		var id, name string
		var args any
		isCall := false
		switch d.protocol {
		case OpenAIChat:
			isCall = true
			id = str(b["id"])
			name = str(get(b, "function", "name"))
			a, err := object([]byte(str(get(b, "function", "arguments"))))
			if err != nil {
				return Message{}, fmt.Errorf("invalid model tool arguments: %w", err)
			}
			args = a
		case OpenAIResponses:
			if b["type"] == "function_call" {
				isCall = true
				id = str(b["call_id"])
				name = str(b["name"])
				a, err := object([]byte(str(b["arguments"])))
				if err != nil {
					return Message{}, fmt.Errorf("invalid model tool arguments: %w", err)
				}
				args = a
			}
		case AnthropicMessages:
			if b["type"] == "tool_use" {
				isCall = true
				id = str(b["id"])
				name = str(b["name"])
				if arguments, exists := d.arguments[i]; exists {
					a, err := object([]byte(arguments))
					if err != nil {
						return Message{}, fmt.Errorf("invalid Anthropic tool arguments: %w", err)
					}
					b["input"] = a
				}
				args = b["input"]
			}
		case Gemini:
			if c := obj(b["functionCall"]); c != nil {
				isCall = true
				id = str(c["id"])
				if id == "" {
					id = fmt.Sprintf("gemini-%d", i)
					c["id"] = id
				}
				name = str(c["name"])
				args = c["args"]
				if args == nil {
					args = map[string]any{}
				}
			}
		}
		if isCall {
			if id == "" || name == "" || obj(args) == nil || seen[id] {
				return Message{}, fmt.Errorf("invalid or duplicate model tool call")
			}
			seen[id] = true
			arguments, err := json.Marshal(args)
			if err != nil {
				return Message{}, err
			}
			calls = append(calls, ToolCall{id, name, arguments})
		}
		blocks = append(blocks, b)
	}
	if len(calls) > 128 {
		return Message{}, fmt.Errorf("too many tool calls")
	}
	message := TextMessage("assistant", d.text.String())
	message.Calls = calls
	message.Usage = d.usage
	var replay any = blocks
	if d.protocol == OpenAIChat {
		chat := map[string]any{"role": "assistant", "content": nil}
		if message.Text != "" {
			chat["content"] = message.Text
		}
		if len(blocks) > 0 {
			chat["tool_calls"] = blocks
		}
		if d.reasoning.Len() > 0 {
			chat["reasoning_content"] = d.reasoning.String()
		}
		replay = chat
	}
	if d.protocol == OpenAIResponses {
		var text strings.Builder
		for _, item := range blocks {
			if obj(item)["type"] == "message" {
				for _, part := range array(obj(item)["content"]) {
					text.WriteString(str(obj(part)["text"]))
				}
			}
		}
		if text.Len() > 0 || message.Text == "" {
			message.Text = text.String()
		}
		if len(message.Text) > 16*1024*1024 {
			return Message{}, fmt.Errorf("model text exceeds limit")
		}
	}
	var err error
	message.Replay, err = json.Marshal(replay)
	return message, err
}
