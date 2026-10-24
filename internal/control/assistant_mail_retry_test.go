package control

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"

	"github.com/jjyr/pot/internal/testutil"
	"github.com/jjyr/pot/internal/usage"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/execution"
	"github.com/jjyr/pot/pkg/storage"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/modelprotocol"
	"github.com/jjyr/pot/pkg/protocol"
)

// Exercise the actual mailbox -> Report -> model -> product dispatch path. A
// confirmed model failure can be continued without replaying completed tools.
func TestAssistantMailContinuationRestoresOriginalDelivery(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 15*time.Second))
	defer cancel()
	var calls atomic.Int32
	var peerID string
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		switch calls.Add(1) {
		case 1:
			http.Error(w, "controlled model failure", http.StatusBadRequest)
		case 2:
			args, _ := json.Marshal(protocol.SendMail{To: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: peerID}, Text: "reply after continuation"})
			event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "original-reply", "type": "function", "function": map[string]any{"name": "send_message", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}})
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: "+string(event)+"\n\ndata: [DONE]\n\n")
		default:
			batchReply(w, "done")
		}
	}, 2)
	peer, err := s.Registry.Create(ctx, "peer", "Peer")
	if err != nil {
		t.Fatal(err)
	}
	peerID = peer.ID
	c, _ := s.Conversation(ctx, "main")
	incoming, err := s.Mailbox.Send(ctx, "incoming", protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: peerID}, protocol.SendMail{To: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "main"}, Text: "request"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	failed := awaitConversation(t, c, func(p protocol.Snapshot) bool { return p.Turn == nil && p.Continuation != nil })
	if err := s.Mailbox.Processing(ctx, incoming); err == nil {
		t.Fatal("failed delivery remained processing")
	}
	q := ContinueMainCommand{RequestID: "continue-original", UserMessageID: failed.Continuation.UserMessageID, AttemptID: failed.Continuation.AttemptID}
	if _, err = s.ContinueMain(ctx, "main", q); err != nil {
		t.Fatal(err)
	}
	done := awaitConversation(t, c, func(p protocol.Snapshot) bool { return p.Turn == nil && p.Continuation == nil })
	var sent bool
	for _, m := range done.Messages {
		if m.Tool != nil && m.Tool.Name == "send_message" {
			sent = true
			if m.Tool.Phase != "completed" {
				t.Fatalf("resumed mail send failed: %+v", m.Tool)
			}
		}
	}
	if !sent {
		t.Fatal("model did not dispatch a reply")
	}
	history, err := s.Mailbox.History(ctx, "main", 0)
	if err != nil || len(history) != 2 {
		t.Fatal(history, err)
	}
	reply := history[1].Message
	if reply.ReplyTo == nil || *reply.ReplyTo != incoming.ID || reply.ConversationID != incoming.ConversationID || reply.From.AgentID != "main" {
		t.Fatal("reply binding changed", reply)
	}
	if _, err = s.ContinueMain(ctx, "main", q); err != nil {
		t.Fatal("continuation receipt lost", err)
	}
	// A late call from the completed attempt cannot mint another message.
	thread, _ := c.Thread(ctx)
	dispatch := s.productDispatch("main", "pot-result-"+incoming.ID, thread.Generation, false)["send_message"]
	args, _ := json.Marshal(protocol.SendMail{To: reply.To, Text: "late"})
	if _, err = dispatch(ctx, core.ExecutionContext{Owner: "main"}, modelprotocol.ToolCall{ID: "late", Name: "send_message", Arguments: args}, nil); err == nil {
		t.Fatal("retired incoming attempt sent new mail")
	}
	// A cancelled dispatch remains cancelled even after a verified retry restored
	// the parent, rather than treating its old generation as renewed authority.
	stale, stop := context.WithCancel(ctx)
	stop()
	if _, err = dispatch(stale, core.ExecutionContext{Owner: "main"}, modelprotocol.ToolCall{ID: "cancelled", Name: "send_message", Arguments: args}, nil); err == nil {
		t.Fatal("cancelled dispatch sent")
	}
	history, _ = s.Mailbox.History(ctx, "main", 0)
	if len(history) != 2 {
		t.Fatal("duplicate or stale send persisted", len(history))
	}
}

