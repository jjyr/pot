package storage

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestCompactionDatabaseUpgradesPreserveHistoryAndUsage(t *testing.T) {
	for _, kind := range []Database{ConversationDatabase, UsageDatabase} {
		t.Run(string(kind), func(t *testing.T) {
			db := migrationDB(t)
			ctx := context.Background()
			old := fstest.MapFS{}
			root := "migrations/" + string(kind)
			entries, err := fs.ReadDir(migrationFiles, root)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if entry.Name() == "0004_idle_compaction.sql" || entry.Name() == "0002_compaction_records.sql" {
					continue
				}
				path := root + "/" + entry.Name()
				raw, _ := fs.ReadFile(migrationFiles, path)
				old[path] = &fstest.MapFile{Data: raw}
			}
			if err = migrateDatabase(ctx, db, kind, old); err != nil {
				t.Fatal(err)
			}
			if kind == ConversationDatabase {
				execMigrationSQL(t, db, `INSERT INTO metadata VALUES('thread.context','{"messages":["original"]}')`)
			} else {
				execMigrationSQL(t, db, `INSERT INTO compactions(id,scope,turn_id,at,reason,state)VALUES('old','main','turn',1,'threshold','complete')`)
			}
			for range 2 {
				if err = Migrate(ctx, db, kind); err != nil {
					t.Fatal(err)
				}
			}
			var value string
			if kind == ConversationDatabase {
				if err = db.QueryRow("SELECT value FROM metadata WHERE key='thread.context'").Scan(&value); err != nil || value != `{"messages":["original"]}` {
					t.Fatal(value, err)
				}
				if err = db.QueryRow("SELECT request_id FROM assistant_idle_compaction WHERE singleton=1").Scan(&value); err != nil || value != "" {
					t.Fatal(value, err)
				}
			} else {
				if err = db.QueryRow("SELECT state FROM compactions WHERE id='old'").Scan(&value); err != nil || value != "complete" {
					t.Fatal(value, err)
				}
				execMigrationSQL(t, db, `INSERT INTO compaction_evidence(compaction_id,at,body)VALUES('old',2,'{}')`)
			}
		})
	}
}
