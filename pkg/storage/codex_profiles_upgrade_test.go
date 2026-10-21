package storage

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/jjyr/pot/pkg/assistantavatar"
)

func TestCodexProfilesUpgradeKeepsSessionAndAssignedAvatar(t *testing.T) {
	db := migrationDB(t)
	prior := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrationFiles, "migrations/tasks")
	for _, entry := range entries {
		if entry.Name() >= "0016_" {
			continue
		}
		path := "migrations/tasks/" + entry.Name()
		raw, _ := fs.ReadFile(migrationFiles, path)
		prior[path] = &fstest.MapFile{Data: raw}
	}
	ctx := context.Background()
	if err := migrateDatabase(ctx, db, TasksDatabase, prior); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO codex_link_assistants(id,name,workspace,mode,thread_id,is_default) VALUES('codex','My renamed assistant','/private/work','read-only','persistent-thread',1)`)
	execMigrationSQL(t, db, `INSERT INTO codex_link_jobs(id,caller,request_id,assistant_id,payload,data) VALUES('job','caller','request','codex','immutable','{"task":{"state":"queued"}}')`)
	for range 2 {
		if err := Migrate(ctx, db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
		var name, workspace, mode, thread, avatar, character, payload, data string
		var revision, def int
		if err := db.QueryRow("SELECT name,workspace,mode,thread_id,avatar,character,revision,is_default FROM codex_link_assistants").Scan(&name, &workspace, &mode, &thread, &avatar, &character, &revision, &def); err != nil {
			t.Fatal(err)
		}
		want, _ := assistantavatar.Render(assistantavatar.ForName(name, "codex"), "codex")
		if name != "My renamed assistant" || workspace != "/private/work" || mode != "read-only" || thread != "persistent-thread" || avatar != want || character == "" || revision != 1 || def != 1 {
			t.Fatal("upgrade changed session or omitted artwork")
		}
		if err := db.QueryRow("SELECT payload,data FROM codex_link_jobs").Scan(&payload, &data); err != nil || payload != "immutable" || data != `{"task":{"state":"queued"}}` {
			t.Fatal("migration touched accepted work", payload, data, err)
		}
	}
}

func TestDirectoryAvatarUpgradePreservesRoutesAndCustomImages(t *testing.T) {
	db := migrationDB(t)
	prior := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrationFiles, "migrations/control")
	for _, entry := range entries {
		if entry.Name() >= "0024_" {
			continue
		}
		path := "migrations/control/" + entry.Name()
		raw, _ := fs.ReadFile(migrationFiles, path)
		prior[path] = &fstest.MapFile{Data: raw}
	}
	ctx := context.Background()
	if err := migrateDatabase(ctx, db, ControlDatabase, prior); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO agent_link_connections(id,route,state,metadata,secret_ref,peer_key) VALUES('link','route','active','{"directory":{"assistants":[{"name":"Saved","address":{"agent_id":"first"}},{"name":"Custom","avatar":"custom-image","address":{"agent_id":"second"}}]},"peer_host":{"node_id":"pinned-node"}}','encrypted-secret','pinned-key')`)
	for range 2 {
		if err := Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var first, second, node, secret, key, route string
	err := db.QueryRow(`SELECT json_extract(metadata,'$.directory.assistants[0].avatar'),json_extract(metadata,'$.directory.assistants[1].avatar'),json_extract(metadata,'$.peer_host.node_id'),secret_ref,peer_key,route FROM agent_link_connections`).Scan(&first, &second, &node, &secret, &key, &route)
	if err != nil || first != "" || second != "custom-image" || node != "pinned-node" || secret != "encrypted-secret" || key != "pinned-key" || route != "route" {
		t.Fatal(first, second, node, secret, key, route, err)
	}
}
