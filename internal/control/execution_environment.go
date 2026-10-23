package control

import (
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
)

// This snapshot informs model planning, never grants execution authority. The owning
// node enforces its local machine policy for every execution.
func executionEnvironment(policy MachinePolicy, target core.ExecutionTarget, workspace string) core.ContextSection {
	data, _ := json.Marshal(map[string]any{
		"runtime_id":                target.RuntimeID,
		"machine_policy":            map[string]string{"ask": "protected", "allow": "trusted", "deny": "blocked"}[policy.ExecutionAccess],
		"policy_revision":           policy.Revision,
		"workspace":                 workspace,
		"protected_filesystem":      "Workspace is writable; system/toolchain files and plugin bundles are read-only. Unrelated user files and Runtime authority are restricted.",
		"protected_temporary_files": "Use $TMPDIR for private temporary scripts/files. HOME and caches are private too. They last for this process/session, not across independent commands; use workspace files for shared state.",
		"network":                   "Unrestricted, including localhost TCP; protected execution restricts Unix sockets independently.",
		"default_execution":         "Protected uses SRT unless an exact host command grant matches. Trusted uses host privileges unless explicitly denied. Blocked rejects new execution. Runtime rechecks authority for each operation; context is a snapshot, not authorization.",
	})
	return core.ContextSection{Key: "execution-environment", Text: string(data)}
}

func (s *Service) addExecutionEnvironment(execution *core.ExecutionContext, target core.ExecutionTarget) error {
	policy, _, err := s.Permissions.Policy(target.RuntimeID)
	if err != nil {
		return err
	}
	if execution.Role == core.Worker {
		execution.ContextSections = append(execution.ContextSections, core.ContextSection{Key: "environment", Text: "cwd: " + execution.Workspace})
	} else {
		execution.ContextSections = append(execution.ContextSections, executionEnvironment(policy, target, execution.Workspace))
	}
	return nil
}
