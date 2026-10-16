package agent

import (
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/internal/core"
	m "github.com/jjyr/pot/internal/modelprotocol"
	"strings"
)

func tokens(bytes int) uint64 {
	if bytes <= 0 {
		return 0
	}
	return uint64(bytes+3) / 4
}
func rawObject(value any) map[string]any { result, _ := value.(map[string]any); return result }
func rawArray(value any) []any           { result, _ := value.([]any); return result }
func rawString(value any) string         { result, _ := value.(string); return result }
func contentBytes(value any) int {
	switch v := value.(type) {
	case string:
		return len(v)
	case []any:
		total := 0
		for _, part := range v {
			total += contentBytes(part)
		}
		return total
	case map[string]any:
		switch v["type"] {
		case "text", "input_text", "output_text":
			return len(rawString(v["text"]))
		case "image", "image_url", "input_image":
			return 7373
		case "encrypted_content":
			return (len(rawString(v["encrypted_content"]))*9 + 15) / 16
		}
		if v["inlineData"] != nil {
			return 7373
		}
		if v["thought"] != true {
			return len(rawString(v["text"]))
		}
	}
	return 0
}
func callTokens(name, args string) uint64 { return tokens(len(name) + len("functions") + len(args)) }
func marshaled(value any) string          { data, _ := json.Marshal(value); return string(data) }
func responseItemTokens(item map[string]any) uint64 {
	switch item["type"] {
	case "message":
		return tokens(contentBytes(item["content"]))
	case "function_call", "custom_tool_call":
		return callTokens(rawString(item["name"]), rawString(item["arguments"]))
	case "function_call_output", "custom_tool_call_output":
		return tokens(contentBytes(item["output"]) + len(rawString(item["call_id"])) + len(rawString(item["name"])))
	case "reasoning", "compaction", "context_compaction":
		return tokens(max(len(rawString(item["encrypted_content"]))*3/4-650, 0))
	case "additional_tools", "tool_search_output":
		return tokens(len(marshaled(item["tools"])))
	case "tool_search_call":
		return tokens(len(marshaled(item["arguments"])))
	case "local_shell_call", "web_search_call":
		if item["action"] != nil {
			return tokens(len(marshaled(item["action"])))
		}
	case "agent_message":
		return tokens(len(rawString(item["author"])) + len(rawString(item["recipient"])) + contentBytes(item["content"]))
	case "image_generation_call":
		size := len(rawString(item["revised_prompt"]))
		if rawString(item["result"]) != "" {
			size += 7373
		}
		return tokens(size)
	}
	if item["content"] != nil {
		return tokens(contentBytes(item["content"]))
	}
	return 0
}

// EstimateTokens applies Codex-style UTF-8 content costs, rounding each logical
// item separately. This is a context estimate, never authoritative billed usage.
func EstimateTokens(request m.Request) (uint64, error) {
	if _, err := m.ContextPayload(request); err != nil {
		return 0, err
	}
	total := tokens(len(request.System))
	for _, message := range request.Messages {
		var replay any
		if len(message.Replay) > 0 {
			_ = json.Unmarshal(message.Replay, &replay)
		}
		native := false
		if message.Role == "assistant" {
			switch request.Protocol {
			case m.OpenAIChat:
				if object := rawObject(replay); object != nil {
					total += tokens(contentBytes(object["content"]) + len(message.Images)*7373)
					for _, call := range rawArray(object["tool_calls"]) {
						function := rawObject(rawObject(call)["function"])
						total += callTokens(rawString(function["name"]), rawString(function["arguments"]))
					}
					native = true
				}
			case m.OpenAIResponses:
				if items := rawArray(replay); items != nil {
					for _, item := range items {
						total += responseItemTokens(rawObject(item))
					}
					total += tokens(len(message.Images) * 7373)
					native = true
				}
			case m.AnthropicMessages:
				if blocks := rawArray(replay); blocks != nil {
					total += tokens(contentBytes(blocks))
					for _, item := range blocks {
						block := rawObject(item)
						switch block["type"] {
						case "tool_use":
							total += callTokens(rawString(block["name"]), marshaled(block["input"]))
						case "tool_result":
							total += tokens(contentBytes(block["content"]) + len(rawString(block["tool_use_id"])))
						}
					}
					native = true
				}
			case m.Gemini:
				if parts := rawArray(replay); parts != nil {
					total += tokens(contentBytes(parts) + len(message.Images)*7373)
					for _, item := range parts {
						part := rawObject(item)
						if call := rawObject(part["functionCall"]); call != nil {
							total += callTokens(rawString(call["name"]), marshaled(call["args"]))
						}
						if output := rawObject(part["functionResponse"]); output != nil {
							total += tokens(contentBytes(rawObject(output["response"])["output"]) + len(rawString(output["id"])))
						}
					}
					native = true
				}
			}
		}
		if native {
			continue
		}
		size := len(message.Text) + len(message.Images)*7373
		if message.Role == "tool" {
			size += len(message.CallID)
		}
		total += tokens(size)
		for _, call := range message.Calls {
			total += callTokens(call.Name, string(call.Arguments))
		}
	}
	if len(request.Tools) > 0 {
		data, err := json.Marshal(request.Tools)
		if err != nil {
			return 0, err
		}
		total += tokens(len(data))
	}
	return total, nil
}
func anchorFingerprint(request m.Request, messages []m.Message) (string, error) {
	request.Messages = messages
	payload, err := m.ContextPayload(request)
	if err != nil {
		return "", err
	}
	routes := []*string{}
	for _, message := range messages {
		routes = append(routes, message.ReplayRoute)
	}
	return core.HashJSON([]any{request.Protocol, request.Model, payload, routes})
}
func captureAnchor(request m.Request, messages []m.Message) (*UsageAnchor, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	usage := messages[len(messages)-1].Usage
	if usage == nil || ^uint64(0)-usage.Input < usage.Output || usage.Input+usage.Output == 0 {
		return nil, nil
	}
	hash, err := anchorFingerprint(request, messages)
	if err != nil {
		return nil, err
	}
	return &UsageAnchor{len(messages), hash, usage.Input + usage.Output}, nil
}
func (a *Agent) estimate(request m.Request, anchor *UsageAnchor, useUsage bool) (uint64, error) {
	estimate := a.EstimateTokens
	if estimate == nil {
		estimate = EstimateTokens
	}
	full, err := estimate(request)
	if err != nil {
		return 0, err
	}
	if anchor == nil || !useUsage || anchor.PrefixLength < 0 || anchor.PrefixLength > len(request.Messages) {
		return full, nil
	}
	prefix := request.Messages[:anchor.PrefixLength]
	hash, err := anchorFingerprint(request, prefix)
	if err != nil {
		return 0, err
	}
	if hash != anchor.Fingerprint {
		return full, nil
	}
	request.Messages = prefix
	base, err := estimate(request)
	if err != nil {
		return 0, err
	}
	growth := uint64(0)
	if full > base {
		growth = full - base
	}
	if ^uint64(0)-anchor.Tokens < growth {
		return 0, fmt.Errorf("context estimate overflow")
	}
	return anchor.Tokens + growth, nil
}
func lastUser(messages []m.Message) *m.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role == "user" && (message.Kind.Type == "conversation" || message.Kind.Type == "") {
			return &message
		}
	}
	return nil
}
func summaryMessage(text string) m.Message {
	message := m.TextMessage("user", "Previous context summary (reference data):\n"+strings.TrimSpace(text))
	message.Kind.Type = "summary"
	return message
}
