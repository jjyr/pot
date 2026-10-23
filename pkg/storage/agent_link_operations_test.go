package storage

import (
	"context"
	"encoding/json"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAgentLinkOperationsUpgradePreservesReceiptsAndRestrictsCapabilities(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	prior := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrationFiles, "migrations/control")
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "0022_") {
			break
		}
		path := "migrations/control/" + entry.Name()
		raw, _ := fs.ReadFile(migrationFiles, path)
		prior[path] = &fstest.MapFile{Data: raw}
	}
	if err := migrateDatabase(ctx, db, ControlDatabase, prior); err != nil {
		t.Fatal(err)
	}
	// Use the existing connection schema through its typed storage columns.
	var schema string
	if err := db.QueryRow("SELECT sql FROM sqlite_master WHERE name='agent_link_connections'").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	metadata := `{"id":"link","state":"active","peer_host":{"node_id":"peer","public_key":"pin"},"directory":{"node_id":"peer","capabilities":["chat"],"assistants":[{"address":{"node_id":"peer","kind":"assistant","agent_id":"agent"}}]}}`
	_, err := db.Exec("INSERT INTO agent_link_connections(id,route,state,metadata,secret_ref,peer_key) VALUES('link','local-peer','active',?,'secret','hash')", metadata)
	if err != nil {
		t.Fatal(schema, err)
	}
	if _, err = db.Exec("INSERT INTO agent_link_received_tasks(id,caller,request_id,assistant_id,payload,task_id)VALUES('accepted','caller','request','removed-assistant','immutable-request','original-task')"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var raw string
	if err = db.QueryRow("SELECT metadata FROM agent_link_connections WHERE id='link'").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		ID, State string
		Peer      map[string]any `json:"peer_host"`
		Directory struct {
			Capabilities json.RawMessage
			Targets      []struct {
				Capabilities struct {
					Version    int
					Scope      string
					Operations []string
				}
			}
		}
	}
	if err = json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Directory.Targets) != 1 {
		t.Fatal(raw)
	}
	ops := decoded.Directory.Targets[0].Capabilities
	if decoded.ID != "link" || decoded.State != "active" || decoded.Peer["public_key"] != "pin" || len(decoded.Directory.Capabilities) != 0 || ops.Version != 1 || ops.Scope != "peer" || strings.Join(ops.Operations, ",") != "chat.read,chat.send,chat.receipt" {
		t.Fatal(raw)
	}
	var owner, payload, task string
	if err = db.QueryRow("SELECT assistant_id,payload,task_id FROM agent_link_received_tasks WHERE id='accepted'").Scan(&owner, &payload, &task); err != nil || owner != "removed-assistant" || payload != "immutable-request" || task != "original-task" {
		t.Fatal(owner, payload, task, err)
	}
}
