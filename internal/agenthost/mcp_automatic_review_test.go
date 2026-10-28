package agenthost

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
)

func TestMCPAutomaticReviewDoesNotPublishApprovalHistory(t *testing.T) {
	origin, pot, owner := receiverGatewayPot(t)
	p, _, _ := receiverFixture(t)
	ctx := t.Context()
	token, connection, err := authorizeReceiver(ctx, origin+"/mcp", p.backend.nodeID, receiverConsentBrowser(t, origin))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := agentlink.NewMCPReceiver(connection, p.backend.nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.control.Exec("UPDATE mcp_receiver_connections SET id=?,url=? WHERE id=?", connection, origin+"/mcp", p.identity.ConnectionID()); err != nil {
		t.Fatal(err)
	}
	p.identity = identity
	client, err := connectReceiver(ctx, origin+"/mcp", token.AccessToken, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	if err = client.Register(ctx, agentlink.MCPReceiverRegistration{Version: 1, Receiver: agentlink.Identity{Address: agentlink.NodeTarget(p.backend.nodeID), Name: "Automatic review fixture", Adapter: "codex"}, Capabilities: Codex().ReceiverCapabilities()}); err != nil {
		t.Fatal(err)
	}
	p.remote = client
	target := agentlink.NodeTarget(p.backend.nodeID)
	accepted, err := pot.CreateTask(ctx, owner.AgentID, protocol.CreateTask{RequestID: "automatic-mcp", Target: &target, Text: "auto-review\nAn operation handled by Codex's reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	var delivery string
	if err = p.backend.db.QueryRow("SELECT delivery_id FROM mcp_receiver_journal WHERE connection_id=?", connection).Scan(&delivery); err != nil {
		t.Fatal(err)
	}
	record, err := readReceiverRecord(ctx, p.backend.db, connection, delivery)
	if err != nil || record.Admission == nil {
		t.Fatal(record, err)
	}
	j, err := p.backend.read(ctx, record.Work.Request.From, record.Admission.Binding.Task.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	// The fake native executable emits real autoApprovalReview JSONL events;
	// production parsing, receiver event projection and Pot approval reads run.
	p.backend.runTask(ctx, j)
	receiverMustStep(t, p)
	remote, err := pot.Tasks.Get(ctx, owner.AgentID, accepted.ID)
	if err != nil || remote == nil || remote.Info.Phase != protocol.TaskComplete || remote.Execution == nil || strings.Contains(remote.Execution.Receipt.Result, "private automatic review") {
		t.Fatal("automatic review leaked through Task result", remote, err)
	}
	view := pot.ApprovalView(ctx, owner.AgentID)
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Pending []json.RawMessage `json:"pending"`
	}
	if err = json.Unmarshal(raw, &decoded); err != nil || decoded.Pending == nil || len(decoded.Pending) != 0 || view["linked_reviews"] != nil || strings.Contains(string(raw), "private automatic review") {
		t.Fatal("automatic review exposed approval history", string(raw), err)
	}
	native, err := p.backend.Review(ctx, reviewQuery(record.Work.Request.From))
	if err != nil || len(native.Pending) != 0 {
		t.Fatal("automatic review became a human request", native, err)
	}
	raw, err = json.Marshal(native)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"work", "can_decide"} {
		if _, found := fields[field]; found {
			t.Fatal("native review exposed execution history", field, string(raw))
		}
	}
}
