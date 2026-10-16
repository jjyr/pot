package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/core"
)

type LiveLimits struct {
	MaxRequests       uint64 `json:"max_requests"`
	MaxReservedTokens uint64 `json:"max_reserved_tokens"`
}

func DefaultLiveLimits() LiveLimits { return LiveLimits{8, 100000} }

type LiveBudget struct {
	mu                 sync.Mutex
	limits             LiveLimits
	deadline           time.Time
	attempts, reserved uint64
}

func NewLiveBudget(limits LiveLimits, deadline time.Duration) (*LiveBudget, error) {
	if limits.MaxRequests == 0 || limits.MaxReservedTokens == 0 || deadline <= 0 {
		return nil, errors.New("invalid live budget")
	}
	return &LiveBudget{limits: limits, deadline: time.Now().Add(deadline)}, nil
}
func (b *LiveBudget) reserve(units uint64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Now().After(b.deadline) || b.attempts >= b.limits.MaxRequests || units > b.limits.MaxReservedTokens-b.reserved {
		return errors.New("live request/deadline/reservation budget reached")
	}
	b.attempts++
	b.reserved += units
	return nil
}
func (b *LiveBudget) Snapshot() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	return map[string]any{"attempts": b.attempts, "reserved_units": b.reserved, "limits": b.limits}
}

// Credential fields are private and deliberately have no JSON or String method.
type DeepSeekConnection struct {
	endpoint, key, model     string
	contextWindow, maxOutput uint64
	compat                   struct {
		ReasoningEffortMap       map[string]string `json:"reasoningEffortMap"`
		RequiresReasoningContent bool              `json:"requiresReasoningContentOnAssistantMessages"`
	}
	cost json.RawMessage
}

func ReadDeepSeek(path string) (*DeepSeekConnection, error) {
	var config struct {
		Providers map[string]struct {
			API     string            `json:"api"`
			BaseURL string            `json:"baseUrl"`
			APIKey  string            `json:"apiKey"`
			Headers map[string]string `json:"headers"`
			Models  []struct {
				ID            string          `json:"id"`
				API           string          `json:"api"`
				ContextWindow uint64          `json:"contextWindow"`
				MaxTokens     uint64          `json:"maxTokens"`
				Compat        json.RawMessage `json:"compat"`
				Cost          json.RawMessage `json:"cost"`
			} `json:"models"`
		} `json:"providers"`
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) > 4*1024*1024 {
		return nil, errors.New("Pi models configuration exceeds limit")
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		return nil, err
	}
	p, ok := config.Providers["deepseek"]
	if !ok || p.API != "openai-completions" || len(p.Headers) > 0 {
		return nil, errors.New("requires Pi DeepSeek openai-completions without custom headers")
	}
	u, err := url.Parse(p.BaseURL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("DeepSeek requires credential-free HTTPS endpoint")
	}
	if p.APIKey == "" || strings.HasPrefix(p.APIKey, "!") {
		return nil, errors.New("requires literal or environment Pi API key; commands unsupported")
	}
	key := p.APIKey
	if value, exists := os.LookupEnv(key); exists {
		key = value
	}
	if key == "" {
		return nil, errors.New("DeepSeek API key is empty")
	}
	var connection *DeepSeekConnection
	for _, m := range p.Models {
		if m.ID != "deepseek-v4-flash" && m.ID != "deepseek-flash" {
			continue
		}
		if connection != nil || m.API != "" && m.API != "openai-completions" || m.ContextWindow == 0 || m.MaxTokens == 0 {
			return nil, errors.New("requires exactly one supported configured DeepSeek Flash model")
		}
		connection = &DeepSeekConnection{endpoint: strings.TrimRight(p.BaseURL, "/") + "/chat/completions", key: key, model: m.ID, contextWindow: m.ContextWindow, maxOutput: m.MaxTokens, cost: m.Cost}
		if len(m.Compat) > 0 {
			if err = json.Unmarshal(m.Compat, &connection.compat); err != nil {
				return nil, err
			}
		}
	}
	if connection == nil {
		return nil, errors.New("Pi DeepSeek Flash model missing")
	}
	return connection, nil
}
func (c *DeepSeekConnection) configure(s *Scenario, limits LiveLimits) (json.RawMessage, error) {
	if len(s.Responses) != 0 || s.Profile.Params.MaxOutputTokens > c.maxOutput || s.Profile.Params.Agent.ContextWindow > c.contextWindow {
		return nil, errors.New("live scenario contains fixtures or exceeds Pi Flash token limits")
	}
	s.Profile.Provider = "deepseek"
	s.Profile.AuthRef = "provider:deepseek"
	s.Profile.Model = c.model
	s.Protocol = "openai_chat"
	thinking := "disabled"
	var effort *string
	if r := s.Profile.Params.ReasoningEffort; r != nil && *r != "none" {
		mapped := *r
		if v, ok := c.compat.ReasoningEffortMap[mapped]; ok {
			mapped = v
		}
		if mapped != "high" && mapped != "max" {
			return nil, errors.New("unsupported DeepSeek reasoning effort")
		}
		thinking = "enabled"
		effort = &mapped
	}
	return json.Marshal(map[string]any{"provider": "deepseek", "model": c.model, "endpoint": c.endpoint, "thinking": thinking, "reasoning_effort": effort, "pi_model_context_window": c.contextWindow, "pi_model_max_output": c.maxOutput, "pi_configured_price_per_million": c.cost, "price_verified": false, "limits": limits})
}

