package control

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/internal/testutil"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func hostRequest(call string) HostRequest {
	return HostRequest{Owner: "assistant", Turn: "turn", Generation: 1, CallID: call, MachineID: "machine", EnrollmentEpoch: "epoch", Tool: "exec_command", Args: json.RawMessage(`{"cmd":"npm test","request_unsandboxed":true,"justification":"Access required by the execution contract test"}`), Host: true, Command: &CommandContext{Command: "npm test", Directory: "/workspace", Shell: "/bin/bash"}}
}
func permissionFixture(t *testing.T) *PermissionStore {
	t.Helper()
	p, err := OpenPermissions(filepath.Join(t.TempDir(), "permissions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Enroll("machine", "epoch"); err != nil {
		t.Fatal(err)
	}
	return p
}
func waitApproval(t *testing.T, p *PermissionStore) Approval {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	sub := p.Changes.Subscribe()
	defer sub.Close()
	for {
		list := p.Pending("assistant")
		if len(list) > 0 {
			return list[0]
		}
		if _, err := sub.Next(ctx); err != nil {
			t.Fatal(err)
		}
	}
}
func decideRequest(t *testing.T, p *PermissionStore, r HostRequest, action string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 3*time.Second))
	defer cancel()
	done := make(chan struct {
		mode string
		err  error
	}, 1)
	go func() {
		m, e := p.Authorize(ctx, r)
		done <- struct {
			mode string
			err  error
		}{m, e}
	}()
	a := waitApproval(t, p)
	if err := p.Decide("wrong-owner", a.ID, action); err == nil {
		t.Fatal("cross-owner decision accepted")
	}
	if err := p.Decide(r.Owner, a.ID, action); err != nil {
		t.Fatal(err)
	}
	result := <-done
	if action == "deny" {
		if !errors.Is(result.err, ErrHostDenied) {
			t.Fatal(result.err)
		}
	} else if result.err != nil {
		t.Fatal(result.err)
	}
	return result.mode
}
func TestDefaultSandboxAndExplicitMachineTrust(t *testing.T) {
	t.Parallel()
	p := permissionFixture(t)
	r := hostRequest("sandbox")
	r.Host = false
	if mode, err := p.Authorize(context.Background(), r); err != nil || mode != "sandbox" {
		t.Fatal(mode, err)
	}
	if err := p.Configure("machine", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	if mode, err := p.Authorize(context.Background(), r); err != nil || mode != "host" {
		t.Fatal(mode, err)
	}
	if err := p.Enroll("other", "other-epoch"); err != nil {
		t.Fatal(err)
	}
	r.MachineID = "other"
	r.EnrollmentEpoch = "other-epoch"
	if mode, err := p.Authorize(context.Background(), r); err != nil || mode != "sandbox" {
		t.Fatal("trust crossed machines", mode, err)
	}
}
func TestPermanentCommandIsExactAndEditable(t *testing.T) {
	t.Parallel()
	p := permissionFixture(t)
	r := hostRequest("approve")
	if mode := decideRequest(t, p, r, "allow_always"); mode != "host" {
		t.Fatal(mode)
	}
	policy, _, _ := p.Policy("machine")
	if policy.ExecutionAccess != "ask" {
		t.Fatal("command approval trusted machine")
	}
	reopened, err := OpenPermissions(p.path)
	if err != nil {
		t.Fatal(err)
	}
	r.CallID = "again"
	r.Host = false
	r.Owner = "other"
	if mode, err := reopened.Authorize(context.Background(), r); err != nil || mode != "host" {
		t.Fatal(mode, err)
	}
	for _, change := range []func(*HostRequest){func(r *HostRequest) { r.Command.Command = "npm install" }, func(r *HostRequest) { r.Command.Command = "npm test && curl bad" }, func(r *HostRequest) { r.Command.Directory = "/another" }, func(r *HostRequest) { r.Command.Shell = "/bin/zsh" }, func(r *HostRequest) { r.Command.Login = true }, func(r *HostRequest) { r.Command.Snapshot = true }, func(r *HostRequest) { r.Command.Interactive = true }, func(r *HostRequest) { r.Command.Credentials = json.RawMessage(`{"GH_TOKEN":{"service":"github"}}`) }} {
		different := r
		copy := *r.Command
		different.Command = &copy
		change(&different)
		if mode, err := reopened.Authorize(context.Background(), different); err != nil || mode != "sandbox" {
			t.Fatal("overbroad command match", different.Command, mode, err)
		}
	}
	var rule CommandRule
	for _, v := range reopened.state.Rules {
		rule = v
	}
	if err = reopened.DeleteRule("machine", rule.ID, policy.Revision); err != nil {
		t.Fatal(err)
	}
	if mode, err := reopened.Authorize(context.Background(), r); err != nil || mode != "sandbox" {
		t.Fatal("removed rule survived", mode, err)
	}
}
func TestTaskGrantIsSameCommandAndCurrentWorkOnly(t *testing.T) {
	t.Parallel()
	p := permissionFixture(t)
	r := hostRequest("approve")
	decideRequest(t, p, r, "allow_task")
	r.CallID = "repeat"
	r.Host = false
	if mode, err := p.Authorize(context.Background(), r); err != nil || mode != "host" {
		t.Fatal(mode, err)
	}
	for _, change := range []func(*HostRequest){func(r *HostRequest) { r.Owner = "other" }, func(r *HostRequest) { r.Turn = "other" }, func(r *HostRequest) { r.Generation++ }, func(r *HostRequest) { copy := *r.Command; copy.Command = "npm install"; r.Command = &copy }} {
		different := r
		change(&different)
		if mode, err := p.Authorize(context.Background(), different); err != nil || mode != "sandbox" {
			t.Fatal("task grant broadened", mode, err)
		}
	}
	p.Expire(r.Owner, r.Turn, r.Generation)
	if mode, err := p.Authorize(context.Background(), r); err != nil || mode != "sandbox" {
		t.Fatal(mode, err)
	}
	if len(p.grants) != 0 {
		t.Fatal("expired grant retained")
	}
}
func TestExplicitDenialSurvivesBroadTrustAndBlockedAllowsCleanup(t *testing.T) {
	t.Parallel()
	p := permissionFixture(t)
	r := hostRequest("denied")
	decideRequest(t, p, r, "deny") // A grant elsewhere must not reverse this exact request.
	p.mu.Lock()
	n := p.copyLocked()
	v := n.Machines["machine"]
	v.ExecutionAccess = "allow"
	n.Machines["machine"] = v
	err := p.saveLocked(n)
	p.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.Authorize(context.Background(), r); !errors.Is(err, ErrHostDenied) {
		t.Fatal(err)
	}
	rebound := r
	rebound.Args = json.RawMessage(`{"cmd":"another command"}`)
	if _, err = p.Authorize(context.Background(), rebound); !errors.Is(err, ErrHostDenied) {
		t.Fatal("denied call identity rebound", err)
	}
	if err = p.Configure("machine", MachinePolicy{ExecutionAccess: "deny"}); err != nil {
		t.Fatal(err)
	}
	r.CallID = "blocked"
	if _, err = p.Authorize(context.Background(), r); !errors.Is(err, ErrHostDenied) {
		t.Fatal(err)
	}
	r.Tool = "write_stdin"
	r.Args = json.RawMessage(`{"session_id":"owned","chars":""}`)
	r.Command = nil
	if m, e := p.Authorize(context.Background(), r); e != nil || m != "session" {
		t.Fatal("cleanup blocked", m, e)
	}
	r.Args = json.RawMessage(`{"session_id":"owned","chars":"execute"}`)
	if _, e := p.Authorize(context.Background(), r); !errors.Is(e, ErrHostDenied) {
		t.Fatal("blocked input accepted", e)
	}
}
func TestRulesDenyHostBeforeTrustedAndManualCRUD(t *testing.T) {
	t.Parallel()
	p := permissionFixture(t)
	r := hostRequest("manual")
	rule := CommandRule{MachineID: "machine", Context: *r.Command, Policy: "deny_host"}
	policy, _, _ := p.Policy("machine")
	if err := p.SaveRule("owner", rule, "", policy.Revision); err != nil {
		t.Fatal(err)
	}
	if err := p.Configure("machine", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Authorize(context.Background(), r); !errors.Is(err, ErrHostDenied) {
		t.Fatal("deny overridden", err)
	}
	r.Host = false
	if mode, err := p.Authorize(context.Background(), r); err != nil || mode != "sandbox" {
		t.Fatal(mode, err)
	}
	if err := p.SaveRule("owner", rule, "", 1); err == nil {
		t.Fatal("stale rule edit accepted")
	}
	if err := p.Enroll("machine", "new-epoch"); err != nil {
		t.Fatal(err)
	}
	if len(p.state.Rules) != 0 {
		t.Fatal("re-enrollment retained old grants")
	}
}
func TestHostPatchUsesNormalPolicyAndExactOneTimeApproval(t *testing.T) {
	t.Parallel()
	for _, policy := range []string{"ask", "allow", "deny"} {
		t.Run(policy, func(t *testing.T) {
			p := permissionFixture(t)
			if err := p.Configure("machine", MachinePolicy{ExecutionAccess: policy}); err != nil {
				t.Fatal(err)
			}
			r := hostRequest("patch")
			r.Tool = "apply_patch"
			r.Command = nil
			r.Args = json.RawMessage(`{"patch":"exact patch","cwd":"/project"}`)
			if policy == "allow" {
				mode, err := p.Authorize(t.Context(), r)
				if err != nil || mode != "host" {
					t.Fatal(mode, err)
				}
				return
			}
			if policy == "deny" {
				if _, err := p.Authorize(t.Context(), r); !errors.Is(err, ErrHostDenied) {
					t.Fatal(err)
				}
				return
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := p.Authorize(ctx, r); done <- err }()
			a := waitApproval(t, p)
			for _, action := range []string{"allow_task", "allow_always"} {
				if err := p.Decide(r.Owner, a.ID, action); err == nil {
					t.Fatal("patch gained broad command grant")
				}
			}
			if err := p.Decide(r.Owner, a.ID, "allow_once"); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPersistenceFailureAndPendingApplicationFailClosed(t *testing.T) {
	t.Parallel()
	p := permissionFixture(t)
	r := hostRequest("write-failure")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := p.Authorize(ctx, r); done <- e }()
	a := waitApproval(t, p)
	p.path = t.TempDir()
	if err := p.Decide(r.Owner, a.ID, "allow_always"); err == nil {
		t.Fatal("failed persistence permitted execution")
	}
	if len(p.Pending(r.Owner)) != 1 || len(p.state.Rules) != 0 {
		t.Fatal("failed save changed authority")
	}
	cancel()
	<-done
	p = permissionFixture(t)
	p.Apply = func(string, uint64) error { return errors.New("remote offline") }
	if err := p.Configure("machine", MachinePolicy{ExecutionAccess: "allow"}); err == nil {
		t.Fatal("missing application failure")
	}
	if _, err := p.Authorize(context.Background(), r); err == nil {
		t.Fatal("pending policy admitted work")
	}
	if p.applied["machine"].State != "pending" {
		t.Fatal("pending application hidden")
	}
	reopened, err := OpenPermissions(p.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = reopened.Authorize(context.Background(), r); err == nil {
		t.Fatal("restart dropped pending application fence")
	}
	policy, epoch, _ := reopened.Policy("machine")
	if err = reopened.CheckRevision("machine", epoch, policy.Revision); err == nil {
		t.Fatal("pending revision admitted after restart")
	}
	if err = reopened.Retry("machine", policy.Revision); err != nil {
		t.Fatal(err)
	}
	if mode, err := reopened.Authorize(context.Background(), r); err != nil || mode != "host" {
		t.Fatal("confirmed retry did not reopen admission", mode, err)
	}
}
func TestOldFormatsArePreserved(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "permissions.json")
	body := []byte(`{"version":5,"machines":{},"epochs":{}}`)
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenPermissions(path); err == nil {
		t.Fatal("legacy format accepted")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(body) {
		t.Fatal("old file overwritten")
	}
}

func TestApplicationReceiptSaveFailureRetainsDurableFence(t *testing.T) {
	t.Parallel()
	p := permissionFixture(t)
	path := p.path
	p.Apply = func(string, uint64) error { p.path = t.TempDir(); return nil }
	if err := p.Configure("machine", MachinePolicy{ExecutionAccess: "allow"}); err == nil {
		t.Fatal("failed application receipt save acknowledged")
	}
	reopened, err := OpenPermissions(path)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.applied["machine"].State != "applying" {
		t.Fatal("pending fence lost after receipt save failure")
	}
	if _, err = reopened.Authorize(context.Background(), hostRequest("after-restart")); err == nil {
		t.Fatal("unpersisted application result admitted work")
	}
	policy, _, _ := reopened.Policy("machine")
	if err = reopened.Retry("machine", policy.Revision); err == nil {
		t.Fatal("restart fabricated missing cleanup proof")
	}
	if err = reopened.Configure("machine", policy); err == nil {
		t.Fatal("settings save bypassed recovery fence")
	}
}

func TestExecutionAndPermissionTargetsRemainLocal(t *testing.T) {
	t.Parallel()
	s := taskControl(t, "node", "")
	ctx := context.Background()
	if _, err := s.Tasks.LocalMachine(ctx, "remote"); err == nil {
		t.Fatal("distributed execution target accepted")
	}
	if err := s.applyPermissionRevision("remote", 1); err == nil {
		t.Fatal("remote permission revision forwarded")
	}
	if target, err := s.Tasks.LocalMachine(ctx, ""); err != nil || target != "node" {
		t.Fatal(target, err)
	}
}
