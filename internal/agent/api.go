// Package agent implements the injected model/tool loop. Persistence, model
// access, authorization, process execution and lifecycle belong to its caller.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/composition"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/modelprotocol"
)

var ErrUncertainEffects = errors.New("agent checkpoint has an operation with unconfirmed effects")

type SteeringInput struct {
	ID   string
	Text string
}
type Observation struct {
	Kind         string                     `json:"kind"`
	ID           string                     `json:"id,omitempty"`
	Stage        string                     `json:"stage,omitempty"`
	CompactionID *string                    `json:"compaction_id,omitempty"`
	Usage        *modelprotocol.UsageReport `json:"usage,omitempty"`
	Error        *string                    `json:"error,omitempty"`
	Tokens       uint64                     `json:"tokens,omitempty"`
	Threshold    uint64                     `json:"threshold,omitempty"`
}

// Feedback acknowledgments must validate owner/generation/turn. SaveContext is
// durable before return; Steering IDs are acknowledged only after saving them.
type Feedback interface {
	LoadContext(context.Context) (json.RawMessage, error)
	SaveContext(context.Context, json.RawMessage) error
	RecordTurn(context.Context, string) error
	InputStreaming(context.Context, string) error
	Ready(context.Context) error
	Steering(context.Context, bool) ([]SteeringInput, error)
	AcknowledgeSteering(context.Context, []string) error
	Observe(context.Context, Observation) error
	Settle(context.Context) error
}

// Dispatch is the ordinary authorized tool path, including approvals, target
// resolution, stable child identity, durable receipts and cleanup fencing. The
// Agent never directly executes tools or bypasses it for composition children.
type Dispatch func(context.Context, core.ExecutionContext, modelprotocol.ToolCall, func(core.Update) error) (json.RawMessage, error)
type Composer interface {
	Run(context.Context, string, map[string]composition.Tool, func(string) error) error
}
type Cleanup func(context.Context, core.ExecutionContext, bool) error
type TokenEstimator func(modelprotocol.Request) (uint64, error)
type PrepareContext func(context.Context, core.ExecutionContext, []modelprotocol.Message) ([]modelprotocol.Message, error)
type SkillProvider func(context.Context, core.ExecutionContext) (SkillCatalog, error)
type Agent struct {
	Services       core.ExecutionServices
	Feedback       Feedback
	Dispatch       Dispatch
	Cleanup        Cleanup
	Tools          []modelprotocol.ToolDefinition
	Composer       Composer
	EstimateTokens TokenEstimator
	PrepareContext PrepareContext
	Skills         SkillProvider
	MaxRounds      int
}

// Checkpoint is owner-held persisted model history, distinct from public/UI
// messages. Pending and Started form the crash boundary for external effects.
type Checkpoint struct {
	Type             string                   `json:"type"`
	Version          uint32                   `json:"version"`
	Messages         []modelprotocol.Message  `json:"messages"`
	Pending          []modelprotocol.ToolCall `json:"pending"`
	Started          *string                  `json:"started"`
	LastRequest      string                   `json:"last_request"`
	Receipts         []ToolReceipt            `json:"receipts"`
	SteeringIDs      []string                 `json:"steering_ids"`
	UsageAnchor      *UsageAnchor             `json:"usage_anchor"`
	ModelRequestID   *string                  `json:"model_request_id"`
	CompletedRequest string                   `json:"completed_request"`
}
type ToolReceipt struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Result    json.RawMessage `json:"result"`
}
type UsageAnchor struct {
	PrefixLength int    `json:"prefix_len"`
	Fingerprint  string `json:"fingerprint"`
	Tokens       uint64 `json:"tokens"`
}
