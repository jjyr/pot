package control

import (
	"errors"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestMCPOwnerGuidanceRetrySurvivesRunAdmission(t *testing.T) {
	a, _ := potMCPControl(t, "guidance-a")
	b, driver := potMCPControl(t, "guidance-b")
	server := potMCPServer(t, a)
	c := potMCPAuthorize(t, b, server, "")
	client := potMCPClient(t, b, c.ID)
	target := agentlink.AssistantTarget(b.options.RuntimeID, "main")
	info, err := a.CreateTask(t.Context(), "main", protocol.CreateTask{RequestID: "guidance-task", Text: "synthetic work", Target: &target})
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := agentlink.NewMCPReceiver(c.ID, b.options.RuntimeID)
	receiver := &potMCPReceiver{s: b, identity: identity, remote: client}
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	records, err := receiver.Records(t.Context())
	if err != nil || len(records) != 1 || records[0].Admission == nil {
		t.Fatal(err)
	}
	first := records[0].Admission.Binding
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	driver.complete(first.RunID)
	if err = b.Tasks.Tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	input := protocol.SendMail{To: *info.Address, Text: "synthetic continuation"}
	owner := agentlink.AssistantTarget(a.options.RuntimeID, "main")
	if _, err = a.sendAgentMessage(t.Context(), "same-owner-guidance", "main", owner, input); err != nil {
		t.Fatal(err)
	}
	potMCPStep(t, client, receiver)
	potMCPStep(t, client, receiver)
	records, err = receiver.Records(t.Context())
	if err != nil || len(records) != 2 || records[1].Admission == nil || records[1].Admission.Binding.RunID == first.RunID {
		t.Fatal("continuation was not admitted", err)
	}
	if _, err = a.sendAgentMessage(t.Context(), "same-owner-guidance", "main", owner, input); err != nil {
		t.Fatalf("identical owner guidance retry changed its implicit expected run after admission: %v", err)
	}
	current, err := a.Tasks.Get(t.Context(), "main", info.ID)
	if err != nil || current.Execution.Receipt.RunID != records[1].Admission.Binding.RunID || current.Info.Phase.Scope() != "active" {
		t.Fatal("duplicate acceptance rolled back the admitted run", err)
	}
	changed := input
	changed.Text += " changed"
	_, err = a.sendAgentMessage(t.Context(), "same-owner-guidance", "main", owner, changed)
	var failure agentlink.MCPError
	if !errors.As(err, &failure) || failure.Code != agentlink.MCPConflict {
		t.Fatal("changed content did not conflict", err)
	}
	caller, err := agentlink.NewMCPNodeCaller(c.ID, a.options.RuntimeID, owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Only the implicit facade run is pinned. An explicit wire run change must
	// remain a different payload, even for the same owner and text.
	wire := agentlink.MCPTaskInput{RequestID: "same-owner-guidance", TaskID: info.ID, RunID: records[1].Admission.Binding.RunID, Text: input.Text}
	if _, err = server.queue.Control(t.Context(), caller, &wire, nil); !errors.Is(err, agentlink.ErrConflict) {
		t.Fatal("changed explicit run became an identical retry", err)
	}
	wire.RunID = first.RunID
	if accepted, err := server.queue.Control(t.Context(), caller, &wire, nil); err != nil || !accepted.Receipt.Duplicate {
		t.Fatal("original explicit input did not reconcile", err)
	}
	foreign := agentlink.AssistantTarget(a.options.RuntimeID, "other-owner")
	if _, err = a.sendAgentMessage(t.Context(), "foreign-owner", "main", foreign, input); !errors.Is(err, agentlink.ErrUnauthorized) {
		t.Fatal("different owner gained guidance authority", err)
	}
	wrong := input
	wrong.To.NodeID = "other-node"
	if _, err = a.sendAgentMessage(t.Context(), "same-owner-guidance", "main", owner, wrong); err == nil {
		t.Fatal("wrong-node Task address accepted")
	}
	var controls int
	if err = server.queue.DB.QueryRow("SELECT COUNT(*) FROM mcp_service_deliveries WHERE task_id=? AND control!=''", info.ID).Scan(&controls); err != nil || controls != 1 {
		t.Fatal("owner guidance was delivered twice", controls, err)
	}
}
