package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/jjyr/pot/pkg/assistantavatar"
)

func TestCharacterAvatarUpgrade(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	old := fstest.MapFS{}
	entries, _ := fs.ReadDir(migrationFiles, "migrations/control")
	for _, entry := range entries {
		if entry.Name() >= "0015_" {
			continue
		}
		path := "migrations/control/" + entry.Name()
		data, _ := fs.ReadFile(migrationFiles, path)
		old[path] = &fstest.MapFile{Data: data}
	}
	if err := migrateDatabase(ctx, db, ControlDatabase, old); err != nil {
		t.Fatal(err)
	}
	count := migrationCount(t, db)
	execMigrationSQL(t, db, `INSERT INTO assistant_definitions(id,name,revision,request_id,created_name,deleted) VALUES
 ('renamed','My helper',3,'seed-renamed','Mug 2',0),('custom','My custom',4,'seed-custom','Cup',0),
 ('missing','Missing',2,'seed-missing','Pot',0),('deleted','Deleted',5,'seed-deleted','Glass',1);
 INSERT INTO assistant_profiles VALUES ('renamed',pot_default_avatar('seed-renamed')),('custom','uploaded-image'),('deleted',pot_default_avatar('seed-deleted'));
 INSERT INTO assistant_profile_requests VALUES
 ('default','original-hash',json_object('id','renamed','name','Old name','revision',2,'request_id','seed-renamed','avatar',pot_default_avatar('seed-renamed'))),
 ('custom','custom-hash','{"id":"custom","name":"My custom","revision":3,"request_id":"seed-custom","avatar":"uploaded-image"}');`)
	// A failed conversion must roll back both new metadata and rewritten images.
	execMigrationSQL(t, db, `CREATE TRIGGER fail_character_conversion BEFORE UPDATE ON assistant_profiles BEGIN SELECT RAISE(ABORT,'test interrupted upgrade'); END;`)
	if err := Migrate(ctx, db, ControlDatabase); err == nil {
		t.Fatal("failed migration accepted")
	}
	if migrationCount(t, db) != count {
		t.Fatal("failed migration left a receipt")
	}
	var fields int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('assistant_profiles') WHERE name='character'`).Scan(&fields); err != nil || fields != 0 {
		t.Fatal("schema did not roll back", err)
	}
	execMigrationSQL(t, db, "DROP TRIGGER fail_character_conversion")
	for range 2 {
		if err := Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
		for id, character := range map[string]string{"renamed": "mug", "custom": "cup", "missing": "pot", "deleted": "glass"} {
			var got, avatar string
			if err := db.QueryRow("SELECT character,avatar FROM assistant_profiles WHERE assistant_id=?", id).Scan(&got, &avatar); err != nil {
				t.Fatal(err)
			}
			want, _ := assistantavatar.Render(character, "seed-"+id)
			if id == "custom" {
				want = "uploaded-image"
			}
			if got != character || avatar != want {
				t.Fatal("incorrect migrated profile", id, got)
			}
		}
		var avatar, name, hash string
		var revision int
		if err := db.QueryRow(`SELECT json_extract(response,'$.avatar'),json_extract(response,'$.name'),json_extract(response,'$.revision'),input_hash FROM assistant_profile_requests WHERE id='default'`).Scan(&avatar, &name, &revision, &hash); err != nil {
			t.Fatal(err)
		}
		want, _ := assistantavatar.Render("mug", "seed-renamed")
		if avatar != want || name != "Old name" || revision != 2 || hash != "original-hash" {
			t.Fatal("receipt damaged")
		}
		if err := db.QueryRow(`SELECT json_extract(response,'$.avatar') FROM assistant_profile_requests WHERE id='custom'`).Scan(&avatar); err != nil || avatar != "uploaded-image" {
			t.Fatal("custom receipt changed", err)
		}
		var total int
		if err := db.QueryRow("SELECT SUM(revision) FROM assistant_definitions").Scan(&total); err != nil || total != 14 {
			t.Fatal("identity revisions changed", err)
		}
	}
}

func TestDefaultAvatarImageUpgrade(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	baseline, err := fs.ReadFile(migrationFiles, "migrations/control/0001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	old := fstest.MapFS{"migrations/control/0001_baseline.sql": &fstest.MapFile{Data: baseline}}
	entries, err := fs.ReadDir(migrationFiles, "migrations/control")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name()[:4] > "0010" {
			continue
		}
		path := "migrations/control/" + entry.Name()
		data, err := fs.ReadFile(migrationFiles, path)
		if err != nil {
			t.Fatal(err)
		}
		old[path] = &fstest.MapFile{Data: data}
	}
	if err := migrateDatabase(ctx, db, ControlDatabase, old); err != nil {
		t.Fatal(err)
	}
	priorCount := migrationCount(t, db)
	execMigrationSQL(t, db, `INSERT INTO assistant_definitions(id,name,revision,request_id,created_name) VALUES
('missing','Missing',3,'seed-missing','Original'),('empty','Empty',4,'seed-empty','Empty'),('custom','Custom',5,'seed-custom','Custom');
INSERT INTO assistant_profiles VALUES('empty',''),('custom','custom-image');
INSERT INTO assistant_profile_requests VALUES
('reset','reset-hash','{"id":"empty","name":"Before rename","revision":2,"request_id":"seed-empty"}'),
('custom','custom-hash','{"id":"custom","name":"Custom","revision":4,"request_id":"seed-custom","avatar":"custom-image"}');`)
	// A bad historical receipt must roll back the profile images and receipt
	// together. Correcting that input allows the same pending version to retry.
	execMigrationSQL(t, db, `INSERT INTO assistant_profile_requests VALUES('bad','bad-hash','{"id":"missing"}');`)
	if err := Migrate(ctx, db, ControlDatabase); err == nil {
		t.Fatal("invalid historical seed accepted")
	}
	var created int
	if err := db.QueryRow("SELECT COUNT(*) FROM assistant_profiles WHERE assistant_id='missing'").Scan(&created); err != nil || created != 0 || migrationCount(t, db) != priorCount {
		t.Fatal("failed avatar migration did not roll back", err)
	}
	execMigrationSQL(t, db, "DELETE FROM assistant_profile_requests WHERE id='bad'")
	for range 2 {
		if err := Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{"missing", "empty", "custom"} {
			var avatar string
			if err := db.QueryRow("SELECT avatar FROM assistant_profiles WHERE assistant_id=?", id).Scan(&avatar); err != nil {
				t.Fatal(err)
			}
			want, _ := assistantavatar.Render(assistantavatar.ForName(map[string]string{"missing": "Original", "empty": "Empty"}[id], "seed-"+id), "seed-"+id)
			if id == "custom" {
				want = "custom-image"
			}
			if avatar != want {
				t.Fatal("upgrade changed image", id)
			}
		}
		var avatar, name, hash string
		var revision int
		if err := db.QueryRow(`SELECT json_extract(response,'$.avatar'),json_extract(response,'$.name'),json_extract(response,'$.revision'),input_hash FROM assistant_profile_requests WHERE id='reset'`).Scan(&avatar, &name, &revision, &hash); err != nil {
			t.Fatal(err)
		}
		want, _ := assistantavatar.Render(assistantavatar.ForName("Empty", "seed-empty"), "seed-empty")
		if avatar != want || name != "Before rename" || revision != 2 || hash != "reset-hash" {
			t.Fatal("upgrade damaged profile receipt")
		}
		if err := db.QueryRow(`SELECT json_extract(response,'$.avatar') FROM assistant_profile_requests WHERE id='custom'`).Scan(&avatar); err != nil || avatar != "custom-image" {
			t.Fatal("upgrade changed custom image receipt", err)
		}
		var totalRevision int
		if err := db.QueryRow("SELECT SUM(revision) FROM assistant_definitions").Scan(&totalRevision); err != nil || totalRevision != 12 {
			t.Fatal("upgrade changed assistant revisions", err)
		}
	}
}

func TestDisplayResourcesUpgradePreservesExistingControl(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	old := fstest.MapFS{}
	entries, err := fs.ReadDir(migrationFiles, "migrations/control")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "0014_" {
			continue
		}
		name := "migrations/control/" + entry.Name()
		data, _ := fs.ReadFile(migrationFiles, name)
		old[name] = &fstest.MapFile{Data: data}
	}
	if err := migrateDatabase(ctx, db, ControlDatabase, old); err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, `INSERT INTO assistant_definitions(id,name,revision,request_id,created_name) VALUES('kept','Kept',7,'kept-request','Kept')`)
	for range 2 {
		if err := Migrate(ctx, db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var name string
	var revision int
	if err := db.QueryRow("SELECT name,revision FROM assistant_definitions WHERE id='kept'").Scan(&name, &revision); err != nil || name != "Kept" || revision != 7 {
		t.Fatal("upgrade changed existing assistant", err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM display_resources").Scan(&count); err != nil || count != 0 {
		t.Fatal("resource table upgrade", err)
	}
}

func migrationDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func migrationFixture(statements ...string) fstest.MapFS {
	files := fstest.MapFS{}
	for index, statement := range statements {
		files[fmt.Sprintf("migrations/tasks/%04d_step.sql", index+1)] = &fstest.MapFile{Data: []byte("-- +migrate Up\n-- +migrate StatementBegin\n" + statement + "\n-- +migrate StatementEnd\n")}
	}
	return files
}

func execMigrationSQL(t *testing.T, db *sql.DB, statement string) {
	t.Helper()
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

func migrationCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM pot_migrations").Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestDatabaseSchemasCreateAndReopen(t *testing.T) {
	for _, kind := range []Database{ControlDatabase, TasksDatabase, RunnerDatabase, ConversationDatabase, MachinesDatabase, UsageDatabase, OperationsDatabase} {
		t.Run(string(kind), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "db.sqlite")
			db, err := OpenDatabase(path, kind)
			if err != nil {
				t.Fatal(err)
			}
			var integrity string
			if err := db.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
				t.Fatal(integrity, err)
			}
			definitions, err := loadMigrations(migrationFiles, kind)
			if err != nil {
				t.Fatal(err)
			}
			if got := migrationCount(t, db); got != len(definitions) {
				t.Fatal(got, len(definitions))
			}
			db.Close()
			db, err = OpenDatabase(path, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if got := migrationCount(t, db); got != len(definitions) {
				t.Fatal(got)
			}
		})
	}
}

func TestMigrationSkipsVersionsAndTransformsJSONOnce(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	first := `CREATE TABLE records(id TEXT PRIMARY KEY,body TEXT NOT NULL); INSERT INTO records VALUES('task','{"schema_version":1,"goal":"keep me"}');`
	files := migrationFixture(first)
	if err := migrateDatabase(ctx, db, TasksDatabase, files); err != nil {
		t.Fatal(err)
	}
	files = migrationFixture(first,
		`ALTER TABLE records ADD COLUMN revision INTEGER NOT NULL DEFAULT 0; UPDATE records SET body=json_set(body,'$.schema_version',2,'$.instructions',json_extract(body,'$.goal'));`,
		`UPDATE records SET body=json_remove(body,'$.goal'),revision=revision+1; CREATE INDEX records_revision ON records(revision);`)
	for range 2 {
		if err := migrateDatabase(ctx, db, TasksDatabase, files); err != nil {
			t.Fatal(err)
		}
	}
	var body string
	var revision int
	if err := db.QueryRow("SELECT body,revision FROM records WHERE id='task'").Scan(&body, &revision); err != nil {
		t.Fatal(err)
	}
	if body != `{"schema_version":2,"instructions":"keep me"}` || revision != 1 || migrationCount(t, db) != 3 {
		t.Fatal(body, revision)
	}
}

func TestMigrationFailureRollsBackAndRetries(t *testing.T) {
	db := migrationDB(t)
	ctx := context.Background()
	first := `CREATE TABLE records(value INTEGER NOT NULL); INSERT INTO records VALUES(10);`
	if err := migrateDatabase(ctx, db, TasksDatabase, migrationFixture(first)); err != nil {
		t.Fatal(err)
	}
	files := migrationFixture(first, `ALTER TABLE records ADD COLUMN added TEXT; UPDATE records SET value=value+1; INSERT INTO ready VALUES(1);`)
	if err := migrateDatabase(ctx, db, TasksDatabase, files); err == nil {
		t.Fatal("missing prerequisite did not fail")
	}
	var value, added int
	if err := db.QueryRow("SELECT value FROM records").Scan(&value); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('records') WHERE name='added'").Scan(&added); err != nil {
		t.Fatal(err)
	}
	if value != 10 || added != 0 || migrationCount(t, db) != 1 {
		t.Fatal(value, added)
	}
	// Resolve the environmental prerequisite; retry the exact same migration.
	execMigrationSQL(t, db, "CREATE TABLE ready(value INTEGER)")
	if err := migrateDatabase(ctx, db, TasksDatabase, files); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT value FROM records").Scan(&value); err != nil || value != 11 {
		t.Fatal(value, err)
	}
	if migrationCount(t, db) != 2 {
		t.Fatal("failed migration was not retried")
	}
}

func TestMigrationRejectsInvalidHistoryBeforeWriting(t *testing.T) {
	for _, mode := range []string{"changed", "newer", "gap", "wrong-database"} {
		t.Run(mode, func(t *testing.T) {
			db := migrationDB(t)
			ctx := context.Background()
			files := migrationFixture("CREATE TABLE records(value INTEGER); INSERT INTO records VALUES(1);", "UPDATE records SET value=2;")
			if err := migrateDatabase(ctx, db, TasksDatabase, files); err != nil {
				t.Fatal(err)
			}
			kind := TasksDatabase
			switch mode {
			case "changed":
				files["migrations/tasks/0001_step.sql"].Data = append(files["migrations/tasks/0001_step.sql"].Data, []byte("-- changed\n")...)
			case "newer":
				delete(files, "migrations/tasks/0002_step.sql")
			case "gap":
				execMigrationSQL(t, db, "DELETE FROM pot_migrations WHERE id LIKE '0001_%'")
			case "wrong-database":
				kind = ControlDatabase
				files["migrations/control/0001_step.sql"] = files["migrations/tasks/0001_step.sql"]
			}
			before := migrationCount(t, db)
			if err := migrateDatabase(ctx, db, kind, files); err == nil || !strings.Contains(err.Error(), "history mismatch") {
				t.Fatal(err)
			}
			var value int
			if err := db.QueryRow("SELECT value FROM records").Scan(&value); err != nil || value != 2 {
				t.Fatal(value, err)
			}
			if migrationCount(t, db) != before {
				t.Fatal("rejected history was changed")
			}
		})
	}
}

func TestMigrationRejectsInvalidDefinitions(t *testing.T) {
	for _, mode := range []string{"gap", "duplicate", "down", "nontransactional", "empty", "unknown-kind"} {
		t.Run(mode, func(t *testing.T) {
			db := migrationDB(t)
			files := migrationFixture("CREATE TABLE records(value INTEGER);")
			kind := TasksDatabase
			switch mode {
			case "gap":
				files["migrations/tasks/0003_step.sql"] = files["migrations/tasks/0001_step.sql"]
			case "duplicate":
				files["migrations/tasks/0001_other.sql"] = files["migrations/tasks/0001_step.sql"]
			case "down":
				files["migrations/tasks/0001_step.sql"].Data = append(files["migrations/tasks/0001_step.sql"].Data, []byte("-- +migrate Down\nDROP TABLE records;\n")...)
			case "nontransactional":
				files["migrations/tasks/0001_step.sql"].Data = []byte("-- +migrate Up notransaction\nCREATE TABLE records(value INTEGER);\n")
			case "empty":
				files["migrations/tasks/0001_step.sql"].Data = []byte("-- +migrate Up\n")
			case "unknown-kind":
				kind = "../tasks"
			}
			if err := migrateDatabase(context.Background(), db, kind, files); err == nil {
				t.Fatal("accepted invalid definition")
			}
			if exists, err := tableExists(context.Background(), db, "pot_migrations"); err != nil || exists {
				t.Fatal(exists, err)
			}
		})
	}
}

func TestMigrationAdoptsLegacyDatabaseWithoutRewritingHistory(t *testing.T) {
	for _, kind := range []Database{ControlDatabase, TasksDatabase, RunnerDatabase, ConversationDatabase, MachinesDatabase, UsageDatabase, OperationsDatabase} {
		t.Run(string(kind), func(t *testing.T) {
			db := migrationDB(t)
			// Recreate the untracked baseline, retaining its old version markers.
			data, err := fs.ReadFile(migrationFiles, "migrations/"+string(kind)+"/0001_baseline.sql")
			if err != nil {
				t.Fatal(err)
			}
			sql := string(data)
			for _, table := range []string{"task_schema", "schedule_schema", "runner_schema"} {
				sql = strings.ReplaceAll(sql, "DROP TABLE IF EXISTS "+table+";", "")
			}
			execMigrationSQL(t, db, sql)
			switch kind {
			case TasksDatabase:
				execMigrationSQL(t, db, `CREATE TABLE task_schema(version INTEGER); INSERT INTO task_schema VALUES(8); CREATE TABLE schedule_schema(version INTEGER); INSERT INTO schedule_schema VALUES(2); INSERT INTO task_authority VALUES('control','runtime'); INSERT INTO tasks VALUES('owner','task','running',123,'{"original":"unchanged"}'); INSERT INTO task_writes VALUES('owner','request','digest','task');`)
			case RunnerDatabase:
				execMigrationSQL(t, db, `CREATE TABLE runner_schema(version INTEGER); INSERT INTO runner_schema VALUES(6); INSERT INTO task_runs VALUES('control','task','run','digest','running','boot','{"original":"unchanged"}');`)
			case ConversationDatabase:
				execMigrationSQL(t, db, `PRAGMA user_version=1; INSERT INTO messages VALUES('message','request','{"original":"unchanged"}');`)
			case ControlDatabase:
				execMigrationSQL(t, db, `INSERT INTO secrets VALUES('credential',X'01',X'0203',X'04');`)
			}
			if err := Migrate(context.Background(), db, kind); err != nil {
				t.Fatal(err)
			}
			var body string
			switch kind {
			case TasksDatabase:
				var phase string
				if err := db.QueryRow("SELECT phase,body FROM tasks").Scan(&phase, &body); err != nil || phase != "running" {
					t.Fatal(phase, err)
				}
			case RunnerDatabase:
				var phase string
				if err := db.QueryRow("SELECT phase,body FROM task_runs").Scan(&phase, &body); err != nil || phase != "running" {
					t.Fatal(phase, err)
				}
			case ConversationDatabase:
				if err := db.QueryRow("SELECT body FROM messages").Scan(&body); err != nil {
					t.Fatal(err)
				}
			case ControlDatabase:
				var ciphertext string
				if err := db.QueryRow("SELECT hex(ciphertext) FROM secrets").Scan(&ciphertext); err != nil || ciphertext != "0203" {
					t.Fatal(ciphertext, err)
				}
			}
			if body != "" && body != `{"original":"unchanged"}` {
				t.Fatal(body)
			}
			if migrationCount(t, db) == 0 {
				t.Fatal("baseline not recorded")
			}
		})
	}
}

func TestMigrationRejectsUnsupportedLegacyWithoutChanges(t *testing.T) {
	for _, fixture := range []struct {
		kind Database
		sql  string
	}{
		{TasksDatabase, "CREATE TABLE task_schema(version INTEGER); INSERT INTO task_schema VALUES(7)"},
		{TasksDatabase, "CREATE TABLE schedule_schema(version INTEGER); INSERT INTO schedule_schema VALUES(1)"},
		{TasksDatabase, "CREATE TABLE task_schema(version INTEGER); INSERT INTO task_schema VALUES(8),(8)"},
		{TasksDatabase, "CREATE TABLE task_schema(version INTEGER)"},
		{TasksDatabase, "CREATE TABLE task_inputs(value TEXT)"},
		{RunnerDatabase, "CREATE TABLE runner_schema(version INTEGER); INSERT INTO runner_schema VALUES(5)"},
		{ConversationDatabase, "PRAGMA user_version=2"},
	} {
		t.Run(fixture.sql, func(t *testing.T) {
			db := migrationDB(t)
			execMigrationSQL(t, db, fixture.sql)
			for range 2 {
				if err := Migrate(context.Background(), db, fixture.kind); err == nil {
					t.Fatal("accepted unsupported legacy database")
				}
				if exists, err := tableExists(context.Background(), db, "pot_migrations"); err != nil || exists {
					t.Fatal(exists, err)
				}
			}
		})
	}
}

func TestMigrationConcurrentHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "shared.sqlite")
	// Open handles first; OpenSQLite's journal PRAGMA itself is not a migration.
	var handles []*sql.DB
	for range 4 {
		db, err := OpenSQLite(path)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		handles = append(handles, db)
	}
	var wg sync.WaitGroup
	for _, db := range handles {
		wg.Go(func() {
			if err := Migrate(context.Background(), db, TasksDatabase); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	definitions, err := loadMigrations(migrationFiles, TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if got := migrationCount(t, handles[0]); got != len(definitions) {
		t.Fatal(got)
	}
}

func TestMigrationCancellationDoesNotWrite(t *testing.T) {
	db := migrationDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Migrate(ctx, db, TasksDatabase); err != context.Canceled {
		t.Fatal(err)
	}
	if exists, err := tableExists(context.Background(), db, "pot_migrations"); err != nil || exists {
		t.Fatal(exists, err)
	}
}

func TestContinuationMigrationsPreserveLegacyRunsAndArchiveState(t *testing.T) {
	ctx := context.Background()
	for _, phase := range []string{"running", "complete", "cancelled", "failed", "interrupted"} {
		t.Run(phase, func(t *testing.T) {
			db := migrationDB(t)
			for _, name := range []string{"0001_baseline.sql", "0002_plugin_event_payload.sql"} {
				data, err := fs.ReadFile(migrationFiles, "migrations/tasks/"+name)
				if err != nil {
					t.Fatal(err)
				}
				execMigrationSQL(t, db, string(data))
			}
			execMigrationSQL(t, db, fmt.Sprintf(`CREATE TABLE task_schema(version INTEGER); INSERT INTO task_schema VALUES(8);
INSERT INTO tasks VALUES('owner','task','%s',123,'{"schema_version":8,"run_id":"run","info":{"phase":"%s"},"request":{"text":"original"},"outcome":{"text":"retained"}}');
INSERT INTO task_writes VALUES('owner','request','original-digest','task');`, phase, phase))
			for range 2 {
				if err := Migrate(ctx, db, TasksDatabase); err != nil {
					t.Fatal(err)
				}
			}
			var version int
			var run, text, outcome, digest string
			var archived bool
			if err := db.QueryRow(`SELECT json_extract(body,'$.schema_version'),json_extract(body,'$.info.run_id'),json_extract(body,'$.info.archived'),json_extract(body,'$.request.text'),json_extract(body,'$.outcome.text') FROM tasks`).Scan(&version, &run, &archived, &text, &outcome); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow("SELECT digest FROM task_writes").Scan(&digest); err != nil {
				t.Fatal(err)
			}
			if version != 10 || run != "run" || archived != (phase == "complete" || phase == "cancelled") || text != "original" || outcome != "retained" || digest != "original-digest" {
				t.Fatal(version, run, archived, text, outcome, digest)
			}
		})
	}
	db := migrationDB(t)
	data, err := fs.ReadFile(migrationFiles, "migrations/runner/0001_baseline.sql")
	if err != nil {
		t.Fatal(err)
	}
	execMigrationSQL(t, db, string(data))
	execMigrationSQL(t, db, `CREATE TABLE runner_schema(version INTEGER); INSERT INTO runner_schema VALUES(6);
INSERT INTO task_runs VALUES('control','task','run','digest','complete','boot','{"schema_version":6,"launch":{"run_id":"run"},"launch_digest":"digest","context":{"checkpoint":"retained"}}');`)
	for range 2 {
		if err := Migrate(ctx, db, RunnerDatabase); err != nil {
			t.Fatal(err)
		}
	}
	var version int
	var run, digest, checkpoint string
	if err := db.QueryRow(`SELECT json_extract(body,'$.schema_version'),json_extract(body,'$.launch.run_id'),json_extract(body,'$.launch_digest'),json_extract(body,'$.context.checkpoint') FROM task_runs`).Scan(&version, &run, &digest, &checkpoint); err != nil {
		t.Fatal(err)
	}
	if version != 7 || run != "run" || digest != "digest" || checkpoint != "retained" {
		t.Fatal(version, run, digest, checkpoint)
	}
}

func TestAssistantDeletionUpgradePreservesExistingIdentity(t *testing.T) {
	for _, predecessor := range []string{"0001", "0011"} {
		t.Run(predecessor, func(t *testing.T) {
			ctx := context.Background()
			db := migrationDB(t)
			entries, err := fs.ReadDir(migrationFiles, "migrations/control")
			if err != nil {
				t.Fatal(err)
			}
			prior := fstest.MapFS{}
			for _, entry := range entries {
				if entry.Name()[:4] > predecessor {
					continue
				}
				path := "migrations/control/" + entry.Name()
				data, err := fs.ReadFile(migrationFiles, path)
				if err != nil {
					t.Fatal(err)
				}
				prior[path] = &fstest.MapFile{Data: data}
			}
			if err = migrateDatabase(ctx, db, ControlDatabase, prior); err != nil {
				t.Fatal(err)
			}
			execMigrationSQL(t, db, "INSERT INTO assistant_definitions(id,name,revision,request_id,created_name) VALUES('main','Keep',7,'original','Keep')")
			for range 2 {
				if err = Migrate(ctx, db, ControlDatabase); err != nil {
					t.Fatal(err)
				}
			}
			var name, request string
			var revision int
			var deleted bool
			if err = db.QueryRow("SELECT name,revision,request_id,deleted FROM assistant_definitions WHERE id='main'").Scan(&name, &revision, &request, &deleted); err != nil {
				t.Fatal(err)
			}
			if name != "Keep" || revision != 7 || request != "original" || deleted {
				t.Fatal(name, revision, request, deleted)
			}
			execMigrationSQL(t, db, "UPDATE assistant_definitions SET deleted=1 WHERE id='main'")
			if err = Migrate(ctx, db, ControlDatabase); err != nil {
				t.Fatal(err)
			}
			if err = db.QueryRow("SELECT deleted FROM assistant_definitions WHERE id='main'").Scan(&deleted); err != nil || !deleted {
				t.Fatal(deleted, err)
			}
		})
	}
}

func TestGitHubNotificationMigrationCancelsHistoryAndPreservesDelivery(t *testing.T) {
	ctx := context.Background()
	for _, consumed := range []bool{false, true} {
		t.Run(fmt.Sprint(consumed), func(t *testing.T) {
			db := migrationDB(t)
			files := githubNotificationPreviousMigrations(t)
			var err error
			if err = migrateDatabase(ctx, db, TasksDatabase, files); err != nil {
				t.Fatal(err)
			}
			receipt := "older"
			if consumed {
				receipt = "history"
			}
			state := fmt.Sprintf(`{"account":"123","manual":%q,"scan":null,"since":"2026-01-01T00:00:00Z","baseline":true,"nextPollAt":9000,"pending":{"thread":{"body":"preserve"}},"threads":{"id":{"marker":"keep"}}}`, receipt)
			_, err = db.Exec(`INSERT INTO plugin_background VALUES('github',1,'main','{"repositories":["me/repo"],"reasons":["mention"],"existing":"history","existing_account":"123"}',9,?,8000,'unavailable','conversation')`, state)
			if err != nil {
				t.Fatal(err)
			}
			execMigrationSQL(t, db, `INSERT INTO plugin_background VALUES('codex',0,'','{}',2,'{"cursor":"keep"}',7000,'disabled','codex-conversation');`)
			for range 2 {
				if err = Migrate(ctx, db, TasksDatabase); err != nil {
					t.Fatal(err)
				}
			}
			var config, raw, status, diagnostic, conversation string
			var revision, next int
			if err = db.QueryRow(`SELECT config,state,revision,next,status,diagnostic,conversation FROM plugin_background WHERE id='github'`).Scan(&config, &raw, &revision, &next, &status, &diagnostic, &conversation); err != nil {
				t.Fatal(err)
			}
			var c, s map[string]any
			if err = json.Unmarshal([]byte(config), &c); err != nil {
				t.Fatal(err)
			}
			if err = json.Unmarshal([]byte(raw), &s); err != nil {
				t.Fatal(err)
			}
			if c["existing"] != nil || c["existing_account"] != nil || s["manual"] != nil || s["scan"] != nil || s["since"] != "2026-01-01T00:00:00Z" || s["pending"] == nil || s["threads"] == nil || revision != 12 || diagnostic != "" || conversation != "conversation" {
				t.Fatal(config, raw, revision, diagnostic, conversation)
			}
			if !consumed && (next != 0 || status != "pending" || s["nextPollAt"] != float64(0)) {
				t.Fatal(next, status, raw)
			}
			if consumed && (next != 8000 || status != "unavailable" || s["nextPollAt"] != float64(9000)) {
				t.Fatal(next, status, raw)
			}
			if err = db.QueryRow(`SELECT state,revision FROM plugin_background WHERE id='codex'`).Scan(&raw, &revision); err != nil || raw != `{"cursor":"keep"}` || revision != 2 {
				t.Fatal(raw, revision, err)
			}
		})
	}
}

func TestGitHubNotificationMigrationHandlesUninitializedAndFailedBaseline(t *testing.T) {
	for _, state := range []string{`null`, `{"account":"123","baseline":false,"since":null,"failures":6,"manual":false,"nextPollAt":999999,"pending":{},"threads":{}}`} {
		db := migrationDB(t)
		if err := migrateDatabase(context.Background(), db, TasksDatabase, githubNotificationPreviousMigrations(t)); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO plugin_background VALUES('github',1,'main','{"existing":"unfinished"}',9,?,999999,'unavailable','same-conversation')`, state); err != nil {
			t.Fatal(err)
		}
		if err := Migrate(context.Background(), db, TasksDatabase); err != nil {
			t.Fatal(err)
		}
		var raw, config, status string
		var next int
		if err := db.QueryRow(`SELECT state,config,next,status FROM plugin_background`).Scan(&raw, &config, &next, &status); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(config, `"reasons":["mention"]`) || next != 0 || status != "pending" {
			t.Fatal(raw, config, next, status)
		}
		if state == "null" && raw != "null" {
			t.Fatal(raw)
		}
		if state != "null" {
			var s map[string]any
			json.Unmarshal([]byte(raw), &s)
			if s["baseline"] != false || s["since"] != nil || s["failures"] != float64(6) || s["nextPollAt"] != float64(0) {
				t.Fatal(raw)
			}
		}
	}
}

func githubNotificationPreviousMigrations(t *testing.T) fstest.MapFS {
	t.Helper()
	files := fstest.MapFS{}
	entries, err := fs.ReadDir(migrationFiles, "migrations/tasks")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() >= "0007_" {
			break
		}
		name := "migrations/tasks/" + entry.Name()
		data, e := fs.ReadFile(migrationFiles, name)
		if e != nil {
			t.Fatal(e)
		}
		files[name] = &fstest.MapFile{Data: data}
	}
	return files
}
