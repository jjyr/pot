package storage

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestChatReplyLifecycleUpgradePreservesHistoryAndRequestIdentity(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	old := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrationFiles, "migrations/control")
	for _, e := range entries {
		if e.Name() >= "0024_" {
			continue
		}
		name := "migrations/control/" + e.Name()
		data, _ := fs.ReadFile(migrationFiles, name)
		old[name] = &fstest.MapFile{Data: data}
	}
	if err := migrateDatabase(ctx, db, ControlDatabase, old); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO agent_link_chat_sessions VALUES('link','assistant','session');
INSERT INTO agent_link_chat(id,link_id,assistant_id,request_id,role,text,created_at,sender,sender_kind) VALUES('input','link','assistant','request','user','question',1,'session','user'),('output','link','assistant','reply','assistant','answer',2,'','assistant'),('old-input','old-link','assistant','old-request','user','old question',0,'assistant','assistant');`)
	for range 2 {
		if err := Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
		var request, text, state, parent string
		if err := db.QueryRow("SELECT request_id,text,reply_state,reply_to FROM agent_link_chat WHERE id='input'").Scan(&request, &text, &state, &parent); err != nil {
			t.Fatal(err)
		}
		if request != "request" || text != "question" || state != "pending" || parent != "" {
			t.Fatal(request, text, state, parent)
		}
		if err := db.QueryRow("SELECT text FROM agent_link_chat WHERE id='output'").Scan(&text); err != nil || text != "answer" {
			t.Fatal(text, err)
		}
		if err := db.QueryRow("SELECT reply_state FROM agent_link_chat WHERE id='old-input'").Scan(&state); err != nil || state != "unknown" {
			t.Fatal(state, err)
		}
	}
}

func TestChatReplyRouteUpgradePreservesImmutableEnvelopes(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	old := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrationFiles, "migrations/tasks")
	for _, e := range entries {
		if e.Name() >= "0016_" {
			continue
		}
		name := "migrations/tasks/" + e.Name()
		data, _ := fs.ReadFile(migrationFiles, name)
		old[name] = &fstest.MapFile{Data: data}
	}
	if err := migrateDatabase(ctx, db, TasksDatabase, old); err != nil {
		t.Fatal(err)
	}
	envelope := `{"kind":"message","id":"reply","reply_to":"original","text":"answer"}`
	if _, err := db.Exec("INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient)VALUES('reply',?,'out','delivered','conversation','user')", envelope); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(ctx, db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
		var raw, state string
		var count int
		if err := db.QueryRow("SELECT envelope,state FROM agent_messages WHERE id='reply'").Scan(&raw, &state); err != nil || raw != envelope || state != "delivered" {
			t.Fatal(raw, state, err)
		}
		if err := db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type='index' AND name='agent_chat_replies'").Scan(&count); err != nil || count != 1 {
			t.Fatal(count, err)
		}
	}
}
