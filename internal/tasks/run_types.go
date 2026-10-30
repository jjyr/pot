// Local task execution retains immutable launches, checkpoints and recovery evidence.
package tasks

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/rollout"
	"strings"
)

// Launch is the immutable execution snapshot. The persisted control_id/runtime_id
// names retain original identity and digest evidence; new executions bind both to
// the same local node. They do not configure separate deployment roles.
type Launch struct {
	Workspace       protocol.TaskWorkspace `json:"workspace"`
	ControlID       string                 `json:"control_id" required:"true"`
	AssistantID     string                 `json:"assistant_id" required:"true"`
	RuntimeID       string                 `json:"runtime_id" required:"true"`
	TaskID          string                 `json:"task_id" required:"true"`
	RunID           string                 `json:"run_id" required:"true"`
	Generation      uint64                 `json:"generation" required:"true"`
	ExecutionTarget core.ExecutionTarget   `json:"execution_target" required:"true"`
	Config          core.ResolvedSpec      `json:"config" required:"true"`
	Plugins         []string               `json:"plugins" required:"true"`
	Skills          *[]string              `json:"skills"`
	Text            string                 `json:"text" required:"true"`
	InputID         string                 `json:"input_id,omitempty"`
}

func (l Launch) Validate(runtimeID string) error {
	if err := l.Workspace.Validate(); err != nil {
		return err
	}
	for _, id := range []string{l.ControlID, l.AssistantID, l.RuntimeID, l.TaskID, l.RunID} {
		if !core.Identifier(id) {
			return fmt.Errorf("invalid launch identity")
		}
	}
	if l.RuntimeID != runtimeID || l.ExecutionTarget.RuntimeID != runtimeID {
		return fmt.Errorf("task Agent and target must belong to destination Runtime")
	}
	if err := l.ExecutionTarget.Validate(); err != nil {
		return err
	}
	if l.Generation == 0 || l.Generation > core.MaxSafeInteger || strings.TrimSpace(l.Text) == "" || len(l.Text) > protocol.MaxScheduledTaskTextBytes {
		return fmt.Errorf("invalid task generation/input")
	}
	if len(l.InputID) > 128 || strings.ContainsRune(l.InputID, 0) {
		return fmt.Errorf("invalid continuation input")
	}
	if err := l.Config.Validate(); err != nil {
		return err
	}
	if l.Config.GatewayProtocol == "unknown" || len(l.Config.Params.Agent.Plugins) != 0 {
		return fmt.Errorf("task launch requires supported protocol and selected plugin names, without bundles")
	}
	names := [][]string{l.Plugins}
	if l.Skills != nil {
		names = append(names, *l.Skills)
	}
	for _, list := range names {
		if len(list) > 128 {
			return fmt.Errorf("too many resources")
		}
		seen := map[string]bool{}
		for _, name := range list {
			if len(name) > 64 || !core.Identifier(name) || strings.ContainsAny(name, "._") || seen[name] {
				return fmt.Errorf("invalid or duplicate resource name")
			}
			seen[name] = true
		}
	}
	data, err := l.CanonicalBytes()
	if err != nil {
		return err
	}
	// JSON can use six bytes per input byte (for example, a NUL as \u0000).
	// Preserve the existing metadata budget alongside a fully escaped schedule input.
	if len(data) > 6*protocol.MaxScheduledTaskTextBytes+768*1024 {
		return fmt.Errorf("task launch exceeds transport limit")
	}
	return nil
}
func digest(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
func (l Launch) CanonicalBytes() ([]byte, error) { return core.CanonicalJSON(l) }
func (l Launch) Digest() (string, error)         { return core.HashJSON(l) }
func (l Launch) WorkspaceScope() (string, error) {
	data, err := core.CanonicalJSON([]any{l.ControlID, l.TaskID, l.ExecutionTarget})
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return base64.RawURLEncoding.EncodeToString(hash[:]), nil
}

type Phase string

const (
	Accepted         Phase = "accepted"
	Running          Phase = "running"
	Waiting          Phase = "waiting"
	Complete         Phase = "complete"
	Failed           Phase = "failed"
	Cancelled        Phase = "cancelled"
	RecoveryRequired Phase = "recovery_required"
	Interrupted      Phase = "interrupted"
)

func (p Phase) Ended() bool {
	return p == Complete || p == Failed || p == Cancelled || p == Interrupted
}

type Terminal struct {
	Outcome core.Outcome `json:"outcome"`
	Text    string       `json:"text"`
}

func (t Terminal) phase() (Phase, error) {
	switch t.Outcome.Kind {
	case "success":
		return Complete, nil
	case "failed":
		return Failed, nil
	case "cancelled":
		return Cancelled, nil
	default:
		return "", fmt.Errorf("unknown Task outcome requires reconciliation")
	}
}

type Run struct {
	SchemaVersion    uint32          `json:"schema_version"`
	Launch           Launch          `json:"launch"`
	LaunchDigest     string          `json:"launch_digest"`
	BootID           string          `json:"boot_id"`
	Phase            Phase           `json:"phase"`
	StartedAtMS      int64           `json:"started_at_ms"`
	FinishedAtMS     int64           `json:"finished_at_ms"`
	Result           *Terminal       `json:"result"`
	Context          json.RawMessage `json:"context,omitempty"`
	RolloutRef       *rollout.Ref    `json:"rollout_ref,omitempty"`
	EventSequence    uint64          `json:"event_sequence"`
	Sequence         uint64          `json:"sequence"`
	CleanupConfirmed bool            `json:"cleanup_confirmed"`
	WaitDetail       string          `json:"wait_detail"`
	StopRequested    string          `json:"stop_requested"`
	Finalizing       bool            `json:"finalizing"`
}
