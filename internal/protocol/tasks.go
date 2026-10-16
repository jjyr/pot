package protocol

import (
	"fmt"
	"github.com/jjyr/pot/internal/core"
	"time"
)

type TaskPhase string

const (
	TaskQueued      TaskPhase = "queued"
	TaskWaiting     TaskPhase = "waiting"
	TaskRunning     TaskPhase = "running"
	TaskComplete    TaskPhase = "complete"
	TaskFailed      TaskPhase = "failed"
	TaskCancelled   TaskPhase = "cancelled"
	TaskInterrupted TaskPhase = "interrupted"
)

func (p TaskPhase) Scope() string {
	switch p {
	case TaskQueued, TaskWaiting, TaskRunning:
		return "active"
	case TaskFailed, TaskInterrupted:
		return "attention"
	case TaskComplete, TaskCancelled:
		return "archived"
	}
	return ""
}

type TaskInfo struct {
	RemoteWaiting   bool                  `json:"remote_waiting"`
	ExecutionTarget *core.ExecutionTarget `json:"execution_target"`
	AssistantID     string                `json:"assistant_id"`
	SandboxID       string                `json:"sandbox_id"`
	ID              string                `json:"id"`
	Title           string                `json:"title"`
	Phase           TaskPhase             `json:"phase"`
	Pending         uint64                `json:"pending"`
	CreatedAtMS     int64                 `json:"created_at_ms"`
	UpdatedAtMS     int64                 `json:"updated_at_ms"`
	Resource        string                `json:"resource"`
	Routing         *TaskRouting          `json:"routing"`
	ExecutorIcon    string                `json:"executor_icon"`
}
type TaskRouting struct {
	Profile         string  `json:"profile"`
	Revision        uint64  `json:"revision"`
	Provider        string  `json:"provider"`
	Reason          string  `json:"reason"`
	SelectionReason *string `json:"selection_reason,omitempty"`
}
type TaskRequirements struct {
	Tools        bool `json:"tools"`
	Desktop      bool `json:"desktop"`
	ExactBudget  bool `json:"exact_budget"`
	LiveSteering bool `json:"live_steering"`
}
type CreateTask struct {
	MachineID       string           `json:"machine_id"`
	Skills          *[]string        `json:"skills"`
	RequestID       string           `json:"request_id" required:"true"`
	Text            string           `json:"text" required:"true"`
	Resource        string           `json:"resource"`
	Profile         *string          `json:"profile"`
	Model           *string          `json:"model"`
	Requirements    TaskRequirements `json:"requirements"`
	SelectionReason *string          `json:"selection_reason,omitempty"`
}

func (t CreateTask) Validate() error {
	if !core.Identifier(t.RequestID) || t.Text == "" || len(t.Text) > 65536 || t.MachineID != "" && !core.Identifier(t.MachineID) {
		return fmt.Errorf("invalid task request")
	}
	return nil
}

type TaskInput struct {
	RequestID string `json:"request_id" required:"true"`
	Text      string `json:"text" required:"true"`
}
type TaskQuery struct {
	ExecutionTarget string     `json:"execution_target"`
	Scope           string     `json:"scope"`
	Phase           *TaskPhase `json:"phase"`
	Q               string     `json:"q"`
	Offset          uint64     `json:"offset"`
	Limit           uint64     `json:"limit"`
	Selected        string     `json:"selected"`
}

func (q *TaskQuery) UnmarshalJSON(data []byte) error {
	type plain TaskQuery
	value := plain{Scope: "active", Limit: 10}
	if err := core.DecodeJSON(data, &value); err != nil {
		return err
	}
	*q = TaskQuery(value)
	return nil
}

