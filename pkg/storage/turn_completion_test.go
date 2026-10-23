package storage

import (
	"context"
	"testing"
	"testing/fstest"
)

func TestTurnCompletionUpgradePreservesExistingTurn(t *testing.T) {
	baseline, err := migrationFiles.ReadFile("migrations/conversation/0001_initial_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range []string{"null", `{"turn":{"request":{"request_id":"original"}},"phase":"succeeded","error":""}`} {
		t.Run(receipt, func(t *testing.T) {
			db := migrationDB(t)
			ctx := context.Background()
			files := fstest.MapFS{"migrations/conversation/0001_initial_schema.sql": &fstest.MapFile{Data: baseline}}
			if err := migrateDatabase(ctx, db, ConversationDatabase, files); err != nil {
				t.Fatal(err)
			}
			turn := `{"owner":"main","request":{"request_id":"original"},"state":"running"}`
			if _, err := db.Exec("INSERT INTO metadata(key,value) VALUES('conversation.turn',?)", turn); err != nil {
				t.Fatal(err)
			}
			if receipt != "null" {
				if _, err := db.Exec("INSERT INTO metadata(key,value) VALUES('conversation.completion',?)", receipt); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := Migrate(ctx, db, ConversationDatabase); err != nil {
					t.Fatal(err)
				}
				var original, completion string
				if err := db.QueryRow("SELECT value FROM metadata WHERE key='conversation.turn'").Scan(&original); err != nil || original != turn {
					t.Fatal(original, err)
				}
				if err := db.QueryRow("SELECT value FROM metadata WHERE key='conversation.completion'").Scan(&completion); err != nil || completion != receipt {
					t.Fatal(completion, err)
				}
			}
		})
	}
}
