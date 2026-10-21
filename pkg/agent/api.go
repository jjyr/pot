// Package agent implements the injected model/tool loop. Persistence, model
// access, authorization, process execution and lifecycle belong to its caller.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/jjyr/pot/pkg/composition"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
)

var ErrUncertainEffects = errors.New("agent checkpoint has an operation with unconfirmed effects")

type SteeringInput struct {
	MessageID   string
	References  []core.MessageReference
	Attachments []core.Attachment
	ID          string
	Text        string
}
type Observation struct {
	StartedAtMS   int64                      `json:"started_at_ms,omitempty"`
	RequestPrefix *modelprotocol.Request     `json:"-"` // Local idle cache prefix, not transcript data.
	Kind          string                     `json:"kind"`
	ID            string                     `json:"id,omitempty"`
	Stage         string                     `json:"stage,omitempty"`
	CompactionID  *string                    `json:"compaction_id,omitempty"`
	Usage         *modelprotocol.UsageReport `json:"usage,omitempty"`
	Error         *string                    `json:"error,omitempty"`
	Tokens        uint64                     `json:"tokens,omitempty"`
	Threshold     uint64                     `json:"threshold,omitempty"`
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
	Run(context.Context, string, map[string]composition.Tool, func(composition.Event) error) error
}
type Cleanup func(context.Context, core.ExecutionContext, bool) error
type TokenEstimator func(modelprotocol.Request) (uint64, error)
type PrepareContext func(context.Context, core.ExecutionContext, []modelprotocol.Message) ([]modelprotocol.Message, error)
type SkillProvider func(context.Context, core.ExecutionContext) (SkillCatalog, error)

// Services supplies model responses and persists compaction observations. Tool
// execution is supplied separately through Dispatch; model-only hosts do not
// need to implement the product's RPC service.
type Services interface {
	Model(context.Context, core.ModelRequest) (io.ReadCloser, error)
	Compaction(context.Context, json.RawMessage) error
}

// Agent runs one durable model/tool loop using caller-owned services and state.
// Composer is optional for model-only and ordinary tool use. To enable composed
// tools, set it to a composition.ProcessRunner with an explicit worker command.
type Agent struct {
	CompactDeadline    time.Time // Estimated cache expiry for idle-only work; zero for explicit hosts/window compaction.
	IdlePrefix         *modelprotocol.Request
	CompactSchedule    json.RawMessage
	CompactOnly        bool // An owning lane requested idle maintenance; never accept synthetic user input.
	MessageID          func(context.Context) (string, error)
	Services           Services
	Feedback           Feedback
	Dispatch           Dispatch
	Cleanup            Cleanup
	Tools              []modelprotocol.ToolDefinition
	Composer           Composer
	EstimateTokens     TokenEstimator
	ResolveAttachments func(context.Context, core.ResolvedSpec, []modelprotocol.Message) ([]modelprotocol.Message, error)
	// ResolveReferences supplies request-local quoted content from the owning
	// transcript. Return a copy; canonical checkpoint messages must stay intact.
	ResolveReferences func(context.Context, []modelprotocol.Message) ([]modelprotocol.Message, error)
	PrepareContext    PrepareContext
	Skills            SkillProvider
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
