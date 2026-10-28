package agentlink_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

type mcpFixture struct {
	Submit          agentlink.MCPSubmitInput          `json:"submit"`
	Input           agentlink.MCPTaskInput            `json:"input"`
	Cancel          agentlink.MCPCancelInput          `json:"cancel"`
	Registration    agentlink.MCPReceiverRegistration `json:"registration"`
	Work            agentlink.MCPWorkDelivery         `json:"work"`
	Claim           agentlink.MCPClaim                `json:"claim"`
	Ack             agentlink.MCPDeliveryAck          `json:"ack"`
	Admission       agentlink.MCPRunAdmission         `json:"admission"`
	Event           agentlink.MCPExecutionEvent       `json:"event"`
	SubmitCanonical string                            `json:"submit_canonical"`
	SubmitDigest    string                            `json:"submit_digest"`
}

func loadMCPFixture(t *testing.T) (mcpFixture, agentlink.MCPCaller, agentlink.MCPReceiver) {
	t.Helper()
	data, err := os.ReadFile("testdata/mcp_c0.json")
	if err != nil {
		t.Fatal(err)
	}
	var f mcpFixture
	if err := agentlink.DecodeMCPContract(data, &f); err != nil {
		t.Fatal(err)
	}
	c, err := agentlink.NewMCPCaller("connection-1", f.Work.Request.From)
	if err != nil {
		t.Fatal(err)
	}
	r, err := agentlink.NewMCPReceiver("connection-1", "receiver-node")
	if err != nil {
		t.Fatal(err)
	}
	return f, c, r
}

