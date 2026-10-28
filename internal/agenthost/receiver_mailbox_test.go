package agenthost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/storage"
)

type mailboxLostResponse struct {
	receiverRemote
	point string
	lost  bool
}

func (r *mailboxLostResponse) lose(point string, err error) error {
	if err == nil && !r.lost && r.point == point {
		r.lost = true
		return errors.New("fixture_mailbox_response_lost")
	}
	return err
}
func (r *mailboxLostResponse) Ack(ctx context.Context, a agentlink.MCPDeliveryAck) error {
	return r.lose("ack", r.receiverRemote.Ack(ctx, a))
}
func (r *mailboxLostResponse) Admit(ctx context.Context, a agentlink.MCPRunAdmission) error {
	return r.lose("admit", r.receiverRemote.Admit(ctx, a))
}
func (r *mailboxLostResponse) InputReceipt(ctx context.Context, a agentlink.MCPInputReceipt) error {
	return r.lose("input", r.receiverRemote.InputReceipt(ctx, a))
}

func TestReceiverHTTPMailboxWaitsForNativeBoundaryAndRecoversFIFO(t *testing.T) {
	p, _, _ := receiverFixture(t)
	origin, _ := receiverPotHTTP(t)
	ctx, cancel := context.WithTimeout(t.Context(), testutil.Timeout(t, 40*time.Second))
	defer cancel()
	token, connection, err := authorizeReceiver(ctx, origin+"/mcp", p.backend.nodeID, receiverConsentBrowser(t, origin))
	if err != nil {
		t.Fatal(err)
	}
	identity, _ := agentlink.NewMCPReceiver(connection, p.backend.nodeID)
	if _, err = p.control.Exec("UPDATE mcp_receiver_connections SET id=?,url=? WHERE id=?", connection, origin+"/mcp", p.identity.ConnectionID()); err != nil {
		t.Fatal(err)
	}
	p.identity = identity
	client, err := connectReceiver(ctx, origin+"/mcp", token.AccessToken, identity)
	if err != nil {
		t.Fatal(err)
	}
	defer client.close()
	registration := agentlink.MCPReceiverRegistration{Version: 1, Receiver: agentlink.Identity{Address: agentlink.NodeTarget(p.backend.nodeID), Name: "Mailbox receiver", Adapter: "codex"}, Capabilities: Codex().ReceiverCapabilities()}
	if err = client.Register(ctx, registration); err != nil {
		t.Fatal(err)
	}
	callerToken, callerConnection, err := authorizeReceiver(ctx, origin+"/mcp", "unused-mailbox-caller", receiverConsentBrowser(t, origin))
	if err != nil {
		t.Fatal(err)
	}
	callerIdentity, _ := agentlink.NewMCPReceiver(callerConnection, "unused-mailbox-caller")
	caller, err := connectReceiver(ctx, origin+"/mcp", callerToken.AccessToken, callerIdentity)
	if err != nil {
		t.Fatal(err)
	}
	defer caller.close()
	var accepted control.MCPTaskAcceptance
	if err = caller.call(ctx, "pot_task_submit", agentlink.MCPSubmitInput{RequestID: "native-mailbox", Target: registration.Receiver.Address, Text: "hold-after-answer"}, &accepted); err != nil {
		t.Fatal(err)
	}
	p.remote = client
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	records, err := p.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	original := records[0]
	j, err := p.backend.read(ctx, original.Work.Request.From, original.Admission.Binding.Task.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	var workspace string
	if err = p.backend.db.QueryRow("SELECT workspace FROM agent_adapter_sessions WHERE task_id=?", j.Task.ID).Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { p.backend.runTask(ctx, j); close(done) }()
	ready := filepath.Join(workspace, "answer-ready")
	for {
		if _, err = os.Stat(ready); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native turn never reached hold boundary")
		case <-time.After(10 * time.Millisecond):
		}
	}
	receiverMustStep(t, p)
	run := original.Admission.Binding.RunID
	for _, id := range []string{"first", "second", "third"} {
		input := agentlink.MCPTaskInput{RequestID: id, TaskID: accepted.Task.ID, RunID: run, Text: id}
		if err = caller.call(ctx, "pot_task_input", input, &accepted); err != nil {
			t.Fatal("running native Task rejected durable input", err)
		}
	}
	var receipt control.MCPAccessReceipt
	if err = caller.call(ctx, "pot_task_receipt", control.MCPAccessReceiptInput{Operation: agentlink.TaskInput, RequestID: "first"}, &receipt); err != nil || receipt.State != "accepted" || receipt.InputState != "pending" || receipt.Input != nil {
		t.Fatal("acceptance was confused with consumption", receipt, err)
	}
	receiverMustStep(t, p) // first claim is durable before ACK exchange
	loss := &mailboxLostResponse{receiverRemote: client, point: "ack"}
	p.remote = loss
	if err = p.step(ctx); err == nil || !loss.lost {
		t.Fatal("ACK loss not injected", err)
	}
	p.remote = client
	for range 4 {
		receiverMustStep(t, p)
	}
	records, err = p.Records(ctx)
	if err != nil || len(records) != 4 {
		t.Fatal(len(records), err)
	}
	for _, r := range records[1:] {
		if !r.Ack.Accepted || !r.Acknowledged || r.Admission != nil {
			t.Fatal("active input interrupted native turn", r)
		}
	}
	current, err := p.backend.read(ctx, j.Request.From, j.Task.ID)
	if err != nil || current.Task.RunID != run || current.Task.State != "running" {
		t.Fatal("original turn changed", current, err)
	}
	if err = os.WriteFile(filepath.Join(workspace, "complete-turn"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("native completion did not settle")
	}
	// Close and reopen the actual receiver Tasks DB with pending inputs. Native
	// completion is already confirmed; restart itself cannot replay any turn.
	path := filepath.Join(p.backend.root, "tasks.sqlite")
	if err = p.backend.db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := storage.OpenDatabase(path, storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	b, err := newBackend(db, p.backend.nodeID, p.backend.root)
	if err != nil {
		t.Fatal(err)
	}
	b.binary = fakeBinary
	p = &receiverPump{backend: b, control: p.control, identity: identity, remote: client}
	assertRetired := receiverRetiredJobsFixture(t, p, 32)
	previous := run
	for i, id := range []string{"first", "second", "third"} {
		point := "admit"
		if i == 1 {
			point = "input"
		}
		loss = &mailboxLostResponse{receiverRemote: client, point: point}
		p.remote = loss
		if err = p.step(ctx); err == nil || !loss.lost {
			t.Fatal("response loss not injected", point, err)
		}
		p.remote = client
		receiverMustStep(t, p)
		records, err = p.Records(ctx)
		if err != nil {
			t.Fatal(err)
		}
		r := records[i+1]
		if r.Admission == nil || r.Admission.PreviousRunID != previous || r.InputReceipt == nil || r.InputReceipt.State != "consumed" || !r.InputAcknowledged || r.Control.Binding.RunID != run {
			t.Fatal("FIFO or durable run binding lost", r)
		}
		if err = caller.call(ctx, "pot_task_receipt", control.MCPAccessReceiptInput{Operation: agentlink.TaskInput, RequestID: id}, &receipt); err != nil || receipt.InputState != "consumed" || receipt.Input == nil || receipt.Input.Binding != r.Admission.Binding {
			t.Fatal("missing consumed receipt", receipt, err)
		}
		forged := *r.InputReceipt
		forged.Binding.RunID = "foreign-run"
		if err = client.InputReceipt(ctx, forged); err == nil {
			t.Fatal("forged consumption receipt accepted")
		}
		current, err = b.read(ctx, j.Request.From, j.Task.ID)
		if err != nil || current.Task.RunID != r.Admission.Binding.RunID || current.Task.State != "queued" {
			t.Fatal(current, err)
		}
		b.runTask(ctx, current)
		current, err = b.read(ctx, j.Request.From, j.Task.ID)
		if err != nil || current.Task.State != "completed" || current.Task.Result != "result: "+id+"\n" {
			t.Fatal("native consumed wrong message", current, err)
		}
		previous = current.Task.RunID
	}
	receiverMustStep(t, p)
	records, err = p.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range records {
		if r.Receipt == nil || r.LastPhase != "complete" {
			t.Fatal("missing per-run terminal receipt", r)
		}
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM agent_adapter_inputs WHERE task_id=?", j.Task.ID).Scan(&count); err != nil || count != 3 {
		t.Fatal("duplicate native continuation", count, err)
	}
	assertRetired()
}

func TestReceiverEndedCancelCannotStopNativeContinuation(t *testing.T) {
	p, remote, d := receiverFixture(t)
	d.Work.Request.Text = "hold-after-answer"
	ctx, cancel := context.WithTimeout(t.Context(), testutil.Timeout(t, 20*time.Second))
	defer cancel()
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	original := receiverRecordFixture(t, p, d)
	j, err := p.backend.read(ctx, d.Work.Request.From, original.Admission.Binding.Task.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	var workspace string
	if err = p.backend.db.QueryRow("SELECT workspace FROM agent_adapter_sessions WHERE task_id=?", j.Task.ID).Scan(&workspace); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { p.backend.runTask(ctx, j); close(done) }()
	for {
		if _, err = os.Stat(filepath.Join(workspace, "answer-ready")); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("native turn did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	receiverMustStep(t, p)
	input := agentlink.MCPControlDelivery{Version: 1, DeliveryID: "boundary-input", Dispatch: d.Claim.Binding, Binding: original.Admission.Binding, Caller: d.Work.Request.From, InputPhase: "running", Input: &agentlink.MCPTaskInput{RequestID: "next", TaskID: d.Claim.Binding.Task.AgentID, RunID: j.Task.RunID, Text: "native continuation"}}
	cancelControl := agentlink.MCPControlDelivery{Version: 1, DeliveryID: "boundary-cancel", Dispatch: d.Claim.Binding, Binding: original.Admission.Binding, Caller: d.Work.Request.From, Cancel: &agentlink.MCPCancelInput{RequestID: "cancel", TaskID: d.Claim.Binding.Task.AgentID, RunID: j.Task.RunID}}
	inputClaim := agentlink.MCPClaim{ConnectionID: p.identity.ConnectionID(), DeliveryID: input.DeliveryID, ClaimID: "input", Binding: d.Claim.Binding}
	cancelClaim := agentlink.MCPClaim{ConnectionID: p.identity.ConnectionID(), DeliveryID: cancelControl.DeliveryID, ClaimID: "cancel", Binding: d.Claim.Binding}
	remote.queue = append(remote.queue, receiverDelivery{Claim: &inputClaim, Control: &input}, receiverDelivery{Claim: &cancelClaim, Control: &cancelControl})
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	if err = os.WriteFile(filepath.Join(workspace, "complete-turn"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("native turn did not finish")
	}
	receiverMustStep(t, p)
	receiverMustStep(t, p)
	records, err := p.Records(ctx)
	if err != nil || len(records) != 3 {
		t.Fatal(records, err)
	}
	if records[0].LastPhase != "complete" || records[1].Admission == nil || !records[2].ControlApplied || records[2].Admission != nil {
		t.Fatal("ended cancel poisoned native mailbox", records)
	}
	current, err := p.backend.read(ctx, d.Work.Request.From, j.Task.ID)
	if err != nil || current.Task.RunID == j.Task.RunID || current.Task.State != "queued" {
		t.Fatal("old cancel changed native continuation", current, err)
	}
	p.backend.runTask(ctx, current)
	receiverMustStep(t, p)
	current, err = p.backend.read(ctx, d.Work.Request.From, j.Task.ID)
	if err != nil || current.Task.State != "completed" || current.Task.Result != "result: native continuation\n" {
		t.Fatal("native continuation did not execute", current, err)
	}
	// A delayed duplicate delivery is reconciled, never applied to the new run.
	if err = p.accept(ctx, receiverDelivery{Claim: &cancelClaim, Control: &cancelControl}); err != nil {
		t.Fatal(err)
	}
	receiverMustStep(t, p)
}
