package inbox

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
	"path/filepath"
	"testing"
)

func TestOutboxStatsFullRouteAndImmutableAcceptanceTime(t *testing.T) {
	ctx := context.Background()
	db, err := storage.OpenDatabase(filepath.Join(t.TempDir(), "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	message := protocol.Envelope{Kind: "message", ID: "message", From: protocol.AgentRef{NodeID: "local", Kind: "assistant", AgentID: "main"}, To: protocol.AgentRef{NodeID: "peer", Kind: "assistant", AgentID: "main"}, ConversationID: "00000000-0000-4000-8000-000000000001", Text: "private input"}
	write := func() {
		t.Helper()
		err := storage.Transaction(ctx, db, func(tx *sql.Tx) error { return EnqueueTx(ctx, tx, message, "out", "queued") })
		if err != nil {
			t.Fatal(err)
		}
	}
	write()
	var first int64
	if err = db.QueryRow("SELECT accepted_at_ms FROM agent_messages").Scan(&first); err != nil || first <= 0 {
		t.Fatal(first, err)
	}
	if _, err = db.Exec("UPDATE agent_messages SET accepted_at_ms=1234"); err != nil {
		t.Fatal(err)
	}
	write()
	count, at, err := OutboxStats(ctx, db, "out", "local", "peer")
	if err != nil || count != 1 || at != 1234 {
		t.Fatal(count, at, err)
	}
	for i := 0; i < 40; i++ {
		if _, err = db.Exec("INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient,accepted_at_ms)VALUES(?,?,'out','queued','conversation','main',2000)", fmt.Sprintf("extra-%d", i), string(mustJSON(message))); err != nil {
			t.Fatal(err)
		}
	}
	count, _, err = OutboxStats(ctx, db, "out", "local", "peer")
	if err != nil || count != 41 {
		t.Fatal(count, err)
	}
	count, _, err = OutboxStats(ctx, db, "out", "local", "other")
	if err != nil || count != 0 {
		t.Fatal(count, err)
	}
	if _, err = db.Exec("UPDATE agent_messages SET accepted_at_ms=0 WHERE id='extra-0'"); err != nil {
		t.Fatal(err)
	}
	_, at, err = OutboxStats(ctx, db, "out", "local", "peer")
	if err != nil || at != 0 {
		t.Fatal(at, err)
	}
	if _, err = db.Exec("UPDATE agent_messages SET state='delivered'"); err != nil {
		t.Fatal(err)
	}
	count, at, err = OutboxStats(ctx, db, "out", "local", "peer")
	if err != nil || count != 0 || at != 0 {
		t.Fatal(count, at, err)
	}
}

func mustJSON(value any) []byte {
	raw, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return raw
}