func TestMCPGoldenContract(t *testing.T) {
	f, caller, receiver := loadMCPFixture(t)
	for name, err := range map[string]error{
		"submit": f.Submit.Validate(), "input": f.Input.Validate(), "cancel": f.Cancel.Validate(),
		"registration": f.Registration.ValidateAgainst(receiver, f.Registration.Capabilities),
		"work":         f.Work.Validate(), "ack": f.Ack.Validate(), "event": f.Event.ValidateAfter(receiver, f.Claim, f.Admission.Binding, 1),
		"admission": f.Admission.ValidateAgainst(receiver, f.Claim, nil),
	} {
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	bound, err := f.Submit.Bind(caller)
	if err != nil || bound.From != f.Work.Request.From || bound.Configuration != nil || bound.Workspace != "" {
		t.Fatalf("caller binding: %+v, %v", bound, err)
	}
	digest, err := f.Submit.Digest(caller)
	if err != nil || digest != f.SubmitDigest {
		t.Fatalf("digest %s, want %s: %v", digest, f.SubmitDigest, err)
	}
	var canonical any
	if err := json.Unmarshal([]byte(f.SubmitCanonical), &canonical); err != nil {
		t.Fatal(err)
	}
	canonicalDigest, err := core.HashJSON(canonical)
	if err != nil || canonicalDigest != digest {
		t.Fatalf("golden canonical input: %s, %v", canonicalDigest, err)
	}
	data, err := json.Marshal(f.Event)
	if err != nil {
		t.Fatal(err)
	}
	var event agentlink.MCPExecutionEvent
	if err := agentlink.DecodeMCPContract(data, &event); err != nil || event.Result.Artifacts[0] != "review.txt" {
		t.Fatalf("terminal result/artifact roundtrip: %v", err)
	}
}

func TestMCPRunAdmissionAndContinuation(t *testing.T) {
	f, caller, receiver := loadMCPFixture(t)
	// First delivery/claim/ack work before any backend run exists. The queue
	// resource and receiver-owned execution Task deliberately have different IDs.
	if f.Work.Binding.Task == f.Admission.Binding.Task {
		t.Fatal("fixture failed to exercise real execution mapping")
	}
	if err := f.Work.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := f.Ack.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := f.Event.ValidateAfter(receiver, f.Claim, agentlink.MCPRunBinding{}, 0); !errors.Is(err, agentlink.ErrFenced) {
		t.Fatalf("event before committed real run admission: %v", err)
	}
	if err := f.Admission.ValidateAgainst(receiver, f.Claim, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.Event.ValidateAfter(receiver, f.Claim, f.Admission.Binding, 0); err != nil {
		t.Fatal(err)
	}
	// Terminal continuation targets the old run, but the next accepted execution
	// must report a distinct real run on its own delivery/claim.
	current := agentlink.Task{ID: f.Input.TaskID, RunID: f.Input.RunID, State: "complete"}
	if _, err := f.Input.Guidance(caller, current); err != nil {
		t.Fatal(err)
	}
	next := f.Admission
	next.Claim.DeliveryID = "input-delivery"
	next.Claim.ClaimID = "input-claim"
	next.PreviousRunID = f.Admission.Binding.RunID
	next.Binding.RunID = "run-2"
	if err := next.ValidateAgainst(receiver, next.Claim, &f.Admission.Binding); err != nil {
		t.Fatal(err)
	}
	stale := next
	stale.Binding.RunID = f.Admission.Binding.RunID
	if err := stale.ValidateAgainst(receiver, next.Claim, &f.Admission.Binding); !errors.Is(err, agentlink.ErrFenced) {
		t.Fatalf("continuation reused old run: %v", err)
	}
	if err := next.ValidateAgainst(receiver, next.Claim, nil); !errors.Is(err, agentlink.ErrFenced) {
		t.Fatalf("continuation lost previous run CAS: %v", err)
	}
	late := f.Event
	late.Claim = next.Claim
	if err := late.ValidateAfter(receiver, next.Claim, next.Binding, 0); !errors.Is(err, agentlink.ErrFenced) {
		t.Fatalf("old run result on new input claim: %v", err)
	}
	result := *late.Result
	result.RunID = next.Binding.RunID
	late.Result = &result
	late.Binding = next.Binding
	if err := late.ValidateAfter(receiver, next.Claim, next.Binding, 0); err != nil {
		t.Fatalf("new run result: %v", err)
	}
}

func TestMCPIdentityBoundary(t *testing.T) {
	f, caller, _ := loadMCPFixture(t)
	actor := protocol.AgentRef{NodeID: "node-1", Kind: "assistant", AgentID: "assistant-1"}
	if _, err := agentlink.NewMCPNodeCaller("connection-1", "other-node", actor, nil); !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatalf("cross-node actor: %v", err)
	}
	for _, kind := range []string{"user", "runtime", "node"} {
		bad := actor
		bad.Kind = kind
		if _, err := agentlink.NewMCPNodeCaller("connection-1", "node-1", bad, nil); !errors.Is(err, agentlink.ErrUnauthorized) {
			t.Fatalf("node asserted %s: %v", kind, err)
		}
	}
	if _, err := agentlink.NewMCPCaller("connection-1", actor); !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatalf("ordinary login asserted execution actor: %v", err)
	}
	task := protocol.AgentRef{NodeID: "node-1", Kind: "task", AgentID: "task-1"}
	binding := agentlink.MCPExecutionBinding{Actor: task, Task: task, RunID: "run-1"}
	if _, err := agentlink.NewMCPNodeCaller("connection-1", "node-1", task, nil); !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatalf("task without run: %v", err)
	}
	executionCaller, err := agentlink.NewMCPNodeCaller("connection-1", "node-1", task, &binding)
	if err != nil {
		t.Fatal(err)
	}
	binding.RunID = "tampered"
	executionCaller.Execution().RunID = "tampered-again"
	if executionCaller.Execution().RunID != "run-1" {
		t.Fatal("caller retained mutable execution metadata")
	}
	binding.Task.AgentID = "other-task"
	if err := binding.Validate(); !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatalf("cross-task execution: %v", err)
	}
	if _, err := f.Submit.Bind(agentlink.MCPCaller{}); !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatalf("zero caller accepted: %v", err)
	}
	otherActor := caller.Actor()
	otherActor.AgentID = "other-caller"
	other, err := agentlink.NewMCPCaller(caller.ConnectionID(), otherActor)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := f.Submit.Digest(other)
	if err != nil || digest == f.SubmitDigest {
		t.Fatalf("effective actor missing from digest: %v", err)
	}
	first, err := f.Submit.Digest(executionCaller)
	if err != nil {
		t.Fatal(err)
	}
	changed := *executionCaller.Execution()
	changed.RunID = "run-2"
	next, err := agentlink.NewMCPNodeCaller("connection-1", "node-1", task, &changed)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.Submit.Digest(next)
	if err != nil || first == second {
		t.Fatalf("execution run missing from digest: %v", err)
	}
}

