package modelprotocol

import (
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"strings"
)

func Encode(r Request) (json.RawMessage, error) {
	if r.Model == "" || r.MaxOutputTokens == 0 || r.MaxOutputTokens > core.MaxSafeInteger {
		return nil, fmt.Errorf("invalid model/output cap")
	}
	if err := r.Protocol.Validate(); err != nil {
		return nil, err
	}
	if r.Protocol == Unknown {
		return nil, fmt.Errorf("unsupported model protocol")
	}
	functions := []any{}
	seen := map[string]bool{}
	for _, t := range r.Tools {
		if t.Name == "" || seen[t.Name] {
			return nil, fmt.Errorf("duplicate or invalid tool name")
		}
		seen[t.Name] = true
		schema, err := object(t.InputSchema)
		if err != nil || schema["type"] != "object" {
			return nil, fmt.Errorf("tool inputSchema must be an object schema")
		}
		if props, exists := schema["properties"]; exists && obj(props) == nil {
			return nil, fmt.Errorf("tool properties must be an object")
		}
		if required, exists := schema["required"]; exists {
			if array(required) == nil {
				return nil, fmt.Errorf("tool required must be string array")
			}
			for _, key := range array(required) {
				if _, ok := key.(string); !ok {
					return nil, fmt.Errorf("tool required must be string array")
				}
			}
		}
		functions = append(functions, map[string]any{"name": t.Name, "description": t.Description, "parameters": schema})
	}
	for _, m := range r.Messages {
		if m.Role != "user" && m.Role != "assistant" && m.Role != "tool" && m.Role != "system" {
			return nil, fmt.Errorf("invalid model message role")
		}
		for _, image := range m.Images {
			if err := image.Validate(); err != nil {
				return nil, err
			}
		}
		for _, call := range m.Calls {
			if call.ID == "" || call.Name == "" {
				return nil, fmt.Errorf("invalid model tool call")
			}
			if _, err := object(call.Arguments); err != nil {
				return nil, fmt.Errorf("tool arguments: %w", err)
			}
		}
	}
	input := []any{}
	images := []any{}
	var body map[string]any
	merge := func(role, key string, parts []any) {
		if len(input) > 0 {
			previous := obj(input[len(input)-1])
			if previous["role"] == role {
				previous[key] = append(array(previous[key]), parts...)
				return
			}
		}
		input = append(input, map[string]any{"role": role, key: parts})
	}
	flushImages := func() {
		if len(images) > 0 {
			input = append(input, map[string]any{"role": "user", "content": images})
			images = []any{}
		}
	}
	switch r.Protocol {
	case OpenAIChat:
		input = append(input, map[string]any{"role": "system", "content": r.System})
		for _, m := range r.Messages {
			if m.Role != "tool" {
				flushImages()
			}
			var replay map[string]any
			if len(m.Replay) > 0 && m.Role == "assistant" {
				_ = json.Unmarshal(m.Replay, &replay)
			}
			if replay != nil {
				input = append(input, json.RawMessage(m.Replay))
			} else if m.Role == "tool" {
				input = append(input, map[string]any{"role": "tool", "tool_call_id": m.CallID, "content": m.Text})
			} else {
				message := map[string]any{"role": m.Role, "content": m.Text}
				if len(m.Calls) > 0 {
					calls := []any{}
					for _, call := range m.Calls {
						calls = append(calls, map[string]any{"id": call.ID, "type": "function", "function": map[string]any{"name": call.Name, "arguments": string(call.Arguments)}})
					}
					message["tool_calls"] = calls
				}
				input = append(input, message)
			}
			for _, image := range m.Images {
				images = append(images, map[string]any{"type": "image_url", "image_url": map[string]any{"url": image.DataURL(), "detail": "high"}})
			}
		}
		flushImages()
		body = map[string]any{"model": r.Model, "messages": input, "stream": true, "stream_options": map[string]any{"include_usage": true}, "max_tokens": r.MaxOutputTokens}
		for _, prefix := range []string{"gpt-5", "o1", "o3", "o4"} {
			if strings.HasPrefix(r.Model, prefix) {
				delete(body, "max_tokens")
				body["max_completion_tokens"] = r.MaxOutputTokens
			}
		}
		if len(functions) > 0 {
			tools := []any{}
			for _, f := range functions {
				tools = append(tools, map[string]any{"type": "function", "function": f})
			}
			body["tools"] = tools
		}
		if r.ReasoningEffort != nil {
			body["reasoning_effort"] = *r.ReasoningEffort
		}
	case OpenAIResponses:
		for _, m := range r.Messages {
			if m.Role != "tool" {
				flushImages()
			}
			var replay []any
			if len(m.Replay) > 0 && m.Role == "assistant" {
				_ = json.Unmarshal(m.Replay, &replay)
			}
			if replay != nil {
				input = append(input, replay...)
			} else if m.Role == "tool" {
				input = append(input, map[string]any{"type": "function_call_output", "call_id": m.CallID, "output": m.Text})
			} else {
				if m.Text != "" || len(m.Calls) == 0 {
					input = append(input, map[string]any{"role": m.Role, "content": m.Text})
				}
				for _, call := range m.Calls {
					input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Name, "arguments": string(call.Arguments)})
				}
			}
			for _, image := range m.Images {
				images = append(images, map[string]any{"type": "input_image", "image_url": image.DataURL(), "detail": "high"})
			}
		}
		flushImages()
		body = map[string]any{"model": r.Model, "instructions": r.System, "input": input, "stream": true, "store": false, "include": []string{"reasoning.encrypted_content"}, "max_output_tokens": r.MaxOutputTokens}
		if len(functions) > 0 {
			for _, f := range functions {
				obj(f)["type"] = "function"
				// Tool contracts use omission for optional arguments. Responses
				// may normalize an unspecified strict mode into required fields.
				obj(f)["strict"] = false
			}
			body["tools"] = functions
		}
		if r.ReasoningEffort != nil {
			body["reasoning"] = map[string]any{"effort": *r.ReasoningEffort}
		}
	case AnthropicMessages:
		for _, m := range r.Messages {
			role := m.Role
			if role == "tool" {
				role = "user"
			}
			parts := []any{}
			if m.Role == "assistant" && len(m.Replay) > 0 {
				_ = json.Unmarshal(m.Replay, &parts)
			}
			if len(parts) == 0 {
				if m.Role == "tool" {
					content := []any{map[string]any{"type": "text", "text": m.Text}}
					for _, image := range m.Images {
						content = append(content, anthropicImage(image))
					}
					parts = append(parts, map[string]any{"type": "tool_result", "tool_use_id": m.CallID, "content": content})
				} else {
					if m.Text != "" || len(m.Calls) == 0 {
						parts = append(parts, map[string]any{"type": "text", "text": m.Text})
					}
					for _, call := range m.Calls {
						parts = append(parts, map[string]any{"type": "tool_use", "id": call.ID, "name": call.Name, "input": call.Arguments})
					}
					for _, image := range m.Images {
						parts = append(parts, anthropicImage(image))
					}
				}
			}
			merge(role, "content", parts)
		}
		body = map[string]any{"model": r.Model, "system": r.System, "messages": input, "stream": true, "max_tokens": r.MaxOutputTokens}
		if len(functions) > 0 {
			tools := []any{}
			for _, f := range functions {
				tools = append(tools, map[string]any{"name": obj(f)["name"], "description": obj(f)["description"], "input_schema": obj(f)["parameters"]})
			}
			body["tools"] = tools
		}
		if r.ReasoningEffort != nil {
			body["thinking"] = map[string]any{"type": "adaptive"}
			body["output_config"] = map[string]any{"effort": *r.ReasoningEffort}
		}
	case Gemini:
		names := map[string]string{}
		for _, m := range r.Messages {
			for _, call := range m.Calls {
				names[call.ID] = call.Name
			}
		}
		for _, m := range r.Messages {
			role := "user"
			if m.Role == "assistant" {
				role = "model"
			}
			parts := []any{}
			if m.Role == "assistant" && len(m.Replay) > 0 {
				_ = json.Unmarshal(m.Replay, &parts)
			}
			if len(parts) == 0 {
				if m.Role == "tool" {
					name := names[m.CallID]
					if name == "" {
						return nil, fmt.Errorf("orphan Gemini tool result")
					}
					parts = append(parts, map[string]any{"functionResponse": map[string]any{"id": m.CallID, "name": name, "response": map[string]any{"output": m.Text}}})
				} else {
					if m.Text != "" || len(m.Calls) == 0 {
						parts = append(parts, map[string]any{"text": m.Text})
					}
					for _, call := range m.Calls {
						parts = append(parts, map[string]any{"functionCall": map[string]any{"id": call.ID, "name": call.Name, "args": call.Arguments}})
					}
				}
			}
			for _, image := range m.Images {
				parts = append(parts, map[string]any{"inlineData": map[string]any{"mimeType": image.MIMEType, "data": image.Data}})
			}
			merge(role, "parts", parts)
		}
		generation := map[string]any{"maxOutputTokens": r.MaxOutputTokens}
		body = map[string]any{"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": r.System}}}, "contents": input, "generationConfig": generation}
		if len(functions) > 0 {
			body["tools"] = []any{map[string]any{"functionDeclarations": functions}}
		}
		if r.ReasoningEffort != nil {
			generation["thinkingConfig"] = map[string]any{"thinkingLevel": strings.ToUpper(*r.ReasoningEffort)}
		}
	}
	return json.Marshal(body)
}
func anthropicImage(image ModelImage) any {
	return map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": image.MIMEType, "data": image.Data}}
}
func ContextPayload(r Request) (json.RawMessage, error) {
	r.MaxOutputTokens = 1
	data, err := Encode(r)
	if err != nil {
		return nil, err
	}
	body, err := object(data)
	if err != nil {
		return nil, err
	}
	keys := map[Protocol][]string{OpenAIChat: {"messages", "tools"}, OpenAIResponses: {"instructions", "input", "tools"}, AnthropicMessages: {"system", "messages", "tools"}, Gemini: {"systemInstruction", "contents", "tools"}}
	result := map[string]any{}
	for _, key := range keys[r.Protocol] {
		if value, found := body[key]; found {
			result[key] = value
		}
	}
	return json.Marshal(result)
}