type liveServices struct {
	connection          *DeepSeekConnection
	directory           string
	transport           json.RawMessage
	client              *http.Client
	budget              *LiveBudget
	shared              []*LiveBudget
	mu                  sync.Mutex
	requests, responses *os.File
}

func (s *liveServices) appendRecord(response bool, value any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	file := &s.requests
	name := "requests.jsonl"
	if response {
		file = &s.responses
		name = "responses.jsonl"
	}
	if *file == nil {
		f, err := os.OpenFile(filepath.Join(s.directory, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return err
		}
		*file = f
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err = (*file).Write(append(raw, '\n')); err != nil {
		return err
	}
	return (*file).Sync()
}
func (s *liveServices) Model(ctx context.Context, r core.ModelRequest) (io.ReadCloser, error) {
	unsent := func(err error) (io.ReadCloser, error) {
		return nil, &core.ModelRequestError{Cause: err, Disposition: "unsent"}
	}
	if r.Provider != "deepseek" || r.Protocol != "openai_chat" || r.Model != s.connection.model {
		return unsent(errors.New("live request escaped configured Flash connection"))
	}
	var body map[string]any
	if err := core.DecodeJSON(r.Body, &body); err != nil {
		return unsent(err)
	}
	var config struct {
		Thinking string  `json:"thinking"`
		Effort   *string `json:"reasoning_effort"`
	}
	if err := json.Unmarshal(s.transport, &config); err != nil {
		return unsent(err)
	}
	body["thinking"] = map[string]string{"type": config.Thinking}
	delete(body, "reasoning_effort")
	if config.Effort != nil {
		body["reasoning_effort"] = *config.Effort
	}
	if s.connection.compat.RequiresReasoningContent {
		if messages, ok := body["messages"].([]any); ok {
			for _, m := range messages {
				if message, ok := m.(map[string]any); ok && message["role"] == "assistant" {
					if _, exists := message["reasoning_content"]; !exists {
						message["reasoning_content"] = ""
					}
				}
			}
		}
	}
	var output uint64
	encodedOutput, err := json.Marshal(body["max_tokens"])
	if err != nil {
		return unsent(err)
	}
	if err = json.Unmarshal(encodedOutput, &output); err != nil || output == 0 {
		return unsent(errors.New("live output cap missing"))
	}
	data, err := json.Marshal(body)
	if err != nil {
		return unsent(err)
	}
	if output > ^uint64(0)-uint64(len(data)) {
		return unsent(errors.New("live reservation overflow"))
	}
	bound := uint64(len(data)) + output
	if err = s.budget.reserve(bound); err != nil {
		return unsent(err)
	}
	for _, budget := range s.shared {
		if err = budget.reserve(bound); err != nil {
			return unsent(err)
		}
	}
	if err = s.appendRecord(false, map[string]any{"id": r.ID, "compaction_id": r.CompactionID, "body": body, "reserved_tokens": bound}); err != nil {
		return unsent(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.connection.endpoint, bytes.NewReader(data))
	if err != nil {
		return unsent(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+s.connection.key)
	response, err := s.client.Do(request)
	if err != nil {
		return nil, errors.New("DeepSeek connection failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, fmt.Errorf("DeepSeek HTTP status %d", response.StatusCode)
	}
	return &liveStream{ReadCloser: response.Body, services: s, id: r.ID}, nil
}
func (*liveServices) RPC(context.Context, *string, string, string, json.RawMessage) (json.RawMessage, *string, error) {
	return nil, nil, errors.New("external live tools are not configured")
}
func (*liveServices) Compaction(context.Context, json.RawMessage) error { return nil }
func (s *liveServices) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.requests != nil {
		s.requests.Close()
	}
	if s.responses != nil {
		s.responses.Close()
	}
}

type liveStream struct {
	io.ReadCloser
	services *liveServices
	id       string
	bytes    uint64
}

func (s *liveStream) Read(data []byte) (int, error) {
	n, err := s.ReadCloser.Read(data)
	if n > 0 {
		s.bytes += uint64(n)
		if s.bytes > 24*1024*1024 {
			return 0, errors.New("live response exceeds bound")
		}
		if e := s.services.appendRecord(true, map[string]any{"request_id": s.id, "bytes": data[:n]}); e != nil {
			return 0, e
		}
	}
	return n, err
}
func RunLive(ctx context.Context, scenario Scenario, directory string, connection *DeepSeekConnection, limits LiveLimits, composer agent.Composer) (Report, error) {
	transport, err := connection.configure(&scenario, limits)
	if err != nil {
		return Report{}, err
	}
	budget, err := NewLiveBudget(limits, time.Duration(scenario.DeadlineMS)*time.Millisecond)
	if err != nil {
		return Report{}, err
	}
	services := &liveServices{connection: connection, directory: directory, transport: transport, budget: budget, client: &http.Client{Timeout: time.Duration(scenario.DeadlineMS) * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	defer services.Close()
	return Run(ctx, scenario, directory, RunOptions{Composer: composer, Services: services, Mode: "native_policy_live", Transport: transport})
}

func liveHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
