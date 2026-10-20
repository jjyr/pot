package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/jjyr/pot/pkg/assistantavatar"
	migrate "github.com/rubenv/sql-migrate"
	"modernc.org/sqlite"
)

// Historical migrations retain their exact renderer. New conversions use the
// current character catalog. Registration precedes every storage connection.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("pot_default_avatar", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		seed, ok := args[0].(string)
		if !ok || seed == "" {
			return nil, fmt.Errorf("default avatar requires a creation seed")
		}
		return legacyDefaultAvatar(seed)
	})
	sqlite.MustRegisterDeterministicScalarFunction("pot_avatar_character", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		name, _ := args[0].(string)
		seed, _ := args[1].(string)
		return assistantavatar.ForName(name, seed), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("pot_character_avatar", 2, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		character, _ := args[0].(string)
		seed, _ := args[1].(string)
		return assistantavatar.Render(character, seed)
	})
}

// Database identifies a physical database schema, independently of its filename.
// Multiple services sharing a file must select the same Database.
type Database string

const (
	ControlDatabase      Database = "control"
	TasksDatabase        Database = "tasks"
	RunnerDatabase       Database = "runner"
	ConversationDatabase Database = "conversation"
	MachinesDatabase     Database = "machines"
	UsageDatabase        Database = "usage"
	OperationsDatabase   Database = "operations"
)

//go:embed migrations/*/*.sql
var migrationFiles embed.FS

// sql-migrate plans before opening its per-migration transaction. Serialize
// same-process callers (some stores share files through independent handles).
// Product roots additionally hold the existing exclusive process lock.
var migrationMu sync.Mutex

var migrationFilename = regexp.MustCompile(`^([0-9]{4})_[a-z][a-z0-9_]*\.sql$`)

// OpenDatabase is the product database entry point. No caller receives a handle
// until all pending migrations have committed. OpenSQLite is the low-level
// connection primitive for tests and external callers managing their own schema.
func OpenDatabase(path string, kind Database) (*sql.DB, error) {
	if !knownDatabase(kind) {
		return nil, fmt.Errorf("unknown database kind %q", kind)
	}
	db, err := OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	if err = Migrate(context.Background(), db, kind); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// Migrate upgrades a caller-owned database through the same centralized schemas.
// Use before opening transactions or starting receivers on an existing handle.
// Each SQL file and its history receipt commit in one SQLite transaction.
func Migrate(ctx context.Context, db *sql.DB, kind Database) error {
	return migrateDatabase(ctx, db, kind, migrationFiles)
}

func migrateDatabase(ctx context.Context, db *sql.DB, kind Database, files fs.FS) error {
	if !knownDatabase(kind) {
		return fmt.Errorf("unknown database kind %q", kind)
	}
	definitions, err := loadMigrations(files, kind)
	if err != nil {
		return err
	}
	migrationMu.Lock()
	defer migrationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	exists, err := tableExists(ctx, db, "pot_migrations")
	if err != nil {
		return err
	}
	if exists {
		// Require an exact prefix: disallow downgrade, edited applied files,
		// another database kind and holes instead of sql-migrate's catch-up mode.
		rows, err := db.QueryContext(ctx, "SELECT id FROM pot_migrations ORDER BY id")
		if err != nil {
			return err
		}
		index := 0
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if index >= len(definitions) || id != definitions[index].Id {
				rows.Close()
				return fmt.Errorf("%s database migration history mismatch at %q: newer database, changed migration or missing predecessor", kind, id)
			}
			index++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if index == len(definitions) {
			return nil
		}
		if index == 0 {
			if err := checkLegacyDatabase(ctx, db, kind); err != nil {
				return err
			}
		}
	} else if err := checkLegacyDatabase(ctx, db, kind); err != nil {
		return err
	}
	set := migrate.MigrationSet{TableName: "pot_migrations"}
	_, err = set.ExecContext(ctx, db, "sqlite3", &migrate.MemoryMigrationSource{Migrations: definitions}, migrate.Up)
	if err != nil {
		return fmt.Errorf("migrate %s database: %w", kind, err)
	}
	return nil
}

func knownDatabase(kind Database) bool {
	switch kind {
	case ControlDatabase, TasksDatabase, RunnerDatabase, ConversationDatabase, MachinesDatabase, UsageDatabase, OperationsDatabase:
		return true
	}
	return false
}

func loadMigrations(files fs.FS, kind Database) ([]*migrate.Migration, error) {
	directory := "migrations/" + string(kind)
	entries, err := fs.ReadDir(files, directory)
	if err != nil {
		return nil, err
	}
	definitions := make([]*migrate.Migration, 0, len(entries))
	for index, entry := range entries {
		match := migrationFilename.FindStringSubmatch(entry.Name())
		if entry.IsDir() || match == nil {
			return nil, fmt.Errorf("invalid migration file %s/%s", directory, entry.Name())
		}
		version, _ := strconv.Atoi(match[1])
		if version != index+1 {
			return nil, fmt.Errorf("%s migrations must be contiguous from 0001", kind)
		}
		data, err := fs.ReadFile(files, directory+"/"+entry.Name())
		if err != nil {
			return nil, err
		}
		// Persist the checksum in sql-migrate's ID so its normal history rejects
		// changed files without maintaining a second, non-atomic checksum table.
		id := fmt.Sprintf("%s_%s_%x", strings.TrimSuffix(entry.Name(), ".sql"), kind, sha256.Sum256(data))
		definition, err := migrate.ParseMigration(id, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", id, err)
		}
		if len(definition.Up) == 0 || len(definition.Down) != 0 || definition.DisableTransactionUp {
			return nil, fmt.Errorf("%s must contain transactional Up SQL only", entry.Name())
		}
		definitions = append(definitions, definition)
	}
	if len(definitions) == 0 {
		return nil, fmt.Errorf("%s has no migrations", kind)
	}
	return definitions, nil
}

func tableExists(ctx context.Context, db *sql.DB, table string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)", table).Scan(&exists)
	return exists, err
}

// The baseline adopts the last pre-migration format. Earlier formats were
// already unsupported and must not be relabeled as successfully migrated.
func checkLegacyDatabase(ctx context.Context, db *sql.DB, kind Database) error {
	versions := map[string]int{}
	switch kind {
	case TasksDatabase:
		versions = map[string]int{"task_schema": 8, "schedule_schema": 2}
		var legacy bool
		if err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name IN ('assistant_mail','task_inputs','task_notifications'))").Scan(&legacy); err != nil {
			return err
		}
		if legacy {
			return fmt.Errorf("unsupported legacy agent message storage")
		}
	case RunnerDatabase:
		versions = map[string]int{"runner_schema": 6}
	case ConversationDatabase:
		var version int
		if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version > 1 {
			return fmt.Errorf("unsupported legacy conversation schema %d", version)
		}
	}
	for table, expected := range versions {
		exists, err := tableExists(ctx, db, table)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		var count, minimum, maximum int
		// table names are fixed constants above, never caller-supplied SQL.
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(MIN(version),0),COALESCE(MAX(version),0) FROM "+table).Scan(&count, &minimum, &maximum); err != nil {
			return err
		}
		if count != 1 || minimum != expected || maximum != expected {
			return fmt.Errorf("unsupported legacy %s: expected one version %d row", table, expected)
		}
	}
	return nil
}
