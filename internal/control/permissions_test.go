package control

import (
	"context"
	"encoding/json"
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
	if err = p.Configure("machine", MachinePolicy{"host", "allow"}); err != nil {
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
