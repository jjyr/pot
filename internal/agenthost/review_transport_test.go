package agenthost

import (
	"context"
	"encoding/json"
	"github.com/jjyr/pot/internal/control"
	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/protocol"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type potReviewView struct {
	Pending []struct {
		ID      string `json:"id"`
		Once    bool   `json:"once_only"`
		Request struct {
			Args map[string]any `json:"args"`
			Task string         `json:"task"`
		} `json:"request"`
	} `json:"pending"`
}

func exercisePotHumanReview(t *testing.T, ctx context.Context, pot *control.Service, h *agentHost, link agentlink.Connection) {
	t.Helper()
	owner, err := pot.Registry.Create(ctx, "task-owner", "Task owner")
	if err != nil {
		t.Fatal(err)
	}
	other, err := pot.Registry.Create(ctx, "other-owner", "Other owner")
	if err != nil {
		t.Fatal(err)
	}
	read := func(id string) potReviewView {
		t.Helper()
		var view potReviewView
		value, err := pot.CallAgent(ctx, id, agentlink.Approvals, agentlink.Call{Action: "read"})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(value)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		if _, found := fields["linked_reviews"]; found {
			t.Fatal("approval API exposed execution history", string(raw))
		}
		if err := json.Unmarshal(raw, &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	a, _ := h.backend.resolve(ctx, "codex")
	marker := filepath.Join(a.Workspace, "approval-executed")
	facts, stopFacts := pot.Lifecycle.Subscribe()
	defer stopFacts()
	task, err := pot.CreateTask(ctx, owner.ID, protocol.CreateTask{Target: &protocol.AgentRef{NodeID: h.identity.Address.NodeID, Kind: "node", AgentID: h.identity.Address.NodeID}, RequestID: "direct-task", Text: "approve\nReview this operation"})
	if err != nil {
		t.Fatal(err)
	}
	var approvalID string
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		view := read(owner.ID)
		if len(view.Pending) == 1 {
			item := view.Pending[0]
			approvalID = item.ID
			if item.Request.Task != task.ID || !item.Once || item.Request.Args["cmd"] != "untrusted" {
				t.Fatal(view)
			}
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if approvalID == "" {
		t.Fatal("task approval did not reach its owner", read(owner.ID))
	}
	timer := time.NewTimer(testutil.Timeout(t, 5*time.Second))
	defer timer.Stop()
	for received := false; !received; {
		select {
		case fact := <-facts:
			if fact.Kind == "approval_requested" {
				if fact.ID != approvalID || fact.Owner != owner.ID || fact.Task != task.ID || fact.Preview != "" {
					t.Fatal("unscoped approval fact", fact)
				}
				received = true
			}
		case <-timer.C:
			t.Fatal("committed peer approval produced no direct business event")
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("executed before approval")
	}
	body, _ := json.Marshal(map[string]string{"id": approvalID, "action": "allow_once"})
	if _, err := pot.CallAgent(ctx, other.ID, agentlink.Approvals, agentlink.Call{Action: "write", Body: body}); err == nil {
		t.Fatal("another assistant approved task")
	}
	if _, err := pot.CallAgent(ctx, owner.ID, agentlink.Approvals, agentlink.Call{Action: "write", Body: body}); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("approved command did not run", err)
	}
	if _, err := pot.CallAgent(ctx, owner.ID, agentlink.Approvals, agentlink.Call{Action: "write", Body: body}); err == nil {
		t.Fatal("stale approval accepted")
	}
	// A completed task must produce a real durable outcome notification, not only
	// a readable backend state. Continuation produces a second notification.
	from := protocol.AgentRef{NodeID: link.Local.Address.NodeID, Kind: "assistant", AgentID: owner.ID}
	remoteID := jobID(from, "direct-task")
	awaitTask(t, h.backend, from, remoteID, "completed")
	assertNotifications := func(want int) {
		t.Helper()
		until := time.Now().Add(15 * time.Second)
		for time.Now().Before(until) {
			var count int
			pot.Tasks.DB().QueryRow(`SELECT count(*) FROM agent_messages WHERE json_extract(envelope,'$.kind')='task_result' AND json_extract(envelope,'$.result.task_id')=?`, task.ID).Scan(&count)
			if count == want {
				return
			}
			time.Sleep(300 * time.Millisecond)
		}
		t.Fatal("task outcome was not delivered", want)
	}
	assertNotifications(1)
	firstRun, _ := h.backend.read(ctx, from, remoteID)
	guidance, _ := json.Marshal(protocol.TaskInput{RequestID: "continue-task", Text: "continue"})
	if _, err := pot.CallAgent(ctx, owner.ID, agentlink.TaskInput, agentlink.Call{ID: task.ID, Action: "write", Body: guidance}); err != nil {
		t.Fatal(err)
	}
	awaitTask(t, h.backend, from, remoteID, "completed")
	continued, _ := h.backend.read(ctx, from, remoteID)
	if firstRun.ThreadID != continued.ThreadID || firstRun.Task.RunID == continued.Task.RunID {
		t.Fatal("continuation lost session/run binding")
	}
	assertNotifications(2)
	// Assistant messages use the receiver's own policy and never create user approvals.
	_ = os.Remove(marker)
	envelope, err := pot.Mailbox.Send(ctx, "peer-message", from, protocol.SendMail{To: h.backend.identity(a).Address, Text: "approve"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	awaitTask(t, h.backend, from, jobID(from, "mail:"+envelope.ID), "failed")
	j, _ := h.backend.read(ctx, from, jobID(from, "mail:"+envelope.ID))
	if j.Task.Error != "codex_approval_denied" {
		t.Fatal(j)
	}
	view := read(owner.ID)
	if len(view.Pending) != 0 {
		t.Fatal("peer message exposed approval", view)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("denied message executed")
	}
	if err := h.backend.publishMessages(ctx); err != nil {
		t.Fatal(err)
	}
	var replies int
	h.tasks.QueryRow(`SELECT count(*) FROM agent_messages WHERE direction='outgoing' AND json_extract(envelope,'$.reply_to')=?`, envelope.ID).Scan(&replies)
	if replies != 1 {
		t.Fatal("denial did not produce an original-caller reply", replies)
	}
}

func TestAutomaticReviewDoesNotPublishApprovalHistory(t *testing.T) {
	ctx, pot, h, link := linkedPot(t)
	owner, err := pot.Registry.Create(ctx, "automatic-owner", "Automatic owner")
	if err != nil {
		t.Fatal(err)
	}
	// Automatic review stays on Codex. No manual operation or history is
	// exposed by the receiver review endpoint or Pot's existing approval API.
	automatic, err := pot.CreateTask(ctx, owner.ID, protocol.CreateTask{Target: &protocol.AgentRef{NodeID: h.identity.Address.NodeID, Kind: "node", AgentID: h.identity.Address.NodeID}, RequestID: "automatic-task", Text: "auto-review\nAn operation handled by Codex's reviewer"})
	if err != nil {
		t.Fatal(err)
	}
	fromAuto := protocol.AgentRef{NodeID: link.Local.Address.NodeID, Kind: "assistant", AgentID: owner.ID}
	awaitTask(t, h.backend, fromAuto, jobID(fromAuto, "automatic-task"), "completed")
	value, err := pot.CallAgent(ctx, owner.ID, agentlink.Approvals, agentlink.Call{Action: "read"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	var view potReviewView
	if err := json.Unmarshal(raw, &view); err != nil {
		t.Fatal(err)
	}
	if len(view.Pending) != 0 {
		t.Fatal("automatic review became a user approval", view, automatic)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	if _, found := fields["linked_reviews"]; found {
		t.Fatal("approval API exposed execution history", string(raw))
	}
	remoteReview, err := h.backend.Review(ctx, reviewQuery(fromAuto))
	if err != nil {
		t.Fatal(err)
	}
	rawReview, _ := json.Marshal(remoteReview)
	var reviewFields map[string]json.RawMessage
	_ = json.Unmarshal(rawReview, &reviewFields)
	for _, field := range []string{"work", "can_decide"} {
		if _, found := reviewFields[field]; found {
			t.Fatal("Agent Link exposed obsolete review state", field, string(rawReview))
		}
	}
}