func TestMCPMutationRetry(t *testing.T) {
	f, caller, _ := loadMCPFixture(t)
	receipt := agentlink.MCPMutationReceipt{RequestID: f.Submit.RequestID, Digest: f.SubmitDigest}
	retry, err := receipt.Retry(f.Submit.RequestID, f.SubmitDigest)
	if err != nil || !retry.Duplicate || retry.Digest != receipt.Digest {
		t.Fatalf("exact retry: %+v, %v", retry, err)
	}
	changed := f.Submit
	changed.Text += " Changed."
	digest, err := changed.Digest(caller)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := receipt.Retry(changed.RequestID, digest); !errors.Is(err, agentlink.ErrConflict) {
		t.Fatalf("conflicting request reuse: %v", err)
	}
	if got := (agentlink.MCPError{Code: agentlink.MCPUnknown}).Retry(); got != agentlink.MCPReconcile {
		t.Fatalf("unknown effects permit replay: %s", got)
	}
	for _, code := range []agentlink.MCPErrorCode{agentlink.MCPConflict, agentlink.MCPFenced, agentlink.MCPUnsupported, agentlink.MCPUnauthorized} {
		if (agentlink.MCPError{Code: code}).Retry() != agentlink.MCPDoNotRetry {
			t.Fatalf("unsafe retry for %s", code)
		}
	}
	current := agentlink.Task{ID: f.Input.TaskID, RunID: f.Input.RunID, State: "completed"}
	if _, err := f.Input.Guidance(caller, current); err != nil {
		t.Fatal(err)
	}
	current.State = "running"
	if _, err := f.Input.Guidance(caller, current); err != nil {
		t.Fatalf("active mailbox admission rejected: %v", err)
	}
	current.RunID = "new-run"
	if _, err := f.Input.Guidance(caller, current); !errors.Is(err, agentlink.ErrFenced) {
		t.Fatalf("stale input run: %v", err)
	}
}

