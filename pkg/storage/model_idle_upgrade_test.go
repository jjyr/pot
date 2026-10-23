package storage

import (
	"context"
	"encoding/json"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/jjyr/pot/pkg/core"
)

func TestModelIdleTimeoutUpgradePreservesContextAndReceipts(t *testing.T) {
	for _, skipped := range []bool{false, true} {
		t.Run(map[bool]string{false: "previous", true: "skipped"}[skipped], func(t *testing.T) {
			db := migrationDB(t)
			ctx := context.Background()
			prior := fstest.MapFS{}
			entries, _ := fs.ReadDir(migrationFiles, "migrations/conversation")
			for _, entry := range entries {
				if entry.Name() >= "0006_" || skipped && entry.Name() >= "0002_" {
					continue
				}
				name := "migrations/conversation/" + entry.Name()
				raw, _ := fs.ReadFile(migrationFiles, name)
				prior[name] = &fstest.MapFile{Data: raw}
			}
			if err := migrateDatabase(ctx, db, ConversationDatabase, prior); err != nil {
				t.Fatal(err)
			}
			settings := core.DefaultAgentSettings()
			settings.RequestTimeoutMS = 180000
			profile := core.Profile{ID: "profile", Revision: 1, Name: "original", Provider: "provider", Model: "model", AuthRef: "provider:provider", Params: core.Parameters{MaxOutputTokens: 100, Agent: settings}, ToolPolicy: core.ToolPolicy{SummaryReadOnly: true, Unattended: "runtime"}}
			spec, err := profile.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			thread, _ := json.Marshal(core.Thread{Owner: "owner", Generation: 7, Config: spec})
			pending, _ := json.Marshal(map[string]any{"revision": 9, "intent": nil, "pending_spec": spec})
			profiles, _ := json.Marshal([]core.Profile{profile})
			for key, value := range map[string]string{
				"thread": string(thread), "assistant.configuration.v1": string(pending), "agent.profiles.v1": string(profiles),
				"thread.context":     `{"type":"context","version":2,"messages":[],"pending":[],"receipts":[{"id":"completed","result":{"exit_code":0}}],"model_request_id":"uncertain-model"}`,
				"conversation.retry": `{"request":{"request_id":"original"},"attempt_id":"attempt"}`,
			} {
				if _, err := db.Exec("INSERT INTO metadata VALUES(?,?)", key, value); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec("INSERT INTO assistant_configuration_requests VALUES('saved','original-digest',?)", string(thread)); err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if err := Migrate(ctx, db, ConversationDatabase); err != nil {
					t.Fatal(err)
				}
				var raw string
				if err := db.QueryRow("SELECT value FROM metadata WHERE key='thread'").Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var migrated core.Thread
				if err := core.DecodeJSON([]byte(raw), &migrated); err != nil || migrated.Config.Validate() != nil || migrated.Generation != 7 || migrated.Owner != "owner" || migrated.Config.Params.Agent.RequestTimeoutMS != 600000 {
					t.Fatal("current thread/hash not upgraded", raw, err)
				}
				if migrated.Config.Model != spec.Model || migrated.Config.Provider != spec.Provider || migrated.Config.SpecHash == spec.SpecHash {
					t.Fatal("timeout upgrade changed model or retained stale hash")
				}
				if err := db.QueryRow("SELECT value FROM metadata WHERE key='assistant.configuration.v1'").Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var state struct {
					Revision uint64            `json:"revision"`
					Intent   json.RawMessage   `json:"intent"`
					Pending  core.ResolvedSpec `json:"pending_spec"`
				}
				if err := core.DecodeJSON([]byte(raw), &state); err != nil || state.Revision != 9 || state.Pending.Validate() != nil || state.Pending.Params.Agent.RequestTimeoutMS != 600000 {
					t.Fatal("pending spec was not upgraded", raw, err)
				}
				var receipt, digest string
				if err := db.QueryRow("SELECT value FROM metadata WHERE key='agent.profiles.v1'").Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var migratedProfiles []core.Profile
				if err := core.DecodeJSON([]byte(raw), &migratedProfiles); err != nil || len(migratedProfiles) != 1 || migratedProfiles[0].Params.Agent.RequestTimeoutMS != 600000 {
					t.Fatal("saved profiles kept the total-request limit", raw, err)
				}
				if err := db.QueryRow("SELECT value FROM metadata WHERE key='conversation.retry'").Scan(&raw); err != nil || raw != `{"request":{"request_id":"original"},"attempt_id":"attempt"}` {
					t.Fatal("retry identity changed", raw, err)
				}
				if err := db.QueryRow("SELECT input_hash,response FROM assistant_configuration_requests WHERE id='saved'").Scan(&digest, &receipt); err != nil || digest != "original-digest" || receipt != string(thread) {
					t.Fatal("historical configuration receipt changed", receipt, err)
				}
				if err := db.QueryRow("SELECT value FROM metadata WHERE key='thread.context'").Scan(&raw); err != nil || raw != `{"type":"context","version":2,"messages":[],"pending":[],"receipts":[{"id":"completed","result":{"exit_code":0}}],"model_request_id":"uncertain-model"}` {
					t.Fatal("uncertain model fence or completed receipts changed", raw, err)
				}
			}
		})
	}
}
