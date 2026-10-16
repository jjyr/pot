package schedule

import (
	"context"
	"database/sql"
	"github.com/jjyr/pot/internal/protocol"
	"github.com/jjyr/pot/internal/tasks"
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
	Tasks   *tasks.Service
	Ready   func(context.Context, string) (bool, error)
	Results tasks.ResultReader
}
type Service struct {
	db      *sql.DB
	options Options
}
type Notification struct {
	ID          string
	AssistantID string
	ScheduleID  string
	RunID       string
	Text        string
}
type ResultFile struct {
	Summary     string   `json:"summary" required:"true"`
	Fingerprint string   `json:"fingerprint" required:"true"`
	Evidence    []string `json:"evidence" required:"true"`
	Artifacts   []string `json:"artifacts,omitempty"`
}
type queued struct {
	Owner  string
	PlanID string
	Run    protocol.ScheduleRun
}
type resolved struct {
	Request   protocol.CreateTask
	Selection tasks.Selection
}
type reply struct {
	TaskID string `json:"task_id"`
}
