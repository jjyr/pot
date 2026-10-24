package control

import (
	"context"
	"github.com/jjyr/pot/pkg/core"
	"strings"
	"testing"
)

func TestOwnerResourceRetriesKeepOriginalReceiptAndFenceUnknownReplacement(t *testing.T) {
	t.Parallel()
	s := taskResourceService(t)
	ctx := context.Background()
	calls := 0
	apply := func() (any, error) {
		calls++
		err := s.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "ask"})
		return map[string]string{"policy": "ask"}, err
	}
	first, err := s.OwnerResourceMutation(ctx, "host-create", "host", strings.Repeat("a", 64), apply)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Permissions.Configure("local", MachinePolicy{ExecutionAccess: "allow"}); err != nil {
		t.Fatal(err)
	}
	again, err := s.OwnerResourceMutation(ctx, "host-create", "host", strings.Repeat("a", 64), apply)
	if err != nil || string(again) != string(first) || calls != 1 {
		t.Fatal(string(again), calls, err)
	}
	policy, _, _ := s.Permissions.Policy("local")
	if policy.ExecutionAccess != "allow" {
		t.Fatal("old Host create reset later policy")
	}
	pending := []byte(`{"request_id":"lost","state":"pending"}`)
	if _, err = s.Tasks.DB().ExecContext(ctx, "INSERT INTO owner_resource_commands VALUES(?,?,?,?)", "lost", strings.Repeat("b", 64), "assistant:shared", string(pending)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.OwnerResourceMutation(ctx, "new-id", "assistant:shared", strings.Repeat("c", 64), apply); err == nil {
		t.Fatal("unknown old resource command allowed replacement")
	}
	if calls != 1 {
		t.Fatal("effect ran despite uncertainty")
	}
	if _, err = s.OwnerResourceMutation(ctx, "unrelated", "host", strings.Repeat("d", 64), func() (any, error) { return true, nil }); err != nil {
		t.Fatal("unrelated resource blocked", err)
	}
}

func TestUncertainResourceFailureFencesReplacement(t *testing.T) {
	t.Parallel()
	s := taskResourceService(t)
	ctx := context.Background()
	_, err := s.OwnerResourceMutation(ctx, "unknown", "assistant:fixed", strings.Repeat("a", 64), func() (any, error) { return nil, core.ErrCleanupUnconfirmed })
	if err == nil {
		t.Fatal("uncertain effect accepted")
	}
	receipts, err := s.OwnerResourceReceipts(ctx)
	if err != nil || len(receipts) != 1 || receipts[0].State != "pending" {
		t.Fatal(receipts, err)
	}
	_, err = s.OwnerResourceMutation(ctx, "replacement", "assistant:fixed", strings.Repeat("b", 64), func() (any, error) { t.Fatal("unknown effect replaced"); return nil, nil })
	if err == nil {
		t.Fatal("replacement admitted")
	}
}
