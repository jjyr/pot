package core

import (
	"fmt"
	"strings"
)

type ExecutionTarget struct {
	RuntimeID string `json:"runtime_id" required:"true"`
}

func RuntimeTarget(runtimeID string) ExecutionTarget { return ExecutionTarget{RuntimeID: runtimeID} }
func Identifier(value string) bool {
	if value == "" || len(value) > 128 || value == "." || value == ".." {
		return false
	}
	for _, c := range []byte(value) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.", rune(c))) {
			return false
		}
	}
	return true
}
func (t ExecutionTarget) Validate() error {
	if !Identifier(t.RuntimeID) {
		return fmt.Errorf("invalid runtime identity")
	}
	return nil
}
func (t ExecutionTarget) IsLocalTo(runtimeID string) bool { return t.RuntimeID == runtimeID }
func (t *ExecutionTarget) UnmarshalJSON(data []byte) error {
	type plain ExecutionTarget
	var value plain
	if err := DecodeJSON(data, &value); err != nil {
		return err
	}
	*t = ExecutionTarget(value)
	return t.Validate()
}

type ObjectRef struct {
	RuntimeID string `json:"runtime_id" required:"true"`
	ObjectID  string `json:"object_id" required:"true"`
}
type Assignment struct {
	SchemaVersion         uint32           `json:"schema_version" required:"true"`
	Owner                 ObjectRef        `json:"owner" required:"true"`
	DispatchOriginRuntime string           `json:"dispatch_origin_runtime" required:"true"`
	AgentRuntimeID        string           `json:"agent_runtime_id" required:"true"`
	ExecutionTarget       *ExecutionTarget `json:"execution_target"`
	WorkspaceScope        string           `json:"workspace_scope" required:"true"`
}

func AssignmentOnTarget(runtimeID, taskID string, target ExecutionTarget) Assignment {
	return Assignment{4, ObjectRef{runtimeID, taskID}, runtimeID, target.RuntimeID, &target, "tasks/" + taskID}
}
func (a Assignment) ValidateBinding(runtimeID, taskID string, target ExecutionTarget) error {
	if err := target.Validate(); err != nil {
		return err
	}
	expected := AssignmentOnTarget(runtimeID, taskID, target)
	if a.SchemaVersion != 4 || a.Owner != expected.Owner || a.DispatchOriginRuntime != runtimeID || a.AgentRuntimeID != target.RuntimeID || a.ExecutionTarget == nil || *a.ExecutionTarget != target || a.WorkspaceScope != expected.WorkspaceScope {
		return fmt.Errorf("unsupported or conflicting task assignment")
	}
	return nil
}
func (a *Assignment) UnmarshalJSON(data []byte) error {
	type plain Assignment
	var value plain
	if err := DecodeJSON(data, &value); err != nil {
		return err
	}
	if value.SchemaVersion != 4 {
		return fmt.Errorf("unsupported assignment schema")
	}
	*a = Assignment(value)
	return nil
}
