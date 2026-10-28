package storage

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"sort"
	"testing"
	"testing/fstest"
)

func taskDefaultMigrationPaths(t *testing.T, kind Database) []string {
	t.Helper()
	paths, err := fs.Glob(migrationFiles, "migrations/"+string(kind)+"/*.sql")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(paths)
	return paths
}

func taskDefaultBefore(paths []string, target string) []string {
	var result []string
	for _, path := range paths {
		if path < target {
			result = append(result, path)
		}
	}
	return result
}

func taskDefaultMigrationFiles(t *testing.T, paths []string) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	for _, path := range paths {
		data, err := fs.ReadFile(migrationFiles, path)
		if err != nil {
			t.Fatal(err)
		}
		files[path] = &fstest.MapFile{Data: data}
	}
	return files
}

func migrateTaskDefaultPaths(t *testing.T, db *sql.DB, kind Database, paths []string) {
	t.Helper()
	if err := migrateDatabase(context.Background(), db, kind, taskDefaultMigrationFiles(t, paths)); err != nil {
		t.Fatal(err)
	}
}

func insertTaskDefaultReceipt(t *testing.T, ctx context.Context, db *sql.DB, id, hash, response string) {
	t.Helper()
	if _, err := db.ExecContext(ctx, "INSERT INTO assistant_configuration_requests VALUES(?,?,?)", id, hash, response); err != nil {
		t.Fatal(err)
	}
}

func verifyConversationTaskDefault(t *testing.T, ctx context.Context, db *sql.DB, wantType, wantModel string) {
	t.Helper()
	var gotType, gotModel sql.NullString
	var pendingModel string
	var pendingA int
	if err := db.QueryRowContext(ctx, `
    SELECT json_type(value,'$.intent.task_default'),
           json_extract(value,'$.intent.task_default.model'),
           json_extract(value,'$.pending_spec.model'),
           json_extract(value,'$.pending_spec.nested.a')
    FROM metadata WHERE key='assistant.configuration.v1'`).Scan(&gotType, &gotModel, &pendingModel, &pendingA); err != nil {
		t.Fatal(err)
	}
	if wantType == "" {
		if gotType.Valid {
			t.Fatalf("task_default unexpectedly present: %q", gotType.String)
		}
	} else if !gotType.Valid || gotType.String != wantType {
		t.Fatalf("task_default type = %v, want %q", gotType, wantType)
	}
	if wantModel == "" {
		if gotModel.Valid {
			t.Fatalf("task_default model unexpectedly present: %q", gotModel.String)
		}
	} else if !gotModel.Valid || gotModel.String != wantModel {
		t.Fatalf("task_default model = %v, want %q", gotModel, wantModel)
	}
	if pendingModel != "pending" || pendingA != 1 {
		t.Fatalf("pending spec changed: %q %d", pendingModel, pendingA)
	}
}