func TestMCPReceiverBoundaries(t *testing.T) {
	f, _, receiver := loadMCPFixture(t)
	actual := f.Registration.Capabilities
	actual.Operations = []agentlink.Operation{agentlink.TaskSubmit}
	if err := f.Registration.ValidateAgainst(receiver, actual); !errors.Is(err, agentlink.ErrUnsupported) {
		t.Fatalf("unimplemented driver operation advertised: %v", err)
	}
	registration := f.Registration
	registration.Capabilities.Operations = []agentlink.Operation{agentlink.TaskConfigure}
	if err := registration.ValidateAgainst(receiver, registration.Capabilities); !errors.Is(err, agentlink.ErrUnsupported) {
		t.Fatalf("owner configuration authority advertised: %v", err)
	}
	registration = f.Registration
	registration.Capabilities.Scope = "owner"
	if err := registration.ValidateAgainst(receiver, registration.Capabilities); !errors.Is(err, agentlink.ErrUnsupported) {
		t.Fatalf("owner scope advertised: %v", err)
	}
	other, err := agentlink.NewMCPReceiver("connection-1", "other-node")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Claim.ValidateAgainst(other, f.Claim); !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatalf("cross-node claim: %v", err)
	}
	other, err = agentlink.NewMCPReceiver("other-connection", receiver.NodeID())
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Claim.ValidateAgainst(other, f.Claim); !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatalf("cross-connection claim: %v", err)
	}
	for _, field := range []string{"claim", "delivery", "task", "receiver"} {
		t.Run(field, func(t *testing.T) {
			stale := f.Claim
			switch field {
			case "claim":
				stale.ClaimID = "old-claim"
			case "delivery":
				stale.DeliveryID = "other-delivery"
			case "task":
				stale.Binding.Task.AgentID = "other-task"
			case "receiver":
				stale.Binding.Receiver.AgentID = "other-assistant"
			}
			if err := stale.ValidateAgainst(receiver, f.Claim); !errors.Is(err, agentlink.ErrFenced) {
				t.Fatalf("stale binding: %v", err)
			}
		})
	}
	if err := f.Event.ValidateAfter(receiver, f.Claim, f.Admission.Binding, f.Event.Sequence); !errors.Is(err, agentlink.ErrFenced) {
		t.Fatalf("stale event: %v", err)
	}
	digest, err := f.Event.Digest()
	if err != nil {
		t.Fatal(err)
	}
	receipt := agentlink.MCPEventReceipt{Claim: f.Event.Claim, Sequence: f.Event.Sequence, Digest: digest}
	if repeated, err := receipt.Retry(f.Event); err != nil || !repeated.Duplicate {
		t.Fatalf("identical event retry: %+v, %v", repeated, err)
	}
	conflict := f.Event
	conflict.ThreadID = "different-thread"
	if _, err := receipt.Retry(conflict); !errors.Is(err, agentlink.ErrConflict) {
		t.Fatalf("conflicting sequence reuse: %v", err)
	}
	event := f.Event
	result := *event.Result
	result.RunID = "other-run"
	event.Result = &result
	if err := event.Validate(); !errors.Is(err, agentlink.ErrInvalid) {
		t.Fatalf("cross-run result: %v", err)
	}
	control := agentlink.MCPControlDelivery{Version: 1, DeliveryID: "cancel-delivery", Dispatch: f.Claim.Binding, Binding: f.Admission.Binding, Caller: f.Work.Request.From, Cancel: &f.Cancel}
	if err := control.Validate(); err != nil {
		t.Fatal(err)
	}
	control.Input = &f.Input
	if err := control.Validate(); !errors.Is(err, agentlink.ErrInvalid) {
		t.Fatalf("ambiguous control: %v", err)
	}
	control.Input = nil
	control.Binding.RunID = "new-run"
	if err := control.Validate(); !errors.Is(err, agentlink.ErrFenced) {
		t.Fatalf("cross-run cancel: %v", err)
	}
}

func TestMCPStrictSchemaAndBounds(t *testing.T) {
	f, _, _ := loadMCPFixture(t)
	data, err := json.Marshal(f.Submit)
	if err != nil {
		t.Fatal(err)
	}
	base := string(data)
	for name, input := range map[string]string{
		"sender":         strings.TrimSuffix(base, "}") + `,"from":{"node_id":"attacker","kind":"user","agent_id":"owner"}}`,
		"duplicate":      strings.TrimSuffix(base, "}") + `,"request_id":"other"}`,
		"null":           strings.Replace(base, `"request_id":"request-1"`, `"request_id":null`, 1),
		"missing":        strings.Replace(base, `"request_id":"request-1",`, ``, 1),
		"trailing":       base + `{}`,
		"unknown nested": strings.Replace(base, `"node_id":"receiver-node"`, `"node_id":"receiver-node","owner":true`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			var decoded agentlink.MCPSubmitInput
			if err := agentlink.DecodeMCPContract([]byte(input), &decoded); err == nil {
				t.Fatal("invalid tool schema accepted")
			}
		})
	}
	f.Submit.Text = strings.Repeat("x", agentlink.MCPMaxTextBytes)
	if err := f.Submit.Validate(); err != nil {
		t.Fatal(err)
	}
	f.Submit.Text += "x"
	if err := f.Submit.Validate(); !errors.Is(err, agentlink.ErrInvalid) {
		t.Fatalf("oversize submission: %v", err)
	}
	if err := agentlink.DecodeMCPContract(make([]byte, agentlink.MCPMaxPayloadBytes+1), &f.Submit); !errors.Is(err, agentlink.ErrInvalid) {
		t.Fatalf("payload ceiling: %v", err)
	}
	f.Event.Result.Text = strings.Repeat("x", agentlink.MCPMaxResultBytes+1)
	if err := f.Event.Validate(); !errors.Is(err, agentlink.ErrInvalid) {
		t.Fatalf("oversize result: %v", err)
	}
}
