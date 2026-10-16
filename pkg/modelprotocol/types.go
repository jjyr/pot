// Package modelprotocol implements pure provider codecs. It has no credentials,
// network transport, persistence, product policy or tool dispatch authority.
package modelprotocol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"slices"
	"strings"
)

type Protocol string

const (
	OpenAIChat        Protocol = "openai_chat"
	OpenAIResponses   Protocol = "openai_responses"
	AnthropicMessages Protocol = "anthropic_messages"
	Gemini            Protocol = "gemini"
	Unknown           Protocol = "unknown"
)

func (p Protocol) Validate() error {
	if !slices.Contains([]Protocol{OpenAIChat, OpenAIResponses, AnthropicMessages, Gemini, Unknown}, p) {
		return fmt.Errorf("unsupported model protocol %q", p)
	}
	return nil
}
func (p Protocol) PathMatches(path, model string) bool {
	switch p {
	case OpenAIChat, Unknown:
		return path == "v1/chat/completions"
	case OpenAIResponses:
		return path == "v1/responses"
	case AnthropicMessages:
		return path == "v1/messages"
	case Gemini:
		return path == "v1/models/"+model+":streamGenerateContent"
	}
	return false
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}
type MessageKind struct {
	Type        string `json:"type"`
	Key         string `json:"key,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Active      bool   `json:"active,omitempty"`
}
type ModelImage struct {
	MIMEType string `json:"mime_type"`
	Data     string `json:"data"`
	Width    uint32 `json:"width"`
	Height   uint32 `json:"height"`
}

func (i ModelImage) DataURL() string { return "data:" + i.MIMEType + ";base64," + i.Data }
func (i ModelImage) Validate() error {
	if !slices.Contains([]string{"image/png", "image/jpeg", "image/webp"}, i.MIMEType) || i.Width == 0 || i.Width > 4096 || i.Height == 0 || i.Height > 4096 || len(i.Data) == 0 || len(i.Data) > 1400000 {
		return fmt.Errorf("invalid model image")
	}
	if _, err := base64.StdEncoding.DecodeString(i.Data); err != nil {
		return fmt.Errorf("invalid image base64: %w", err)
	}
	return nil
}

type TokenUsage struct {
	Input  uint64 `json:"input"`
	Output uint64 `json:"output"`
}
type Message struct {
	Kind        MessageKind     `json:"kind"`
	Images      []ModelImage    `json:"images,omitempty"`
	Role        string          `json:"role"`
	Text        string          `json:"text"`
	Calls       []ToolCall      `json:"calls,omitempty"`
	CallID      string          `json:"call_id,omitempty"`
	Replay      json.RawMessage `json:"replay,omitempty"`
	ReplayRoute *string         `json:"replay_route,omitempty"`
	Usage       *TokenUsage     `json:"usage,omitempty"`
}

func TextMessage(role, text string) Message {
	return Message{Kind: MessageKind{Type: "conversation"}, Role: role, Text: text}
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}
type Request struct {
	Protocol        Protocol
	Model           string
	System          string
	Messages        []Message
	Tools           []ToolDefinition
	MaxOutputTokens uint64
	ReasoningEffort *string
}
type Response = Message
type UsageReport struct {
	InputTokens     *uint64 `json:"prompt_tokens"`
	OutputTokens    *uint64 `json:"completion_tokens"`
	CacheReadTokens *uint64 `json:"prompt_cache_hit_tokens"`
}
type StreamEvent struct {
	TextDelta string
	Usage     *UsageReport
}

func object(data []byte) (map[string]any, error) {
	var v map[string]any
	if err := core.DecodeJSON(data, &v); err != nil {
		return nil, err
	}
	if v == nil {
		return nil, fmt.Errorf("expected JSON object")
	}
	return v, nil
}
func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }
func array(v any) []any        { a, _ := v.([]any); return a }
func str(v any) string         { s, _ := v.(string); return s }
func get(v any, keys ...string) any {
	for _, key := range keys {
		v = obj(v)[key]
	}
	return v
}
func uintValue(v any) *uint64 {
	n, ok := v.(json.Number)
	if !ok {
		return nil
	}
	if strings.ContainsAny(n.String(), ".eE-") {
		return nil
	}
	var value uint64
	if err := json.Unmarshal([]byte(n), &value); err != nil {
		return nil
	}
	return &value
}
func integer(v uint64) *uint64 { return &v }
func sum(a, b uint64) (uint64, error) {
	if ^uint64(0)-a < b {
		return 0, fmt.Errorf("provider usage overflow")
	}
	return a + b, nil
}
