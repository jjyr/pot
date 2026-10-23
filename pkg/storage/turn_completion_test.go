package storage

import (
	"context"
	"testing"
)

func TestTurnCompletionUpgradePreservesExistingTurn(t *testing.T) {
	for _, receipt := range []string{"null", `{"turn":{"request":{"request_id":"original"}},"phase":"succeeded","error":""}`} {
		t.Run(receipt, func(t *testing.T) {
			db := migrationDB(t)
			ctx := context.Background()
			if err := migrateDatabase(ctx, db, ConversationDatabase, initialSchema(t, ConversationDatabase)); err != nil {
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
