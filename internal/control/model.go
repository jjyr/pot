// Package control owns business policy and binds credential-free execution ports
// to provider, allowance and approval services at the owning Control.
package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/internal/modelprotocol"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/usage"
)

const MaxModelBody = 16 * 1024 * 1024

// ModelSession is fixed to one accepted execution. Claim runs under the business
// owner's turn lock immediately before the durable network claim. It must reject
// cancelled/replaced turns and enforce lifetime/per-turn request counts. Returning
// an error means no network attempt occurred. It must not recursively call Model.
type ModelSession struct {
	Registry     *gateway.Registry
	Usage        *usage.Ledger
	Spec         core.ResolvedSpec
	Scope, Turn  string
	WaitScope    string
	Generation   uint64
	Role         string
	Claim        func(context.Context, string) error
	Clock        func() time.Time
	mu           sync.Mutex
	requestTimes map[string]int64
}

func (s *ModelSession) Model(ctx context.Context, r core.ModelRequest) (stream io.ReadCloser, modelErr error) {
	knownUnsent := true
	defer func() {
		if modelErr != nil && knownUnsent {
			modelErr = &core.ModelRequestError{Cause: modelErr, Disposition: "unsent"}
		}
	}()
	if s.Registry == nil || s.Usage == nil || s.Claim == nil || s.Scope == "" || s.Turn == "" || s.Generation == 0 || s.Generation > core.MaxSafeInteger {
		return nil, errors.New("incomplete Control model session")
	}
	if !validRequestTimeout(s.Spec.Params.Agent.RequestTimeoutMS) {
		return nil, errors.New("model request timeout exceeds supported duration")
	}
	if err := s.Spec.Validate(); err != nil {
		return nil, err
	}
	if r.ID == "" || r.Protocol != s.Spec.GatewayProtocol || r.Provider != s.Spec.Provider || r.Model != s.Spec.Model || len(r.Body) > MaxModelBody {
		return nil, errors.New("model request differs from fixed execution configuration")
	}
	provider, err := s.Registry.Get(ctx, s.Spec.Provider)
	if err != nil {
		return nil, err
	}
	if provider.Protocol != s.Spec.GatewayProtocol {
		return nil, errors.New("provider protocol changed from execution snapshot")
	}
	var fields map[string]json.RawMessage
	if err = core.DecodeJSON(r.Body, &fields); err != nil {
		return nil, err
	}
	if provider.Protocol != "gemini" {
		var model string
		var stream bool
		if json.Unmarshal(fields["model"], &model) != nil || json.Unmarshal(fields["stream"], &stream) != nil || model != s.Spec.Model || !stream {
			return nil, errors.New("invalid model or streaming mode")
		}
	}
	p := modelprotocol.Protocol(provider.Protocol)
	body, cap, err := p.Clamp(r.Body, s.Spec.Params.MaxOutputTokens)
	if err != nil {
		return nil, err
	}
	if provider.Protocol == "openai_chat" && provider.APIProvider == "deepseek" {
		fields = nil
		if err = json.Unmarshal(body, &fields); err != nil {
			return nil, err
		}
		fields["thinking"] = json.RawMessage(`{"type":"disabled"}`)
		body, err = json.Marshal(fields)
		if err != nil {
			return nil, err
		}
	}
	at := time.Now()
	if s.Clock != nil {
		at = s.Clock()
	}
	s.mu.Lock()
	if s.requestTimes == nil {
		s.requestTimes = map[string]int64{}
	}
	atMS, exists := s.requestTimes[r.ID]
	if !exists {
		atMS = at.UnixMilli()
		s.requestTimes[r.ID] = atMS
	}
	s.mu.Unlock()
	if r.CompactionID != nil && *r.CompactionID != "conversation" {
		if err = s.Usage.ValidateCompaction(ctx, s.Scope, s.Turn, *r.CompactionID); err != nil {
			return nil, err
		}
	}
	waitScope := s.WaitScope
	if waitScope == "" {
		waitScope = s.Scope
	}
	// A late original resume may reach a later allowance boundary in the same
	// run. The pre-request boundary independently enforces its current manual gate.
	if waiting, e := s.Usage.Waiting(ctx, waitScope); e != nil {
		return nil, e
	} else if waiting != nil {
		if waiting.Turn != s.Turn {
			return nil, protocol.APIError{Code: "pot_token_limit", Message: "Allowance wait belongs to another original turn"}
		}
		ready, e := s.Usage.Ready(ctx, waitScope, at.UnixMilli())
		if e != nil {
			return nil, e
		}
		if !ready {
			return nil, protocol.APIError{Code: "pot_token_limit", Message: "Pot token allowance requires its original wait to be released"}
		}
	}
	request := usage.ReserveRequest{ID: r.ID, Scope: s.Scope, Turn: s.Turn, AtMS: atMS, Model: s.Spec.Model, InputEstimate: int64(len(body)) + 1024, MaxOutput: int64(cap), Route: &usage.Route{Provider: provider.ID, Generation: int64(s.Generation), SpecHash: s.Spec.SpecHash, Agent: s.Role, ProfileID: s.Spec.SourceProfileID}}
	reservation, err := s.Usage.ReserveRouted(ctx, request)
	if err != nil {
		knownUnsent = false
		return nil, err
	}
	if reservation == nil {
		if err = s.Usage.Wait(ctx, usage.WaitRequest{Scope: waitScope, Turn: s.Turn, Provider: provider.ID, AtMS: atMS, Required: request.InputEstimate + 1}); err != nil {
			return nil, err
		}
		return nil, protocol.APIError{Code: "pot_token_limit", Message: "Pot token allowance is unavailable; this turn is paused before sending the model request"}
	}
	if reservation.Duplicate {
		knownUnsent = false
		return nil, protocol.APIError{Code: "unknown_model_request", Message: "Model request already reserved; inspect its original receipt instead of resending"}
	}
	if err = s.Usage.ClearWaitForTurn(ctx, waitScope, s.Turn); err != nil {
		s.Usage.Rejected(ctx, r.ID)
		return nil, err
	}
	body, _, err = p.Clamp(body, uint64(reservation.OutputCap))
	if err != nil {
		s.Usage.Rejected(ctx, r.ID)
		return nil, err
	}
	if r.CompactionID != nil && *r.CompactionID != "conversation" {
		if err = s.Usage.TagCompaction(ctx, r.ID, *r.CompactionID); err != nil {
			s.Usage.Rejected(ctx, r.ID)
			return nil, err
		}
	}
	// Resolve the credential before claiming: missing keys cannot consume a model
	// attempt. Never pass this key through Agent/Runner services or error messages.
	key, err := s.Registry.Credential(ctx, provider)
	if err != nil {
		s.Usage.Rejected(ctx, r.ID)
		return nil, err
	}
	defer clear(key)
	if err = s.Claim(ctx, r.ID); err != nil {
		s.Usage.Rejected(ctx, r.ID)
		return nil, err
	}
	claimed, err := s.Usage.Claim(ctx, r.ID)
	if err != nil {
		knownUnsent = false
		return nil, err
	}
	if !claimed {
		knownUnsent = false
		return nil, errors.New("model request already claimed or uncertain")
	}
	knownUnsent = false
	response, err := gateway.Forward(ctx, provider, key, body, "", time.Duration(s.Spec.Params.Agent.RequestTimeoutMS)*time.Millisecond)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		if err = s.Usage.Rejected(ctx, r.ID); err != nil {
			return nil, err
		}
		return nil, &core.ModelRequestError{Cause: gateway.StatusError(response.StatusCode), Disposition: "rejected"}
	}
	return &meteredStream{body: response.Body, ledger: s.Usage, id: r.ID, protocol: p}, nil
}

