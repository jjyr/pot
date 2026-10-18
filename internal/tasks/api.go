// Package tasks owns durable Control-side Task coordination. Destination Runner
// ports own execution; observing remote work never acquires a local Agent slot.
package tasks

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/hooks"
	"github.com/jjyr/pot/internal/runner"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

var ErrResultPending = errors.New("original target result transport unavailable")

type Selection struct {
	MachineID string
	Config    core.ResolvedSpec
	Plugins   []string
}
type ResolveModel func(context.Context, string, protocol.CreateTask) (Selection, error)
type ResolveTarget func(context.Context, string) (core.ExecutionTarget, error)
type Options struct {
	ControlID        string
	RuntimeID        string
	Port             runner.Port
	ResolveModel     ResolveModel
	ResolveMachine   func(context.Context, string) (string, error)
	ResolveTarget    ResolveTarget
	AdmitModel       func(context.Context, *sql.Tx, core.ResolvedSpec) (bool, error)
	AwaitingApproval func(assistant, task, run string, generation uint64) bool
	ValidateTarget   func(context.Context, core.ExecutionTarget, protocol.TaskRequirements) error
	MaxTasks         int
	PublishHook      hooks.Publish
}
type Service struct {
	db      *sql.DB
	options Options
	changes core.Changes
}
type Input struct {
	RequestID string `json:"request_id"`
	Text      string `json:"text"`
	Kind      string `json:"kind"`
	Sender    string `json:"sender"`
	State     string `json:"state"`
	Sequence  uint64 `json:"sequence"`
}
type Record struct {
	SchemaVersion uint32              `json:"schema_version"`
	AssistantID   string              `json:"assistant_id"`
	Info          protocol.TaskInfo   `json:"info"`
	Request       protocol.CreateTask `json:"request"`
	Config        core.ResolvedSpec   `json:"config"`
	Plugins       []string            `json:"plugins"`
	RunID         string              `json:"run_id"`
	Generation    uint64              `json:"generation"`
	Launch        *runner.Launch      `json:"launch"`
	LaunchSent    bool                `json:"launch_sent"`
	StopMode      string              `json:"stop_mode"`
	Finalizing    bool                `json:"finalizing"`
	Error         string              `json:"error"`
	Result        *runner.Terminal    `json:"result"`
	Revision      uint64              `json:"revision"`
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
	TaskID      string
	Phase       protocol.TaskPhase
	Text        string
}
type Resumer interface {
	Resume(context.Context, string, string, string) error
}
type Enqueue interface {
	EnqueueTx(context.Context, *sql.Tx, string, protocol.CreateTask, Selection, int64) (Record, error)
}
type ResultReader interface {
	ReadResult(context.Context, core.ExecutionTarget, string, string, int64) (json.RawMessage, error)
}
