package control

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func hostRequest(call string) HostRequest {
	return HostRequest{Owner: "assistant", Turn: "turn", Generation: 1, CallID: call, MachineID: "machine", EnrollmentEpoch: "epoch", Tool: "exec_command", Args: json.RawMessage(`{"cmd":"printf hello"}`)}
}
func waitApproval(t *testing.T, p *PermissionStore) Approval {
	t.Helper()
	sub := p.Changes.Subscribe()
	defer sub.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for {
		pending := p.Pending("assistant")
		if len(pending) > 0 {
			return pending[0]
		}
		if _, err := sub.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
func TestExactApprovalScopePolicyAndExpiry(t *testing.T) {
	p, err := OpenPermissions(filepath.Join(t.TempDir(), "permissions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Enroll("machine", "epoch"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		persistent, err := p.Authorize(ctx, hostRequest("1"))
		if persistent {
			t.Error("temporary grant became persistent")
		}
		done <- err
	}()
	a := waitApproval(t, p)
	if err = p.Decide("wrong-owner", a.ID, "allow_once"); err == nil {
		t.Fatal("cross-owner approval")
	}
	if err = p.Decide("assistant", a.ID, "allow_once"); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	go func() { _, err := p.Authorize(ctx, hostRequest("2")); done <- err }()
	a = waitApproval(t, p)
	if a.Request.CallID != "2" {
		t.Fatal("inherited approval")
	}
	p.Expire("assistant", "turn", 1)
	if err = <-done; err == nil {
		t.Fatal("expired approval succeeded")
	}
	if err = p.Configure("machine", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	if persistent, err := p.Authorize(ctx, hostRequest("3")); err != nil || !persistent {
		t.Fatal(persistent, err)
	}
	if err = p.Enroll("machine", "new-epoch"); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Authorize(ctx, hostRequest("4")); err == nil {
		t.Fatal("old enrollment retained authority")
	}
}
func TestSingleOperationDisallowsHostInput(t *testing.T) {
	p, _ := OpenPermissions(filepath.Join(t.TempDir(), "policy.json"))
	p.Enroll("machine", "epoch")
	r := hostRequest("tty")
	r.Args = json.RawMessage(`{"cmd":"bash","tty":true}`)
	if _, err := p.Authorize(context.Background(), r); err == nil {
		t.Fatal("temporary interactive terminal")
	}
	r.Tool = "write_stdin"
	r.Args = json.RawMessage(`{"session_id":"s","chars":"rm something\n"}`)
	if _, err := p.Authorize(context.Background(), r); err == nil {
		t.Fatal("temporary stdin input")
	}
	r.Args = json.RawMessage(`{"session_id":"s","chars":""}`)
	if _, err := p.Authorize(context.Background(), r); err != nil {
		t.Fatal(err)
	}
}

func TestAlwaysAllowPersistsOnlyForEnrolledMachineAndCanBeRevoked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permissions.json")
	p, err := OpenPermissions(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Enroll("machine", "epoch"); err != nil {
		t.Fatal(err)
	}
	if err = p.Enroll("other", "other-epoch"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		persistent, err := p.Authorize(ctx, hostRequest("first"))
		if err == nil && !persistent {
			err = errors.New("saved grant was not persistent")
		}
		done <- err
	}()
	approval := waitApproval(t, p)
	if err = p.Decide("wrong-owner", approval.ID, "allow_always"); err == nil {
		t.Fatal("cross-owner grant accepted")
	}
	if err = p.Decide("assistant", approval.ID, "allow_always"); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = p.Decide("assistant", approval.ID, "allow_always"); err != nil {
		t.Fatal("decision retry", err)
	}
	reopened, err := OpenPermissions(path)
	if err != nil {
		t.Fatal(err)
	}
	next := hostRequest("another-tool")
	next.Owner = "another-assistant"
	next.Task = "new-task"
	next.Tool = "apply_patch"
	next.Args = json.RawMessage(`{"patch":"different"}`)
	if persistent, err := reopened.Authorize(ctx, next); err != nil || !persistent {
		t.Fatal("saved machine grant not shared across assistants/tasks", persistent, err)
	}
	other, _, err := reopened.Policy("other")
	if err != nil || other.ExecutionAccess != "ask" {
		t.Fatal("other machine inherited grant", other, err)
	}
	if err = reopened.Configure("machine", MachinePolicy{ExecutionAccess: "ask"}); err != nil {
		t.Fatal(err)
	}
	next = hostRequest("after-revocation")
	go func() { _, err := reopened.Authorize(ctx, next); done <- err }()
	denied := waitApproval(t, reopened)
	if err = reopened.Decide("assistant", denied.ID, "deny"); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrHostDenied) {
		t.Fatal("denied operation executed", err)
	}
	if err = reopened.Decide("assistant", denied.ID, "allow_always"); err == nil {
		t.Fatal("denied request upgraded on retry")
	}
	if err = reopened.Configure("machine", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	if err = reopened.Enroll("machine", "new-epoch"); err != nil {
		t.Fatal(err)
	}
	reset, _, err := reopened.Policy("machine")
	if err != nil || reset.ExecutionAccess != "ask" {
		t.Fatal("reenrollment retained authority", reset, err)
	}
	if _, err = reopened.Authorize(ctx, hostRequest("old-epoch")); err == nil {
		t.Fatal("old epoch remained authorized")
	}
}

func TestAlwaysAllowWakesOtherCallsButNeverApprovesInstructionChanges(t *testing.T) {
	p, err := OpenPermissions(filepath.Join(t.TempDir(), "permissions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Enroll("machine", "epoch"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 3)
	first := hostRequest("one")
	second := hostRequest("two")
	second.Owner = "second-assistant"
	review := hostRequest("rules")
	review.Tool = "update_agent_instructions"
	go func() { _, err := p.Authorize(ctx, first); done <- err }()
	one := waitApproval(t, p)
	go func() { _, err := p.Authorize(ctx, second); done <- err }()
	go func() { _, err := p.Authorize(ctx, review); done <- err }()
	sub := p.Changes.Subscribe()
	defer sub.Close()
	for len(p.Pending("assistant")) != 2 || len(p.Pending("second-assistant")) != 1 {
		if _, err = sub.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
	var rules Approval
	for _, a := range p.Pending("assistant") {
		if a.Request.Tool == "update_agent_instructions" {
			rules = a
		}
	}
	if err = p.Decide("assistant", rules.ID, "allow_always"); err == nil {
		t.Fatal("instruction review granted broad execution")
	}
	if err = p.Decide("assistant", one.ID, "allow_always"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = <-done; err != nil {
			t.Fatal(err)
		}
	}
	if pending := p.Pending("assistant"); len(pending) != 1 || pending[0].ID != rules.ID {
		t.Fatal("instruction review was consumed", pending)
	}
	if err = p.Decide("assistant", rules.ID, "allow_once"); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestOldPermissionFormatIsRejectedWithoutRewriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "permissions.json")
	original := []byte(`{"version":4,"machines":{},"epochs":{}}`)
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPermissions(path); err == nil {
		t.Fatal("old machine policy accepted")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(original) {
		t.Fatal("old state modified", err)
	}
}

func TestMachineDenyBlocksExecutionButRetainsIndependentInstructionReview(t *testing.T) {
	p, err := OpenPermissions(filepath.Join(t.TempDir(), "permissions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Enroll("machine", "epoch"); err != nil {
		t.Fatal(err)
	}
	if err = p.Configure("machine", MachinePolicy{ExecutionAccess: "deny"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err = p.Authorize(ctx, hostRequest("blocked")); !errors.Is(err, ErrHostDenied) {
		t.Fatal("disabled machine executed", err)
	}
	if len(p.Pending("assistant")) != 0 {
		t.Fatal("disabled machine asked instead of refusing")
	}
	review := hostRequest("review")
	review.Tool = "update_agent_instructions"
	done := make(chan error, 1)
	go func() { _, err := p.Authorize(ctx, review); done <- err }()
	a := waitApproval(t, p)
	if err = p.Decide("assistant", a.ID, "allow_once"); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal("independent instruction review blocked", err)
	}
}

func TestAlwaysAllowWriteFailureLeavesExactRequestPending(t *testing.T) {
	p, err := OpenPermissions(filepath.Join(t.TempDir(), "permissions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Enroll("machine", "epoch"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := p.Authorize(ctx, hostRequest("save-failure")); done <- err }()
	a := waitApproval(t, p)
	p.path = t.TempDir() // A directory cannot be atomically replaced by a policy file.
	if err = p.Decide("assistant", a.ID, "allow_always"); err == nil {
		t.Fatal("failed policy write accepted")
	}
	policy, _, err := p.Policy("machine")
	if err != nil || policy.ExecutionAccess != "ask" {
		t.Fatal("failed save granted execution", policy, err)
	}
	if len(p.Pending("assistant")) != 1 {
		t.Fatal("failed save consumed approval")
	}
	if err = p.Decide("assistant", a.ID, "allow_once"); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPersistentGrantDoesNotReverseDeniedExactCall(t *testing.T) {
	p, err := OpenPermissions(filepath.Join(t.TempDir(), "permissions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Enroll("machine", "epoch"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	denied := hostRequest("denied")
	go func() { _, err := p.Authorize(ctx, denied); done <- err }()
	a := waitApproval(t, p)
	if err = p.Decide("assistant", a.ID, "deny"); err != nil {
		t.Fatal(err)
	}
	if err = <-done; !errors.Is(err, ErrHostDenied) {
		t.Fatal(err)
	}
	go func() { _, err := p.Authorize(ctx, hostRequest("grant")); done <- err }()
	a = waitApproval(t, p)
	if err = p.Decide("assistant", a.ID, "allow_always"); err != nil {
		t.Fatal(err)
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if _, err = p.Authorize(ctx, denied); !errors.Is(err, ErrHostDenied) {
		t.Fatal("persistent grant reversed exact denial", err)
	}
	if persistent, err := p.Authorize(ctx, hostRequest("new-call")); err != nil || !persistent {
		t.Fatal("new call did not use saved authorization", persistent, err)
	}
}
