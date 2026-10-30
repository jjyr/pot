package storage

import "testing"

func TestRequestAccountingUpgradePreservesUnknownHistory(t *testing.T) {
	for _, previous := range []bool{false, true} {
		t.Run(map[bool]string{true: "previous", false: "baseline"}[previous], func(t *testing.T) {
			db := migrationDB(t)
			ctx := t.Context()
			if previous {
				migrateTaskDefaultPaths(t, db, UsageDatabase, taskDefaultBefore(taskDefaultMigrationPaths(t, UsageDatabase), "migrations/usage/0004_request_accounting.sql"))
			} else if e := migrateDatabase(ctx, db, UsageDatabase, initialSchema(t, UsageDatabase)); e != nil {
				t.Fatal(e)
			}
			if _, e := db.Exec(`INSERT INTO requests(id,scope,turn_id,at,model,input,output,input_estimate,output_cap,original) VALUES('old','owner','turn',1,'model',100,20,100,20,'{}')`); e != nil {
				t.Fatal(e)
			}
			for range 2 {
				if e := Migrate(ctx, db, UsageDatabase); e != nil {
					t.Fatal(e)
				}
				var count, input int
				if e := db.QueryRow(`SELECT COUNT(*),SUM(input) FROM requests`).Scan(&count, &input); e != nil || count != 1 || input != 100 {
					t.Fatal(count, input, e)
				}
				if e := db.QueryRow(`SELECT COUNT(*) FROM request_accounting`).Scan(&count); e != nil || count != 0 {
					t.Fatal("invented historical attribution", count, e)
				}
			}
			if _, e := db.Exec(`INSERT INTO request_accounting(request_id,stage,cache_write) VALUES('old','unknown',-1)`); e == nil {
				t.Fatal("negative cache write")
			}
			if _, e := db.Exec(`INSERT INTO request_accounting(request_id,stage) VALUES('missing','unknown')`); e == nil {
				t.Fatal("missing request accepted")
			}
		})
	}

}
