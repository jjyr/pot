package storage

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestBackgroundInstanceUpgradePreservesAcceptedReceipts(t *testing.T) {
	ctx := context.Background()
	db := migrationDB(t)
	previous := fstest.MapFS{}
	entries, err := fs.ReadDir(migrationFiles, "migrations/tasks")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "0009_") {
			continue
		}
		path := "migrations/tasks/" + e.Name()
		raw, err := fs.ReadFile(migrationFiles, path)
		if err != nil {
			t.Fatal(err)
		}
		previous[path] = &fstest.MapFile{Data: raw}
	}
	if err = migrateDatabase(ctx, db, TasksDatabase, previous); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO plugin_background(id,enabled,assistant,config,revision,state,next,status,conversation,diagnostic) VALUES('github',0,'main','{}',9,'{"cursor":"keep"}',9000,'disabled','kept-conversation','keep');
 INSERT INTO plugin_background(id,enabled,assistant,config,revision,state,next,status,conversation,diagnostic) VALUES('codex',1,'main','{}',3,'null',5000,'ready','codex-conversation','');
 INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient,detail) VALUES('kept-id','{"kind":"plugin_event","plugin":{"identity":{"id":"github","version":"1","digest":"installed"},"event":{"id":"upstream","body":"keep","title":"title","occurred_at":"2026-01-01T00:00:00Z"}},"from":{"node_id":"local","kind":"runtime","agent_id":"plugin-github"},"to":{"node_id":"local","kind":"assistant","agent_id":"main"},"conversation_id":"kept-conversation","text":""}','in','interrupted','kept-conversation','main','uncertain');`)
	for range 2 {
		if err = Migrate(ctx, db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var pluginID, connectionID, checkpoint, conversation, diagnostic string
	var revision, next int
	var enabled bool
	if err = db.QueryRow(`SELECT plugin_id,connection_id,enabled,revision,state,next,conversation,diagnostic FROM plugin_background WHERE id='github'`).Scan(&pluginID, &connectionID, &enabled, &revision, &checkpoint, &next, &conversation, &diagnostic); err != nil {
		t.Fatal(err)
	}
	if pluginID != "github" || connectionID != "" || enabled || revision != 9 || checkpoint != `{"cursor":"keep"}` || next != 9000 || conversation != "kept-conversation" || diagnostic != "keep" {
		t.Fatal(pluginID, connectionID, enabled, revision, checkpoint, next, conversation, diagnostic)
	}
	var id, instance, state, detail, sender, body string
	if err = db.QueryRow(`SELECT id,json_extract(envelope,'$.plugin.instance_id'),state,detail,json_extract(envelope,'$.from.agent_id'),json_extract(envelope,'$.plugin.event.body') FROM agent_messages`).Scan(&id, &instance, &state, &detail, &sender, &body); err != nil {
		t.Fatal(err)
	}
	if id != "kept-id" || instance != "github" || state != "interrupted" || detail != "uncertain" || sender != "plugin-github" || body != "keep" {
		t.Fatal(id, instance, state, detail, sender, body)
	}
	if err = db.QueryRow(`SELECT plugin_id FROM plugin_background WHERE id='codex'`).Scan(&pluginID); err != nil || pluginID != "codex" {
		t.Fatal(pluginID, err)
	}
}
