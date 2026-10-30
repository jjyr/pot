package inbox

import (
	"database/sql"
	"encoding/json"
	"github.com/jjyr/pot/pkg/agent"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"path/filepath"
	"testing"
)

func TestMessageContentEstimateAtomicAndDurable(t *testing.T) {
	ctx := t.Context()
	path := filepath.Join(t.TempDir(), "tasks.sqlite")
	db, err := storage.OpenDatabase(path, storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	msg := protocol.Envelope{ID: "one", Kind: "message", From: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "sender"}, To: protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "receiver"}, ConversationID: "00000000-0000-4000-8000-000000000001", Text: "中文🙂\n\"quoted\""}
	write := func() error {
		return storage.Transaction(ctx, db, func(tx *sql.Tx) error { return EnqueueTx(ctx, tx, msg, "out", "queued") })
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = EnqueueTx(ctx, tx, msg, "out", "queued"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow("SELECT COUNT(*) FROM agent_messages").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if err = write(); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = storage.OpenDatabase(path, storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if err = write(); err != nil {
		t.Fatal(err)
	}
	var tokens int64
	var method string
	if err = db.QueryRow("SELECT COUNT(*),content_estimated_tokens,content_estimate_method FROM agent_messages").Scan(&count, &tokens, &method); err != nil || count != 1 || tokens != int64(agent.EstimateTextTokens(msg.Text)) || method != agent.ContentEstimateMethod {
		t.Fatal(count, tokens, method, err)
	}
	// A task result has one structured content body, not both the body and Text.
	msg.ID = "result"
	msg.Kind = "task_result"
	msg.From.Kind = "runtime"
	msg.To.NodeID = "local"
	msg.Result = &protocol.TaskResultEvent{TaskID: "task", RunID: "run", Title: "title", Phase: protocol.TaskComplete, Text: "result body", Origin: protocol.TaskOrigin{Kind: "manual"}}
	msg.ConversationID = "run"
	msg.Text = "duplicate display projection"
	if err = write(); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(msg.Result)
	if err = db.QueryRow("SELECT content_estimated_tokens FROM agent_messages WHERE id='result'").Scan(&tokens); err != nil || tokens != int64(agent.EstimateTextTokens(string(body))) {
		t.Fatal(tokens, err)
	}
}
