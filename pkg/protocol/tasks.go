package protocol

import (
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/cronexpr"
	"strings"
	"time"
)

type TaskPhase string

const (
	TaskQueued          TaskPhase = "queued"
	TaskWaiting         TaskPhase = "waiting"
	TaskRunning         TaskPhase = "running"
	TaskWaitingApproval TaskPhase = "waiting_approval"
	TaskComplete        TaskPhase = "complete"
	TaskFailed          TaskPhase = "failed"
	TaskCancelled       TaskPhase = "cancelled"
	TaskInterrupted     TaskPhase = "interrupted"
)

func (p TaskPhase) Scope() string {
	switch p {
	case TaskQueued, TaskWaiting, TaskRunning, TaskWaitingApproval:
		return "active"
	case TaskFailed, TaskInterrupted, TaskComplete, TaskCancelled:
		return "attention"
	}
	return ""
}

type AgentLinkOrigin struct {
	LinkID      string `json:"link_id"`
	NodeID      string `json:"node_id"`
	AssistantID string `json:"assistant_id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
}

type TaskInfo struct {
	AgentLink       *AgentLinkOrigin      `json:"agent_link,omitempty"`
	Address         *AgentRef             `json:"address,omitempty"`
	MachineID       string                `json:"machine_id"`
	RemoteWaiting   bool                  `json:"remote_waiting"`
	ExecutionTarget *core.ExecutionTarget `json:"execution_target"`
	AssistantID     string                `json:"assistant_id"`

	ID            string       `json:"id"`
	Title         string       `json:"title"`
	Phase         TaskPhase    `json:"phase"`
	RunID         string       `json:"run_id"`
	Archived      bool         `json:"archived"`
	ArchiveReason string       `json:"archive_reason,omitempty"`
	Paused        bool         `json:"paused,omitempty"`
	Pending       uint64       `json:"pending"`
	CreatedAtMS   int64        `json:"created_at_ms"`
	StartedAtMS   int64        `json:"started_at_ms"`
	UpdatedAtMS   int64        `json:"updated_at_ms"`
	Routing       *TaskRouting `json:"routing"`
	ExecutorIcon  string       `json:"executor_icon"`
}

func (t TaskInfo) Scope() string {
	if t.Archived {
		return "archived"
	}
	return t.Phase.Scope()
}

type TaskDisposition struct {
	TaskID   string `json:"task_id" required:"true"`
	RunID    string `json:"run_id" required:"true"`
	Archived bool   `json:"archived" required:"true"`
	Reason   string `json:"reason" required:"true"`
}

type TaskDispositions struct {
	Decisions []TaskDisposition `json:"decisions" required:"true"`
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

// TaskWorkspace is creation intent; destination Runtime resolves the actual path.
// An omitted kind selects a direct project when supplied, otherwise OS temporary storage.
type TaskWorkspace struct {
	Kind       string `json:"kind,omitempty"`
	ProjectDir string `json:"project_dir,omitempty"`
	Remote     string `json:"remote,omitempty"`
	Base       string `json:"base,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Commit     string `json:"commit,omitempty"`
}

func (w TaskWorkspace) Mode() string {
	if w.Kind != "" {
		return w.Kind
	}
	if w.ProjectDir != "" {
		return "directory"
	}
	return "temporary"
}
func (w TaskWorkspace) Validate() error {
	mode := w.Mode()
	if mode != "temporary" && mode != "directory" && mode != "worktree" {
		return fmt.Errorf("invalid workspace kind")
	}
	for _, value := range []string{w.ProjectDir, w.Remote, w.Base, w.Branch, w.Commit} {
		if len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return fmt.Errorf("invalid workspace selection")
		}
	}
	if (mode == "temporary") != (w.ProjectDir == "") {
		return fmt.Errorf("workspace requires a project directory except for temporary work")
	}
	if mode != "worktree" && (w.Remote != "" || w.Base != "" || w.Branch != "" || w.Commit != "") {
		return fmt.Errorf("Git selection requires worktree mode")
	}
	if w.Base != "" && w.Commit != "" {
		return fmt.Errorf("choose a base branch or pinned commit")
	}
	return nil
}

type CreateTask struct {
	Target    *AgentRef     `json:"target,omitempty"`
	Workspace TaskWorkspace `json:"workspace"`

	Skills          *[]string        `json:"skills"`
	RequestID       string           `json:"request_id" required:"true"`
	Text            string           `json:"text" required:"true"`
	Profile         *string          `json:"profile"`
	Model           *string          `json:"model"`
	Requirements    TaskRequirements `json:"requirements"`
	SelectionReason *string          `json:"selection_reason,omitempty"`
}

func (t CreateTask) Validate() error {
	if t.Target != nil && (!t.Target.Valid() || (t.Target.Kind != "assistant" && (t.Target.Kind != "node" || t.Target.AgentID != t.Target.NodeID))) {
		return fmt.Errorf("invalid task target")
	}
	if err := t.Workspace.Validate(); err != nil {
		return err
	}
	if t.RequestID == "" || len(t.RequestID) > 128 || strings.ContainsRune(t.RequestID, 0) || strings.TrimSpace(t.Text) == "" || len(t.Text) > 65536 {
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
type TaskOutcome struct {
	Sequence    uint64    `json:"sequence"`
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Phase       TaskPhase `json:"phase"`
	UpdatedAtMS int64     `json:"updated_at_ms"`
}
type TaskOutcomePage struct {
	Items   []TaskOutcome `json:"items"`
	Cursor  uint64        `json:"cursor"`
	HasMore bool          `json:"has_more"`
}
type ScheduleSpec struct {
	MachineID    string           `json:"machine_id"`
	Title        string           `json:"title" required:"true"`
	Goal         string           `json:"goal" required:"true"`
	Timezone     string           `json:"timezone"`
	Kind         string           `json:"kind" required:"true"`
	At           string           `json:"at"`
	EveryMinutes uint64           `json:"every_minutes"`
	Cron         string           `json:"cron"`
	Misfire      string           `json:"misfire"`
	Notify       string           `json:"notify"`
	Enabled      bool             `json:"enabled"`
	Profile      *string          `json:"profile"`
	Model        *string          `json:"model"`
	Requirements TaskRequirements `json:"requirements"`
}

func (s *ScheduleSpec) UnmarshalJSON(data []byte) error {
	type plain ScheduleSpec
	value := plain{Misfire: "coalesce", Notify: "all", Enabled: true}
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
	// An omitted zone is intent, resolved by the owning Control before save.
	// UTC only validates the wall-clock syntax here; it is not a default zone.
	zone := time.UTC
	if s.Timezone != "" {
		var err error
		zone, err = cronexpr.Location(s.Timezone)
		if err != nil {
			return err
		}
	}
	if s.Misfire != "coalesce" && s.Misfire != "skip" {
		return fmt.Errorf("invalid misfire policy")
	}
	switch s.Kind {
	case "once":
		instant, err := time.ParseInLocation("2006-01-02 15:04", s.At, zone)
		if err != nil {
			return err
		}
		if instant.Format("2006-01-02 15:04") != s.At {
			return fmt.Errorf("one-off local time does not exist")
		}
	case "interval":
		if s.EveryMinutes == 0 || s.EveryMinutes > 525600 {
			return fmt.Errorf("invalid interval")
		}
	case "cron":
		if _, err := cronexpr.Parse(s.Cron); err != nil {
			return err
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