func TestFailedAssistantMailDoesNotStarveLaterConversation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 10*time.Second))
	defer cancel()
	var calls atomic.Int32
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "confirmed model rejection", 400)
			return
		}
		batchReply(w, "new correspondence handled")
	}, 2)
	peer, err := s.Registry.Create(ctx, "peer", "Peer")
	if err != nil {
		t.Fatal(err)
	}
	c, _ := s.Conversation(ctx, "main")
	send := s.productDispatch(peer.ID, "user-turn", 1, false)["send_message"]
	args, _ := json.Marshal(protocol.SendMail{To: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "main"}, Text: "new request"})
	first, err := send(ctx, core.ExecutionContext{Owner: peer.ID}, modelprotocol.ToolCall{ID: "first", Name: "send_message", Arguments: args}, nil)
	if err != nil {
		t.Fatal(err)
	}
	awaitConversation(t, c, func(p protocol.Snapshot) bool { return p.Turn == nil && p.Continuation != nil })
	var old protocol.Envelope
	json.Unmarshal(first, &old)
	second, err := send(ctx, core.ExecutionContext{Owner: peer.ID}, modelprotocol.ToolCall{ID: "second", Name: "send_message", Arguments: args}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var next protocol.Envelope
	json.Unmarshal(second, &next)
	if next.ID == old.ID || next.ConversationID == old.ConversationID {
		t.Fatal("new request lost identity")
	}
	sub := s.Mailbox.Changes.Subscribe()
	defer sub.Close()
	for {
		history, e := s.Mailbox.History(ctx, "main", 0)
		if e != nil {
			t.Fatal(e)
		}
		if len(history) == 2 && history[0].State == "failed" && history[1].State == "processed" {
			break
		}
		if _, e = sub.Next(ctx); e != nil {
			t.Fatal("confirmed failure starved later accepted message", history, e)
		}
	}
	frame, err := c.UIFrame(ctx, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, m := range frame.Messages {
		for _, e := range m.Correspondence {
			if e.ID == next.ID {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("processed inbox item did not enter receive-message UI projection")
	}
	if calls.Load() != 2 {
		t.Fatal("failed mail replayed", calls.Load())
	}
}

func TestAcceptedSendSurvivesTranscriptWriteFailureWithoutResend(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 10*time.Second))
	defer cancel()
	var calls atomic.Int32
	var peerID string
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			args, _ := json.Marshal(protocol.SendMail{To: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: peerID}, Text: "accepted once"})
			event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "stable-send", "type": "function", "function": map[string]any{"name": "send_message", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}})
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: "+string(event)+"\n\ndata: [DONE]\n\n")
			return
		}
		batchReply(w, "continued from original accepted receipt")
	}, 2)
	peer, err := s.Registry.Create(ctx, "peer", "Peer")
	if err != nil {
		t.Fatal(err)
	}
	peerID = peer.ID
	c, _ := s.Conversation(ctx, "main")
	db, err := storage.OpenDatabase(filepath.Join(s.options.Root, "assistants", "main", "conversation.sqlite"), storage.ConversationDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`CREATE TRIGGER fail_send_transcript BEFORE INSERT ON events WHEN json_extract(NEW.body,'$.message.tool.name')='send_message' AND json_extract(NEW.body,'$.message.tool.phase')='completed' BEGIN SELECT RAISE(FAIL,'controlled transcript failure'); END`); err != nil {
		t.Fatal(err)
	}
	accepted, err := c.Send(ctx, protocol.SendMessage{RequestID: "original", ConversationID: "main", Text: "send once"})
	if err != nil {
		t.Fatal(err)
	}
	failed := awaitConversation(t, c, func(p protocol.Snapshot) bool { return p.Turn == nil && p.Continuation != nil })
	var raw string
	if err = db.QueryRow("SELECT value FROM metadata WHERE key='thread.context'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var checkpoint agent.Checkpoint
	if err = json.Unmarshal([]byte(raw), &checkpoint); err != nil {
		t.Fatal(err)
	}
	if len(checkpoint.Receipts) != 1 || checkpoint.Started != nil {
		t.Fatal("committed send receipt was not checkpointed", checkpoint.Started, len(checkpoint.Receipts))
	}
	var receipt protocol.Envelope
	if err = json.Unmarshal(checkpoint.Receipts[0].Result, &receipt); err != nil || receipt.ID == "" {
		t.Fatal("accepted receipt lost", err)
	}
	history, err := s.Mailbox.History(ctx, "main", 0)
	if err != nil || len(history) != 1 || history[0].Message.ID != receipt.ID {
		t.Fatal("receipt differs from durable inbox", history, err)
	}
	if _, err = db.Exec("DROP TRIGGER fail_send_transcript"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ContinueMain(ctx, "main", ContinueMainCommand{RequestID: "continue", UserMessageID: accepted.MessageID, AttemptID: failed.Continuation.AttemptID}); err != nil {
		t.Fatal(err)
	}
	awaitConversation(t, c, func(p protocol.Snapshot) bool { return p.Turn == nil && p.Continuation == nil })
	history, _ = s.Mailbox.History(ctx, "main", 0)
	if len(history) != 1 || history[0].Message.ID != receipt.ID || calls.Load() != 2 {
		t.Fatal("transcript failure resent accepted message", len(history), calls.Load())
	}
}

func TestAssistantMailAllowanceResumeAfterRestartRestoresDelivery(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), testutil.Timeout(t, 15*time.Second))
	defer cancel()
	var calls atomic.Int32
	var peerID string
	s, _ := batchService(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			args, _ := json.Marshal(protocol.SendMail{To: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: peerID}, Text: "reply after allowance restart"})
			event, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "allowance-reply", "type": "function", "function": map[string]any{"name": "send_message", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}})
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: "+string(event)+"\n\ndata: [DONE]\n\n")
			return
		}
		batchReply(w, "done")
	}, 2)
	peer, err := s.Registry.Create(ctx, "peer", "Peer")
	if err != nil {
		t.Fatal(err)
	}
	peerID = peer.ID
	if err = s.Usage.SetPolicy(ctx, usage.Policy{Period: "day", Limit: 1, Timezone: "UTC", AutoResume: false}); err != nil {
		t.Fatal(err)
	}
	c, _ := s.Conversation(ctx, "main")
	incoming, err := s.Mailbox.Send(ctx, "allowance-incoming", protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: peerID}, protocol.SendMail{To: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "main"}, Text: "request"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	awaitConversation(t, c, func(p protocol.Snapshot) bool { return p.Turn != nil && p.Turn.State == "waiting" })
	waiting, err := s.Usage.Waiting(ctx, "main")
	if err != nil || waiting == nil {
		t.Fatal(waiting, err)
	}
	if calls.Load() != 0 {
		t.Fatal("allowance wait invoked model")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	manager, err := execution.New("local", "reboot", execution.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	options := s.options
	options.Manager = manager
	options.BootID = "reboot"
	s, err = Open(options)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	defer manager.Stop()
	if err = s.Mailbox.Processing(ctx, incoming); err == nil {
		t.Fatal("restart did not fence old mail attempt")
	}
	if err = s.Usage.SetPolicy(ctx, usage.Policy{Period: "day", Limit: 100000, Timezone: "UTC", AutoResume: false}); err != nil {
		t.Fatal(err)
	}
	c, _ = s.Conversation(ctx, "main")
	q := ResumeAllowanceCommand{RequestID: "resume-allowance", Turn: waiting.Turn, Since: waiting.Since}
	if err = s.ResumeAllowance(ctx, "main", q); err != nil {
		t.Fatal(err)
	}
	done := awaitConversation(t, c, func(p protocol.Snapshot) bool { return p.Turn == nil })
	for _, m := range done.Messages {
		if m.Tool != nil && m.Tool.Name == "send_message" && m.Tool.Phase != "completed" {
			t.Fatal("restarted allowance send fenced", m.Tool)
		}
	}
	history, err := s.Mailbox.History(ctx, "main", 0)
	if err != nil || len(history) != 2 || history[0].State != "processed" || history[1].Message.ReplyTo == nil || *history[1].Message.ReplyTo != incoming.ID {
		t.Fatal(history, err)
	}
	if err = s.ResumeAllowance(ctx, "main", q); err != nil {
		t.Fatal("allowance receipt not idempotent", err)
	}
	if calls.Load() != 2 {
		t.Fatal("allowance resume replayed model", calls.Load())
	}
}
