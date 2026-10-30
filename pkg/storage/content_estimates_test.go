package storage

import (
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestContentEstimateUpgradesPreserveHistory(t *testing.T) {
	for _, kind := range []Database{UsageDatabase, TasksDatabase} {
		for _, initialOnly := range []bool{true, false} {
			t.Run(string(kind)+map[bool]string{true: "/skipped", false: "/previous"}[initialOnly], func(t *testing.T) {
				db := migrationDB(t)
				ctx := t.Context()
				before := initialSchema(t, kind)
				if !initialOnly {
					files, err := fs.Glob(migrationFiles, "migrations/"+string(kind)+"/*.sql")
					if err != nil {
						t.Fatal(err)
					}
					before = fstest.MapFS{}
					for _, f := range files[:len(files)-1] {
						data, err := fs.ReadFile(migrationFiles, f)
						if err != nil {
							t.Fatal(err)
						}
						before[f] = &fstest.MapFile{Data: data}
					}
				}
				if err := migrateDatabase(ctx, db, kind, before); err != nil {
					t.Fatal(err)
				}
				insert := `INSERT INTO tools(id,scope,turn_id,name,at,bytes,failed) VALUES('old','owner','turn','exec',123,456,1)`
				query := `SELECT call_estimated_tokens IS NULL AND result_estimated_tokens IS NULL AND call_estimate_method IS NULL AND result_estimate_method IS NULL,bytes=456 AND failed=1 FROM tools WHERE id='old'`
				invalid := `UPDATE tools SET result_estimated_tokens=-1 WHERE id='old'`
				if kind == TasksDatabase {
					insert = `INSERT INTO agent_messages(id,envelope,direction,state,conversation,recipient,accepted_at_ms) VALUES('old','{}','in','processed','conversation','owner',123)`
					query = `SELECT content_estimated_tokens IS NULL AND content_estimate_method IS NULL,state='processed' AND accepted_at_ms=123 FROM agent_messages WHERE id='old'`
					invalid = `UPDATE agent_messages SET content_estimated_tokens=-1 WHERE id='old'`
				}
				if _, err := db.Exec(insert); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if err := Migrate(ctx, db, kind); err != nil {
						t.Fatal(err)
					}
					var unknown, preserved bool
					if err := db.QueryRow(query).Scan(&unknown, &preserved); err != nil || !unknown || !preserved {
						t.Fatal(unknown, preserved, err)
					}
				}
				if _, err := db.Exec(invalid); err == nil {
					t.Fatal("negative estimate accepted")
				}
			})
		}
	}
}
