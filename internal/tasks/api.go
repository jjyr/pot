// Package tasks owns the durable Task catalogue for all Agent Link targets.
// The receiving Pot adapter additionally stores its local execution state here.
package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"sync"
)

type Selection struct {
	Config  core.ResolvedSpec
	Plugins []string
}
type ResolveModel func(context.Context, string, protocol.CreateTask) (Selection, error)
type Options struct {
	OwnerAvailable       func(context.Context, string) error
	NodeID               string
	Executor             LocalExecutor
	ResolveModel         ResolveModel
	AdmitModel           func(context.Context, *sql.Tx, core.ResolvedSpec) (bool, error)
	AwaitingApproval     func(assistant, task, run string, generation uint64) bool
	ValidateRequirements func(context.Context, protocol.TaskRequirements) error
	MaxTasks             int
	PublishHook          hooks.Publish
	OutcomesChanged      func()
	PrepareOutcome       func(context.Context, Record, protocol.TaskResultEvent) (OutcomeCommit, error)
}

// OutcomeCommit applies domain result policy in the Task's terminal transaction.
// Preparation may read files; the returned commit must only use the supplied tx.
type OutcomeCommit func(context.Context, *sql.Tx, *protocol.TaskResultEvent) error
type Service struct {
	db            *sql.DB
	options       Options
	changes       core.Changes
	outcomeMu     sync.Mutex
	outcomeCursor uint64
}
type Input struct {
	RequestID string `json:"request_id"`
	Text      string `json:"text"`
	Kind      string `json:"kind"`
	Sender    string `json:"sender"`
	State     string `json:"state"`
	Sequence  uint64 `json:"sequence"`
}

// Execution binds a caller-owned Task to the original Agent Link execution.
// Local Pot execution details remain private to the receiving adapter.
type Execution struct {
	UnresolvedRequest json.RawMessage       `json:"unresolved_request,omitempty"`
	ObservedAtMS      int64                 `json:"observed_at_ms"`
	Request           agentlink.TaskRequest `json:"request"`
	Receipt           agentlink.Task        `json:"receipt"`
	Notified          bool                  `json:"notified"`
	StopRequested     bool                  `json:"stop_requested"`
}

type Record struct {
	NodeID        string                    `json:"node_id,omitempty"`
	Execution     *Execution                `json:"execution,omitempty"`
	SchemaVersion uint32                    `json:"schema_version"`
	AssistantID   string                    `json:"assistant_id"`
	Info          protocol.TaskInfo         `json:"info"`
	Request       protocol.CreateTask       `json:"request"`
	Config        *core.ResolvedSpec        `json:"config,omitempty"`
	Plugins       []string                  `json:"plugins"`
	RunID         string                    `json:"run_id"`
	PreviousRunID string                    `json:"previous_run_id,omitempty"`
	RunText       string                    `json:"run_text,omitempty"`
	RunInputID    string                    `json:"run_input_id,omitempty"`
	Generation    uint64                    `json:"generation"`
	Launch        *Launch                   `json:"launch"`
	StopMode      string                    `json:"stop_mode"`
	Finalizing    bool                      `json:"finalizing"`
	Error         string                    `json:"error"`
	Result        *Terminal                 `json:"result"`
	Revision      uint64                    `json:"revision"`
	Origin        protocol.TaskOrigin       `json:"origin"`
	Outcome       *protocol.TaskResultEvent `json:"outcome,omitempty"`
}
type WaitTarget struct {
	TaskID string
	After  uint64
}
type WaitItem struct {
	Info   protocol.TaskInfo
	Cursor uint64
	Error  string
}
type Notification struct {
	ID          string
	AssistantID string
	protocol.TaskResultEvent
}

// LocalExecutor runs work inside this node. It is not a remote transport.
type LocalExecutor interface {
	Launch(context.Context, Launch) (Run, error)
	Inspect(context.Context, string, string, string) (*Run, error)
	Stop(context.Context, string, string, string, string) error
	Resume(context.Context, string, string, string) error
}
type Enqueue interface {
	EnqueueTx(context.Context, *sql.Tx, string, protocol.CreateTask, Selection, protocol.TaskOrigin, int64) (Record, error)
}
type ResultReader interface {
	ReadResult(context.Context, core.ExecutionTarget, string, string, int64) (json.RawMessage, error)
}