func verifyConversationReceipts(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	rows, err := db.QueryContext(ctx, `
    SELECT id,input_hash,
           json_type(response,'$.intent.task_default'),
           json_extract(response,'$.intent.task_default.model'),
           json_extract(response,'$.intent.nested.x'),
           json_extract(response,'$.intent.other')
    FROM assistant_configuration_requests ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var id, hash string
		var typ, model sql.NullString
		var nested, other sql.NullInt64
		if err := rows.Scan(&id, &hash, &typ, &model, &nested, &other); err != nil {
			t.Fatal(err)
		}
		seen[id] = true
		switch id {
		case "missing":
			if hash != "hash-missing" || !typ.Valid || typ.String != "null" || model.Valid || !nested.Valid || nested.Int64 != 1 || other.Valid {
				t.Fatalf("missing receipt = %q %v %v %v %v", hash, typ, model, nested, other)
			}
		case "explicit":
			if hash != "hash-explicit" || !typ.Valid || typ.String != "object" || !model.Valid || model.String != "keep" || nested.Valid || !other.Valid || other.Int64 != 1 {
				t.Fatalf("explicit receipt = %q %v %v %v %v", hash, typ, model, nested, other)
			}
		case "null-intent":
			if hash != "hash-null" || typ.Valid || model.Valid || nested.Valid || other.Valid {
				t.Fatalf("null intent receipt = %q %v %v %v %v", hash, typ, model, nested, other)
			}
		case "absent-intent":
			if hash != "hash-absent" || typ.Valid || model.Valid || nested.Valid || other.Valid {
				t.Fatalf("absent intent receipt = %q %v %v %v %v", hash, typ, model, nested, other)
			}
		default:
			t.Fatalf("unexpected receipt %q", id)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"missing", "explicit", "null-intent", "absent-intent"} {
		if !seen[id] {
			t.Fatalf("receipt %q missing", id)
		}
	}
}

func TestTaskDefaultConversationMigration(t *testing.T) {
	target := "migrations/conversation/0003_task_default.sql"
	paths := taskDefaultMigrationPaths(t, ConversationDatabase)
	if _, err := fs.Stat(migrationFiles, target); err != nil {
		t.Fatal(err)
	}
	prior := taskDefaultBefore(paths, target)
	if len(prior) == 0 {
		t.Fatal("missing prior conversation migrations")
	}
	cases := []struct {
		name      string
		initial   []string
		state     string
		wantType  string
		wantModel string
	}{
		{
			name:     "baseline",
			initial:  prior[:1],
			state:    `{"revision":7,"intent":{"goal":"keep","nested":{"n":1}},"pending_spec":{"profile_id":"p","model":"pending","nested":{"a":1}}}`,
			wantType: "null",
		},
		{
			name:     "preceding",
			initial:  prior,
			state:    `{"revision":7,"intent":{"goal":"keep","nested":{"n":1}},"pending_spec":{"profile_id":"p","model":"pending","nested":{"a":1}}}`,
			wantType: "null",
		},
		{
			name:      "preceding-explicit",
			initial:   prior,
			state:     `{"revision":7,"intent":{"task_default":{"profile_id":"p","model":"keep"},"goal":"keep"},"pending_spec":{"profile_id":"p","model":"pending","nested":{"a":1}}}`,
			wantType:  "object",
			wantModel: "keep",
		},
		{
			name:     "preceding-null-intent",
			initial:  prior,
			state:    `{"revision":7,"intent":null,"pending_spec":{"profile_id":"p","model":"pending","nested":{"a":1}}}`,
			wantType: "",
		},
		{
			name:     "preceding-absent-intent",
			initial:  prior,
			state:    `{"revision":7,"pending_spec":{"profile_id":"p","model":"pending","nested":{"a":1}}}`,
			wantType: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "conversation.sqlite")
			db, err := OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			migrateTaskDefaultPaths(t, db, ConversationDatabase, tc.initial)
			if _, err := db.ExecContext(ctx, "INSERT INTO metadata VALUES(?,?)", "assistant.configuration.v1", tc.state); err != nil {
				t.Fatal(err)
			}
			other := `{"intent":{"task_default":{"profile_id":"x","model":"y"}},"keep":true}`
			if _, err := db.ExecContext(ctx, "INSERT INTO metadata VALUES(?,?)", "other", other); err != nil {
				t.Fatal(err)
			}
			seededReceipts := false
			if exists, err := tableExists(ctx, db, "assistant_configuration_requests"); err != nil {
				t.Fatal(err)
			} else if exists {
				seededReceipts = true
				insertTaskDefaultReceipt(t, ctx, db, "missing", "hash-missing", `{"owner":"main","generation":1,"config":null,"revision":1,"intent":{"goal":"receipt","nested":{"x":1}},"pending":false}`)
				insertTaskDefaultReceipt(t, ctx, db, "explicit", "hash-explicit", `{"owner":"main","generation":2,"config":null,"revision":2,"intent":{"task_default":{"profile_id":"explicit","model":"keep"},"other":true},"pending":false}`)
				insertTaskDefaultReceipt(t, ctx, db, "null-intent", "hash-null", `{"owner":"main","generation":3,"config":null,"revision":3,"intent":null,"pending":false}`)
				insertTaskDefaultReceipt(t, ctx, db, "absent-intent", "hash-absent", `{"owner":"main","generation":4,"config":null,"revision":4,"pending":false}`)
			}
			if err := migrateDatabase(ctx, db, ConversationDatabase, migrationFiles); err != nil {
				t.Fatal(err)
			}
			verifyConversationTaskDefault(t, ctx, db, tc.wantType, tc.wantModel)
			var gotOther string
			if err := db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key='other'").Scan(&gotOther); err != nil {
				t.Fatal(err)
			}
			if gotOther != other {
				t.Fatalf("unrelated metadata changed: %q", gotOther)
			}
			if seededReceipts {
				verifyConversationReceipts(t, ctx, db)
			}
			if err := migrateDatabase(ctx, db, ConversationDatabase, migrationFiles); err != nil {
				t.Fatal(err)
			}
			verifyConversationTaskDefault(t, ctx, db, tc.wantType, tc.wantModel)
			if seededReceipts {
				verifyConversationReceipts(t, ctx, db)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = OpenDatabase(path, ConversationDatabase)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			verifyConversationTaskDefault(t, ctx, db, tc.wantType, tc.wantModel)
			if seededReceipts {
				verifyConversationReceipts(t, ctx, db)
			}
		})
	}
}

func verifyControlIntent(t *testing.T, ctx context.Context, db *sql.DB, id, wantType, wantModel string) {
	t.Helper()
	var gotType, gotModel sql.NullString
	if err := db.QueryRowContext(ctx, "SELECT json_type(intent,'$.task_default'), json_extract(intent,'$.task_default.model') FROM assistant_initial_intent WHERE id=?", id).Scan(&gotType, &gotModel); err != nil {
		t.Fatal(err)
	}
	if wantType == "" {
		if gotType.Valid {
			t.Fatalf("task_default unexpectedly present for %s: %q", id, gotType.String)
		}
	} else if !gotType.Valid || gotType.String != wantType {
		t.Fatalf("task_default type for %s = %v, want %q", id, gotType, wantType)
	}
	if wantModel == "" {
		if gotModel.Valid {
			t.Fatalf("task_default model unexpectedly present for %s: %q", id, gotModel.String)
		}
	} else if !gotModel.Valid || gotModel.String != wantModel {
		t.Fatalf("task_default model for %s = %v, want %q", id, gotModel, wantModel)
	}
}

func TestTaskDefaultControlMigration(t *testing.T) {
	target := "migrations/control/0005_task_default.sql"
	paths := taskDefaultMigrationPaths(t, ControlDatabase)
	if _, err := fs.Stat(migrationFiles, target); err != nil {
		t.Fatal(err)
	}
	prior := taskDefaultBefore(paths, target)
	if len(prior) == 0 {
		t.Fatal("missing prior control migrations")
	}
	cases := []struct {
		name    string
		initial []string
	}{
		{"baseline", prior[:1]},
		{"preceding", prior},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.sqlite")
			db, err := OpenSQLite(path)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			migrateTaskDefaultPaths(t, db, ControlDatabase, tc.initial)
			insert := func(id, intent string) {
				if _, err := db.ExecContext(ctx, "INSERT INTO assistant_initial_intent(id,intent) VALUES(?,?)", id, intent); err != nil {
					t.Fatal(err)
				}
			}
			insert("missing", `{"goal":"keep","nested":{"n":1}}`)
			insert("explicit", `{"task_default":{"profile_id":"p","model":"keep"},"other":true}`)
			insert("null", `null`)
			insert("array", `[1,2]`)
			if err := migrateDatabase(ctx, db, ControlDatabase, migrationFiles); err != nil {
				t.Fatal(err)
			}
			verifyControlIntent(t, ctx, db, "missing", "null", "")
			verifyControlIntent(t, ctx, db, "explicit", "object", "keep")
			verifyControlIntent(t, ctx, db, "null", "", "")
			verifyControlIntent(t, ctx, db, "array", "", "")
			var explicit string
			if err := db.QueryRowContext(ctx, "SELECT intent FROM assistant_initial_intent WHERE id='explicit'").Scan(&explicit); err != nil {
				t.Fatal(err)
			}
			if explicit != `{"task_default":{"profile_id":"p","model":"keep"},"other":true}` {
				t.Fatalf("explicit default changed: %q", explicit)
			}
			if err := migrateDatabase(ctx, db, ControlDatabase, migrationFiles); err != nil {
				t.Fatal(err)
			}
			verifyControlIntent(t, ctx, db, "missing", "null", "")
			verifyControlIntent(t, ctx, db, "explicit", "object", "keep")
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = OpenDatabase(path, ControlDatabase)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			verifyControlIntent(t, ctx, db, "missing", "null", "")
			verifyControlIntent(t, ctx, db, "explicit", "object", "keep")
			verifyControlIntent(t, ctx, db, "null", "", "")
			verifyControlIntent(t, ctx, db, "array", "", "")
		})
	}
}
