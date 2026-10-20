package schedule

import (
	"context"
	"database/sql"
	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/protocol"
)

type Request struct {
	RequestID  string                 `json:"request_id"`
	Action     string                 `json:"action"`
	ScheduleID string                 `json:"schedule_id"`
	Spec       *protocol.ScheduleSpec `json:"spec"`
}
type Query struct {
	Offset     uint64
	Selected   string
	RunsOffset uint64
	Scope      string
	Limit      uint64
}
type Plan struct {
	Info       protocol.ScheduleInfo `json:"info"`
	Generation uint64                `json:"generation"`
}
type Options struct {
	// SystemTimezone resolves the owning Control, never the destination Runtime.
	SystemTimezone func() (string, error)
	Tasks          *tasks.Service
	Ready          func(context.Context, string) (bool, error)
	Results        tasks.ResultReader
}
type Service struct {
	db      *sql.DB
	options Options
}
type ResultFile struct {
	Summary     string   `json:"summary" required:"true"`
	Fingerprint string   `json:"fingerprint" required:"true"`
	Evidence    []string `json:"evidence" required:"true"`
	Artifacts   []string `json:"artifacts,omitempty"`
}
type resolved struct {
	Request   protocol.CreateTask
	Selection tasks.Selection
}
type reply struct {
	TaskID string `json:"task_id"`
}
