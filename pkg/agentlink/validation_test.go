package agentlink

import (
	"github.com/jjyr/pot/pkg/protocol"
	"testing"
)

func TestGenericAdapterValidationRetainsNodeAndReceiptBindings(t *testing.T) {
	identity := Identity{Address: protocol.AgentRef{NodeID: "receiver", Kind: "node", AgentID: "receiver"}, Adapter: "fixture-agent", Name: "Agent"}
	if !validIdentity(identity) || identity.Address != NodeTarget(identity.Address.NodeID) {
		t.Fatal(identity)
	}
	for _, adapter := range []string{"", "..", "../agent", "agent/type", string(make([]byte, 129))} {
		invalid := identity
		invalid.Adapter = adapter
		if validIdentity(invalid) {
			t.Fatal("invalid adapter identity accepted", adapter)
		}
		if err := validateDirectory(Directory{NodeID: "receiver", Name: "Agent", Adapter: adapter}, "receiver"); err == nil {
			t.Fatal("invalid adapter directory accepted", adapter)
		}
	}
	directory := Directory{NodeID: "receiver", Name: "Agent", Adapter: "fixture-agent", Targets: []Endpoint{{Identity: identity, Capabilities: Description{Version: 1, Scope: "caller", Operations: []Operation{TaskSubmit, TaskRead}}}}}
	if err := validateDirectory(directory, "receiver"); err != nil {
		t.Fatal(err)
	}
	if err := validateDirectory(directory, "other-node"); err == nil {
		t.Fatal("foreign directory accepted")
	}
	identity.Address = protocol.AgentRef{NodeID: "receiver", Kind: "task", AgentID: "task"}
	receipt := Task{ID: "task", RequestID: "request", Receiver: identity, State: "queued"}
	if err := validateTask(receipt, "receiver", "fixture-agent", "task", "request"); err != nil {
		t.Fatal(err)
	}
	for _, binding := range [][4]string{{"wrong-node", "fixture-agent", "task", "request"}, {"receiver", "other-agent", "task", "request"}, {"receiver", "fixture-agent", "other-task", "request"}, {"receiver", "fixture-agent", "task", "other-request"}} {
		if err := validateTask(receipt, binding[0], binding[1], binding[2], binding[3]); err == nil {
			t.Fatal("receipt binding ignored", binding)
		}
	}
	receipt.Receiver.Address.AgentID = "different-task"
	if err := validateTask(receipt, "receiver", "fixture-agent", "task", "request"); err == nil {
		t.Fatal("execution identity mismatch accepted")
	}
}
