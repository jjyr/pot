package storage

import (
	"context"
	"io/fs"
	"testing"
	"testing/fstest"
)

func TestUsageCacheReportingUpgradePreservesUnknownAndHistory(t *testing.T) {
	db := migrationDB(t)
	old := fstest.MapFS{}
	for _, name := range []string{"0001_baseline.sql", "0002_compaction_records.sql"} {
		path := "migrations/usage/" + name
		raw, err := fs.ReadFile(migrationFiles, path)
		if err != nil {
			t.Fatal(err)
		}
		old[path] = &fstest.MapFile{Data: raw}
	}
	ctx := context.Background()
	if err := migrateDatabase(ctx, db, UsageDatabase, old); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO requests(id,scope,turn_id,at,model,input,output,cache,input_estimate,output_cap,original) VALUES
('cached','main','turn',1,'model',100,10,80,100,20,'{"retained":true}'),
('unknown','main','turn',2,'model',100,10,0,100,20,'{"retained":true}')`)
	for range 2 {
		if err := Migrate(ctx, db, UsageDatabase); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"cached", "unknown"} {
		var input, output, cache int64
		var known bool
		var original string
		if err := db.QueryRow("SELECT input,output,cache,cache_reported,original FROM requests WHERE id=?", id).Scan(&input, &output, &cache, &known, &original); err != nil {
			t.Fatal(err)
		}
		if input != 100 || output != 10 || known != (id == "cached") || original != `{"retained":true}` || (id == "cached" && cache != 80) {
			t.Fatal(id, input, output, cache, known, original)
		}
	}
}