// meteredStream observes native SSE without changing forwarded bytes. Partial or
// missing usage retains conservative reservations, including disconnect/restart.
type meteredStream struct {
	body     io.ReadCloser
	ledger   *usage.Ledger
	id       string
	protocol modelprotocol.Protocol
	sse      modelprotocol.SSE
	observer modelprotocol.UsageObserver
	finished bool
}

func (m *meteredStream) Read(data []byte) (int, error) {
	n, readErr := m.body.Read(data)
	if n > 0 && m.protocol != modelprotocol.Unknown {
		frames, err := m.sse.Push(data[:n])
		if err != nil {
			return n, err
		}
		for _, frame := range frames {
			report, err := m.observer.Observe(m.protocol, frame)
			if err != nil {
				return n, err
			}
			if report == nil {
				continue
			}
			convert := func(v *uint64) (*int64, error) {
				if v == nil {
					return nil, nil
				}
				if *v > core.MaxSafeInteger {
					return nil, errors.New("provider usage exceeds supported integer precision")
				}
				n := int64(*v)
				return &n, nil
			}
			input, err := convert(report.InputTokens)
			if err != nil {
				return n, err
			}
			output, err := convert(report.OutputTokens)
			if err != nil {
				return n, err
			}
			cache, err := convert(report.CacheReadTokens)
			if err != nil {
				return n, err
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err = m.ledger.Settle(ctx, m.id, usage.Frame{Input: input, Output: output, Cache: cache})
			cancel()
			if err != nil {
				return n, err
			}
		}
	}
	if readErr == io.EOF && !m.finished {
		m.finished = true
		if m.protocol != modelprotocol.Unknown {
			if err := m.sse.Finish(); err != nil {
				return n, err
			}
		}
	}
	return n, readErr
}
func (m *meteredStream) Close() error { return m.body.Close() }
