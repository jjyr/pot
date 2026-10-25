package selfupdate

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jjyr/pot/pkg/storage"
)

func TestFixtureSchemas(t *testing.T) {
	if path := os.Getenv("POT_UPDATE_SCHEMA_OUTPUT"); path != "" {
		schemas, err := storage.Schemas()
		if err != nil {
			t.Fatal(err)
		}
		mustWrite(t, path, schemas)
	}
}

// Compile a real target with one additional embedded SQL migration. Production
// schema files and published artifacts remain untouched. Both binaries execute
// the real startup runner against private SQLite roots.
func buildMigratingFixture(t *testing.T) (string, storage.SchemaCatalog) {
	t.Helper()
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	copyRoot := t.TempDir()
	for _, directory := range []string{"internal", "pkg"} {
		err := filepath.WalkDir(filepath.Join(repo, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(repo, path)
			dest := filepath.Join(copyRoot, rel)
			if entry.IsDir() {
				return os.MkdirAll(dest, 0700)
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(dest, body, 0600)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		body, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(copyRoot, name), body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	// Selfupdate imports only these product packages; no web/native host is built.
	sql := `-- +migrate Up
-- +migrate StatementBegin
CREATE TABLE update_fixture_receipt(id TEXT PRIMARY KEY,value INTEGER);
INSERT INTO update_fixture_receipt VALUES('converted',1);
INSERT INTO fixture_ready VALUES('converted');
-- +migrate StatementEnd
`
	if err := os.WriteFile(filepath.Join(copyRoot, "pkg/storage/migrations/tasks/0005_update_fixture.sql"), []byte(sql), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(copyRoot, "target-test")
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-c", "-o", binary, "./internal/selfupdate")
	command.Dir = copyRoot
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build target: %v\n%s", err, out)
	}
	catalog := filepath.Join(copyRoot, "schemas.json")
	command = exec.CommandContext(ctx, binary, "-test.run=^TestFixtureSchemas$")
	command.Env = append(os.Environ(), "POT_UPDATE_SCHEMA_OUTPUT="+catalog)
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("target schemas: %v\n%s", err, out)
	}
	var schemas storage.SchemaCatalog
	body, err := os.ReadFile(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &schemas); err != nil {
		t.Fatal(err)
	}
	return binary, schemas
}

func TestWorkerRealMigrationAndForwardRecovery(t *testing.T) {
	binary, targetSchemas := buildMigratingFixture(t)
	for _, scenario := range []string{"success", "sql-failure", "readiness-failure", "partial-roots"} {
		t.Run(scenario, func(t *testing.T) {
			base := t.TempDir()
			install := filepath.Join(base, "pot")
			directory := install + ".updates"
			old := Installation{Version: "0.1.0", Kind: "cli", Platform: "linux-amd64", StorageFormat: storageFormat(), Protocol: UpdateProtocol}
			copyTestBinary(t, filepath.Join(install, "bin/pot"))
			mustWrite(t, filepath.Join(install, "Resources/update.json"), old)
			stage := filepath.Join(directory, "stage-0.2.0/pot")
			target := old
			target.Version = "0.2.0"
			if err := os.MkdirAll(filepath.Join(stage, "bin"), 0700); err != nil {
				t.Fatal(err)
			}
			source, err := os.Open(binary)
			if err != nil {
				t.Fatal(err)
			}
			dest, err := os.OpenFile(filepath.Join(stage, "bin/pot"), os.O_CREATE|os.O_WRONLY, 0700)
			if err != nil {
				t.Fatal(err)
			}
			_, err = io.Copy(dest, source)
			source.Close()
			dest.Close()
			if err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(stage, "Resources/update.json"), target)
			var roots []string
			log, err := os.Create(filepath.Join(base, "host.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			count := 1
			if scenario == "partial-roots" {
				count = 2
			}
			for i := range count {
				root := filepath.Join(base, []string{"a-root", "b-root"}[i])
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				roots = append(roots, root)
				db, err := storage.OpenDatabase(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
				if err != nil {
					t.Fatal(err)
				}
				if scenario != "sql-failure" && !(scenario == "partial-roots" && i == 1) {
					if _, err := db.Exec("CREATE TABLE fixture_ready(value TEXT PRIMARY KEY)"); err != nil {
						t.Fatal(err)
					}
				}
				db.Close()
				receipts, err := storage.OpenDatabase(filepath.Join(root, "operations.sqlite"), storage.OperationsDatabase)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := receipts.Exec("INSERT INTO operations VALUES('effect','original','done',X'01','')"); err != nil {
					t.Fatal(err)
				}
				receipts.Close()
				process := exec.Command(filepath.Join(install, "bin/pot"), "-test.run=^TestHostProcess$")
				process.Env = append(os.Environ(), "POT_UPDATE_TEST_ROOT="+root, "POT_UPDATE_TEST_MIGRATE=1")
				if scenario == "readiness-failure" {
					process.Env = append(process.Env, "POT_UPDATE_TEST_FAIL=1")
				}
				process.Stdout, process.Stderr = log, log
				if err := process.Start(); err != nil {
					t.Fatal(err)
				}
				defer stopTestUpdateHost(t, process, log.Name())
			}
			waitFixture := func(predicate func() bool) {
				t.Helper()
				deadline := time.Now().Add(12 * time.Second)
				for time.Now().Before(deadline) {
					if predicate() {
						return
					}
					time.Sleep(20 * time.Millisecond)
				}
				body, _ := os.ReadFile(log.Name())
				t.Fatalf("fixture timeout\n%s", body)
			}
			waitFixture(func() bool {
				peers, _ := participants(directory)
				if len(peers) != count {
					return false
				}
				for _, p := range peers {
					if !p.Ready {
						return false
					}
				}
				return true
			})
			initialBoot := fixtureReady(roots[0]).Nonce
			r := eligibleRelease(t, "0.2.0")
			r.Schemas = targetSchemas
			r.Artifacts = []Artifact{{Kind: "cli", Platform: "linux-amd64", URL: releaseOrigin + "v0.2.0/fixture.tar.gz", SHA256: strings.Repeat("ab", 32), Size: 1}}
			feed, _ := signed(t, *r)
			if err := os.WriteFile(filepath.Join(directory, "verified-feed.json"), feed, 0600); err != nil {
				t.Fatal(err)
			}
			mustWrite(t, filepath.Join(directory, "prepared.json"), struct{ Version, Path string }{"0.2.0", stage})
			mustWrite(t, filepath.Join(directory, "state.json"), State{Version: "0.2.0", Phase: "installing"})
			mustWrite(t, filepath.Join(directory, "job.json"), Job{Install: install, Directory: directory, Version: "0.2.0", Package: old})
			before := healthTimeout
			healthTimeout = time.Second
			defer func() { healthTimeout = before }()
			err = Worker(filepath.Join(directory, "job.json"))
			m := New(t.Context(), &Host{Directory: directory}, nil)
			state := m.state()
			if scenario == "success" {
				if err != nil || state.Phase != "current" {
					t.Fatal(state, err)
				}
			} else if scenario == "sql-failure" {
				if err == nil || state.Phase != "failed" {
					t.Fatal("uncommitted failure did not roll back package", state, err)
				}
				waitFixture(func() bool { return fixtureRestarted(roots[0], initialBoot, "0.1.0") })
			} else {
				if err == nil || state.Phase != "blocked" {
					t.Fatal("committed migration did not fence old binary", state, err)
				}
				var installed Installation
				body, _ := os.ReadFile(filepath.Join(install, "Resources/update.json"))
				json.Unmarshal(body, &installed)
				if installed.Version != "0.2.0" {
					t.Fatal("old package restored over committed schema")
				}
				for _, root := range roots {
					lock, e := openLock(filepath.Join(root, "runtime.lock"), syscall.LOCK_EX|syscall.LOCK_NB)
					if e != nil {
						t.Fatal("blocked new host still holds database lock", e)
					}
					lock.Close()
				}
			}
			if paths, _ := filepath.Glob(filepath.Join(directory, "data-*")); len(paths) != 0 {
				t.Fatal("migration created database snapshots", paths)
			}
			for i, root := range roots {
				receipts, e := storage.OpenDatabaseReadOnly(filepath.Join(root, "operations.sqlite"), storage.OperationsDatabase)
				if e != nil {
					t.Fatal(e)
				}
				var state string
				var n int
				if e := receipts.QueryRow("SELECT count(*),state FROM operations WHERE id='effect'").Scan(&n, &state); e != nil || n != 1 || state != "done" {
					t.Fatal("effect receipt lost", n, state, e)
				}
				receipts.Close()
				if scenario == "sql-failure" {
					continue
				}
				if scenario == "partial-roots" && i == 1 {
					db, e := storage.OpenDatabaseReadOnly(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
					if e != nil {
						t.Fatal(e)
					}
					var n int
					db.QueryRow("SELECT count(*) FROM pot_migrations").Scan(&n)
					db.Close()
					if n != 4 {
						t.Fatal("failed database partially committed", n)
					}
					continue
				}
				if _, e := storage.OpenDatabase(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase); e == nil {
					t.Fatal("old binary accepted migrated schema")
				}
			}
			if scenario == "partial-roots" {
				// Repair the environmental prerequisite without migrating. Retry
				// the already installed new package through the actual worker.
				command := exec.Command(filepath.Join(install, "bin/pot"), "-test.run=^TestFixtureRepairPrerequisite$")
				command.Env = append(os.Environ(), "POT_UPDATE_FORWARD_ROOT="+roots[1])
				if out, e := command.CombinedOutput(); e != nil {
					t.Fatalf("fixture repair: %v %s", e, out)
				}
				mustWrite(t, filepath.Join(directory, "state.json"), State{Version: "0.2.0", Phase: "installing"})
				mustWrite(t, filepath.Join(directory, "job.json"), Job{Install: install, Directory: directory, Version: "0.2.0", Package: target, Retry: true})
				command = exec.Command(filepath.Join(install, "bin/pot"), "_update-worker", filepath.Join(directory, "job.json"))
				if out, e := command.CombinedOutput(); e != nil {
					t.Fatalf("forward worker retry: %v %s", e, out)
				}
				if current := m.state(); current.Phase != "current" {
					t.Fatal(current)
				}
				for _, root := range roots {
					command := exec.Command(filepath.Join(install, "bin/pot"), "-test.run=^TestFixtureForwardRetry$")
					command.Env = append(os.Environ(), "POT_UPDATE_FORWARD_ROOT="+root)
					if out, e := command.CombinedOutput(); e != nil {
						t.Fatalf("idempotent forward reopen: %v %s", e, out)
					}
				}
			}

		})
	}
}

func TestFixtureRepairPrerequisite(t *testing.T) {
	root := os.Getenv("POT_UPDATE_FORWARD_ROOT")
	if root == "" {
		return
	}
	db, err := storage.OpenSQLite(filepath.Join(root, "tasks.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE fixture_ready(value TEXT PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
}
func TestFixtureForwardRetry(t *testing.T) {
	root := os.Getenv("POT_UPDATE_FORWARD_ROOT")
	if root == "" {
		return
	}
	for range 2 {
		if err := storage.MigrateRoot(context.Background(), root); err != nil {
			t.Fatal(err)
		}
	}
	db, err := storage.OpenDatabaseReadOnly(filepath.Join(root, "tasks.sqlite"), storage.TasksDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM update_fixture_receipt").Scan(&n); err != nil || n != 1 {
		t.Fatal("reentry repeated conversion", n, err)
	}
}
