package tasks

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/jjyr/pot/pkg/protocol"
)

func TestForwardedPeerMessageRemainsReferenceAcrossLongChains(t *testing.T) {
	ctx := context.Background()
	s, _, _ := fixture(t)
	record, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "create", Text: "work"})
	if err != nil {
		t.Fatal(err)
	}
	sender := protocol.AgentRef{NodeID: s.NodeID(), Kind: "assistant", AgentID: "assistant"}
	parent := protocol.Envelope{ID: "peer-parent", Kind: "message", From: protocol.AgentRef{NodeID: s.NodeID(), Kind: "assistant", AgentID: "peer"}, To: sender, ConversationID: record.RunID, Text: "reference", Hops: 130}
	if err = s.SendMessageWithParent(ctx, "assistant", record.Info.ID, "reference", sender, "forwarded data", &parent); err != nil {
		t.Fatal(err)
	}
	inputs, err := s.Inputs(ctx, "assistant", record.Info.ID)
	if err != nil || len(inputs) != 2 || inputs[1].Kind != "reference" {
		t.Fatal("peer content became owner guidance", inputs, err)
	}
	var body []byte
	if err = s.DB().QueryRow("SELECT envelope FROM agent_messages WHERE json_extract(envelope,'$.request_id')='reference'").Scan(&body); err != nil {
		t.Fatal(err)
	}
	var stored protocol.Envelope
	if err = json.Unmarshal(body, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.From != sender || stored.Hops != 131 || stored.ReplyTo == nil || *stored.ReplyTo != parent.ID {
		t.Fatal("message provenance or causation was lost", stored)
	}
}

func TestNodeIdentityUpgradeRetainsInputHashWithoutStoredParent(t *testing.T) {
	ctx := context.Background()
	s, _, path := fixture(t)
	record, err := s.Create(ctx, "assistant", protocol.CreateTask{RequestID: "create-upgrade", Text: "original work"})
	if err != nil {
		t.Fatal(err)
	}
	sender := protocol.AgentRef{NodeID: s.NodeID(), Kind: "assistant", AgentID: "assistant"}
	parent := protocol.Envelope{ID: "external-parent", Kind: "message", From: protocol.AgentRef{NodeID: "remote", Kind: "assistant", AgentID: "alice"}, To: sender, ConversationID: record.RunID, Text: "parent content", Hops: 2}
	if err = s.SendMessageWithParent(ctx, "assistant", record.Info.ID, "retained-request", sender, "reference text", &parent); err != nil {
		t.Fatal(err)
	}
	legacyHash, err := inputDigest(1, "reference text", "reference", sender, &parent)
	if err != nil {
		t.Fatal(err)
	}
	// This private fixture represents sqlite before migration 0006. The
	// parent is deliberately absent; its complete content cannot be reconstructed.
	_, err = s.DB().Exec(`UPDATE agent_messages SET envelope=replace(envelope,'"node_id":','"control_id":');
UPDATE agent_messages SET envelope=json_set(envelope,'$.digest',?) WHERE json_extract(envelope,'$.request_id')='retained-request';
UPDATE tasks SET body=replace(json_set(body,'$.schema_version',9),'"node_id":','"control_id":');
DROP INDEX agent_messages_outbox_route; ALTER TABLE agent_messages DROP COLUMN accepted_at_ms; ALTER TABLE assistant_mail_identity RENAME COLUMN node_id TO control_id;
ALTER TABLE assistant_contacts RENAME COLUMN node TO control;
DROP TABLE plugin_background_writes; DROP TABLE task_input_digest_formats; DROP TABLE codex_task_inputs; DROP TABLE codex_task_sessions; DROP TABLE codex_task_executor; DROP TABLE codex_link_jobs; DROP TABLE codex_link_assistants; DROP INDEX agent_link_user_chat_routes; DROP INDEX agent_chat_replies;
ALTER TABLE plugin_background DROP COLUMN diagnostic;
DROP INDEX agent_plugin_instance_state;
ALTER TABLE plugin_background DROP COLUMN plugin_id;
ALTER TABLE plugin_background DROP COLUMN connection_id;
DROP TABLE run_workspaces; DROP TABLE task_runs; DROP TABLE task_events; DROP TABLE retired_execution_receipts; DROP TABLE journal_file_import; DROP TABLE task_node; ALTER TABLE retired_task_authority RENAME TO task_authority; DELETE FROM pot_migrations WHERE id >= '0006_';`, legacyHash)
	if err != nil {
		t.Fatal(err)
	}
	options := s.options
	s.Close()
	for range 2 {
		s, err = Open(path, options)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.SendMessageWithParent(ctx, "assistant", record.Info.ID, "retained-request", sender, "reference text", &parent); err != nil {
			t.Fatal("same original request lost its exact content receipt", err)
		}
		for _, change := range []string{"text", "parent", "sender"} {
			altered := parent
			from := sender
			text := "reference text"
			switch change {
			case "text":
				text = "changed reference"
			case "parent":
				altered.Text = "changed parent"
			case "sender":
				from.AgentID = "other"
				altered.To = from
			}
			if err = s.SendMessageWithParent(ctx, "assistant", record.Info.ID, "retained-request", from, text, &altered); err == nil {
				t.Fatal("upgraded receipt admitted conflicting " + change)
			}
		}
		var hash string
		var format, count int
		if err = s.DB().QueryRow(`SELECT json_extract(envelope,'$.digest'),f.format FROM agent_messages m JOIN task_input_digest_formats f ON f.message=m.id WHERE json_extract(envelope,'$.request_id')='retained-request'`).Scan(&hash, &format); err != nil || hash != legacyHash || format != 1 {
			t.Fatal(hash, format, err)
		}
		if err = s.DB().QueryRow("SELECT COUNT(*) FROM agent_messages WHERE id='external-parent'").Scan(&count); err != nil || count != 0 {
			t.Fatal("fixture unexpectedly persisted parent", count, err)
		}
		s.Close()
	}
}