type TaskCounts struct {
	Active    uint64 `json:"active"`
	Attention uint64 `json:"attention"`
	Archived  uint64 `json:"archived"`
}
type TaskPage struct {
	Items      []TaskInfo `json:"items"`
	Total      uint64     `json:"total"`
	Offset     uint64     `json:"offset"`
	NextOffset *uint64    `json:"next_offset"`
	Counts     TaskCounts `json:"counts"`
	Selected   *TaskInfo  `json:"selected"`
}
type ScheduleSpec struct {
	MachineID    string           `json:"machine_id"`
	Title        string           `json:"title" required:"true"`
	Goal         string           `json:"goal" required:"true"`
	Timezone     string           `json:"timezone"`
	Kind         string           `json:"kind" required:"true"`
	At           string           `json:"at"`
	EveryMinutes uint64           `json:"every_minutes"`
	Time         string           `json:"time"`
	Weekdays     []uint32         `json:"weekdays"`
	Misfire      string           `json:"misfire"`
	Notify       string           `json:"notify"`
	Enabled      bool             `json:"enabled"`
	Profile      *string          `json:"profile"`
	Model        *string          `json:"model"`
	Requirements TaskRequirements `json:"requirements"`
}

func (s *ScheduleSpec) UnmarshalJSON(data []byte) error {
	type plain ScheduleSpec
	value := plain{Timezone: "Asia/Shanghai", Misfire: "coalesce", Notify: "changes", Enabled: true}
	if err := core.DecodeJSON(data, &value); err != nil {
		return err
	}
	*s = ScheduleSpec(value)
	return nil
}
func (s ScheduleSpec) Validate() error {
	if s.Title == "" || s.Goal == "" || s.MachineID != "" && !core.Identifier(s.MachineID) {
		return fmt.Errorf("invalid schedule")
	}
	if _, err := time.LoadLocation(s.Timezone); err != nil {
		return err
	}
	if s.Misfire != "coalesce" && s.Misfire != "skip" {
		return fmt.Errorf("invalid misfire policy")
	}
	switch s.Kind {
	case "once":
		if _, err := time.Parse(time.RFC3339, s.At); err != nil {
			return err
		}
	case "interval":
		if s.EveryMinutes == 0 || s.EveryMinutes > core.MaxSafeInteger {
			return fmt.Errorf("invalid interval")
		}
	case "daily", "weekly":
		if _, err := time.Parse("15:04", s.Time); err != nil {
			return err
		}
		if s.Kind == "weekly" && len(s.Weekdays) == 0 {
			return fmt.Errorf("weekly schedule requires weekdays")
		}
		for _, day := range s.Weekdays {
			if day > 6 {
				return fmt.Errorf("invalid weekday")
			}
		}
	default:
		return fmt.Errorf("unknown schedule kind")
	}
	return nil
}

type ScheduleInfo struct {
	ID          string       `json:"id"`
	Spec        ScheduleSpec `json:"spec"`
	NextAtMS    *int64       `json:"next_at_ms"`
	NextLabel   string       `json:"next_label"`
	Deleted     bool         `json:"deleted"`
	LastError   string       `json:"last_error"`
	Baseline    *string      `json:"baseline"`
	CreatedAtMS int64        `json:"created_at_ms"`
}
type ScheduleRun struct {
	ID             string  `json:"id"`
	ScheduledForMS int64   `json:"scheduled_for_ms"`
	ScheduledLabel string  `json:"scheduled_label"`
	Generation     uint64  `json:"generation"`
	TaskID         *string `json:"task_id"`
	State          string  `json:"state"`
	Summary        string  `json:"summary"`
	Changed        *bool   `json:"changed"`
	FinishedAtMS   *int64  `json:"finished_at_ms"`
	Notify         string  `json:"notify"`
}
type SchedulePage struct {
	Items          []ScheduleInfo `json:"items"`
	Total          uint64         `json:"total"`
	Offset         uint64         `json:"offset"`
	NextOffset     *uint64        `json:"next_offset"`
	Selected       *ScheduleInfo  `json:"selected"`
	Runs           []ScheduleRun  `json:"runs"`
	RunsNextOffset *uint64        `json:"runs_next_offset"`
}
