package protocol

import (
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
)

// ToolCall is the durable UI projection, separate from model context and receipts.
// Output and result are deliberately independent: completion never erases a stream.
type ToolCall struct {
	ExecutionMode string                `json:"execution_mode,omitempty"`
	TurnID        string                `json:"turn_id"`
	ID            string                `json:"id"`
	ParentID      string                `json:"parent_id,omitempty"`
	Name          string                `json:"name"`
	Args          json.RawMessage       `json:"args"`
	Phase         string                `json:"phase"`
	Target        *core.ExecutionTarget `json:"target,omitempty"`
	Workdir       string                `json:"workdir,omitempty"`
	StartedAtMS   int64                 `json:"started_at_ms"`
	WaitingAtMS   int64                 `json:"waiting_at_ms,omitempty"`
	RunningAtMS   int64                 `json:"running_at_ms,omitempty"`
	FinishedAtMS  int64                 `json:"finished_at_ms,omitempty"`
	Output        []ToolOutput          `json:"output,omitempty"`
	Result        json.RawMessage       `json:"result,omitempty"`
	Diagnostic    string                `json:"diagnostic,omitempty"`
}
type ToolOutput struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

func (t ToolCall) Validate() error {
	if t.ID == "" || t.Name == "" || !json.Valid(t.Args) || t.ParentID == t.ID || t.TurnID != "" && !core.Identifier(t.TurnID) {
		return fmt.Errorf("invalid tool call")
	}
	for _, at := range []int64{t.StartedAtMS, t.WaitingAtMS, t.RunningAtMS, t.FinishedAtMS} {
		if at < 0 || uint64(at) > core.MaxSafeInteger {
			return fmt.Errorf("invalid tool timestamp")
		}
	}
	if len(t.Result) > 0 && !json.Valid(t.Result) {
		return fmt.Errorf("invalid tool result")
	}
	switch t.Phase {
	case "preparing", "waiting_approval", "running", "completed", "failed", "denied", "stopped", "unknown":
	default:
		return fmt.Errorf("invalid tool phase")
	}
	if t.ExecutionMode != "" && t.ExecutionMode != "host" && t.ExecutionMode != "sandbox" && t.ExecutionMode != "session" {
		return fmt.Errorf("invalid execution mode")
	}
	if t.Target != nil {
		return t.Target.Validate()
	}
	return nil
}
func (t *ToolCall) Apply(u core.Update) {
	switch u.Type {
	case "tool_started":
		*t = ToolCall{ID: u.ID, ParentID: u.ParentID, Name: u.Name, Args: u.Args, Phase: "preparing", StartedAtMS: u.AtMS}
		if len(t.Args) == 0 {
			t.Args = json.RawMessage(`{}`)
		}
	case "tool_state":
		t.Phase = u.Phase
		if u.ExecutionMode != "" {
			t.ExecutionMode = u.ExecutionMode
		}
		if u.Target != nil {
			t.Target = u.Target
		}
		if u.Workdir != "" {
			t.Workdir = u.Workdir
		}
		if u.Phase == "waiting_approval" && t.WaitingAtMS == 0 {
			t.WaitingAtMS = u.AtMS
		}
		if u.Phase == "running" && t.RunningAtMS == 0 {
			t.RunningAtMS = u.AtMS
		}
	case "tool_output":
		if t.Phase == "preparing" {
			t.Phase = "running"
			t.RunningAtMS = u.AtMS
		}
		if len(t.Output) > 0 && t.Output[len(t.Output)-1].Stream == u.Stream {
			t.Output[len(t.Output)-1].Text += u.Text
		} else {
			t.Output = append(t.Output, ToolOutput{u.Stream, u.Text})
		}
	case "tool_finished":
		t.Result = u.Result
		t.FinishedAtMS = u.AtMS
		if t.Phase != "denied" {
			t.Phase = "completed"
			if u.Failed {
				t.Phase = "failed"
			}
		}
	case "turn_finished":
		t.Phase = "unknown"
		t.FinishedAtMS = u.AtMS
		if u.Outcome != nil {
			t.Diagnostic = u.Outcome.Error
			if u.Outcome.Kind == "cancelled" {
				t.Phase = "stopped"
			}
		}
	}
}
