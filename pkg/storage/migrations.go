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
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jjyr/pot/pkg/assistantavatar"
	"github.com/jjyr/pot/pkg/storage/migrations"
	migrate "github.com/rubenv/sql-migrate"
	"modernc.org/sqlite"
)

// Historical migrations retain their exact renderer. New conversions use the
// current character catalog. Registration precedes every storage connection.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("pot_idle_model_spec", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		raw, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("model timeout upgrade requires JSON text")
		}
		return migrations.UpgradeModelIdleSpec(raw)
	})
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
	// Validate existing files through a read-only connection before OpenSQLite's
	// journal PRAGMAs. Rejected pre-release databases keep their bytes and history.
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		existing, err := OpenDatabaseReadOnly(path, kind)
		if err != nil {
			return nil, err
		}
		definitions, err := loadMigrations(migrationFiles, kind)
		if err == nil {
			_, err = checkMigrationHistory(context.Background(), existing, kind, definitions)
		}
		closeErr := existing.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	} else if err != nil && !os.IsNotExist(err) {
		return nil, err
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
// Every pending file and its history receipt commit in one database transaction.
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
	// BEGIN IMMEDIATE also serializes independent handles/processes. Recheck
	// history inside this transaction, rather than planning outside the write lock.
	tx, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer tx.Close()
	// Pin one physical connection and issue the lock explicitly, including for
	// caller-owned handles that were not opened with OpenSQLite's _txlock option.
	defer tx.ExecContext(context.Background(), "ROLLBACK")
	if _, err := tx.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	complete, err := checkMigrationHistory(ctx, tx, kind, definitions)
	if err != nil {
		return err
	}
	if complete {
		_, err := tx.ExecContext(ctx, "COMMIT")
		return err
	}
	if _, err := tx.ExecContext(ctx, migrationLedger); err != nil {
		return err
	}
	var applied int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM pot_migrations").Scan(&applied); err != nil {
		return err
	}
	// Definitions, stable IDs and statement parsing remain owned by sql-migrate.
	// Its ExecContext commits per file; this adapter applies its parsed Up plan
	// inside the one transaction required by product startup.
	for _, definition := range definitions[applied:] {
		for _, query := range definition.Up {
			if _, err := tx.ExecContext(ctx, query); err != nil {
				return fmt.Errorf("migrate %s database at %s: %w", kind, definition.Id, err)
			}
		}
		if _, err := tx.ExecContext(ctx, "INSERT INTO pot_migrations(id,applied_at) VALUES(?,?)", definition.Id, time.Now()); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, "COMMIT")
	return err
}

// checkMigrationHistory only reads. An empty receipt table can remain after a
// failed first migration; it is safe to retry only if no product objects exist.
type migrationReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

//go:embed migrations/ledger.sql
var migrationLedger string

func checkMigrationHistory(ctx context.Context, db migrationReader, kind Database, definitions []*migrate.Migration) (bool, error) {
	exists, err := tableExists(ctx, db, "pot_migrations")
	if err != nil {
		return false, err
	}
	if exists {
		if err := checkMigrationLedger(ctx, db); err != nil {
			return false, err
		}
		// Require an exact prefix: disallow downgrade, edited applied files,
		// another database kind and holes instead of sql-migrate's catch-up mode.
		rows, err := db.QueryContext(ctx, "SELECT id FROM pot_migrations ORDER BY id")
		if err != nil {
			return false, err
		}
		index := 0
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return false, err
			}
			if index >= len(definitions) || id != definitions[index].Id {
				rows.Close()
				return false, fmt.Errorf("%s database migration history mismatch at %q: unsupported pre-release database, newer database, changed migration or missing predecessor; back up the complete root and use a new root (see docs/database-migrations.md)", kind, id)
			}
			index++
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return false, err
		}
		if index == len(definitions) {
			return true, nil
		}
		if index == 0 {
			if err := checkEmptyDatabase(ctx, db, kind); err != nil {
				return false, err
			}
		}
	} else if err := checkEmptyDatabase(ctx, db, kind); err != nil {
		return false, err
	}
	return false, nil
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
		for _, query := range definition.Up {
			if err := validateMigrationSQL(query); err != nil {
				return nil, fmt.Errorf("%s: %w", entry.Name(), err)
			}
		}
		definitions = append(definitions, definition)
	}
	if len(definitions) == 0 {
		return nil, fmt.Errorf("%s has no migrations", kind)
	}
	return definitions, nil
}

func tableExists(ctx context.Context, db migrationReader, table string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name=?)", table).Scan(&exists)
	return exists, err
}

// This one-time pre-release reset deliberately does not adopt any old schema,
// even one whose final tables resemble the baseline. Future versions upgrade
// only an exact prefix of this new migration lineage.
func checkEmptyDatabase(ctx context.Context, db migrationReader, kind Database) error {
	var objects, userVersion, applicationID int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%' AND name != 'pot_migrations'").Scan(&objects); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&userVersion); err != nil {
		return err
	}
	if err := db.QueryRowContext(ctx, "PRAGMA application_id").Scan(&applicationID); err != nil {
		return err
	}
	if objects != 0 || userVersion != 0 || applicationID != 0 {
		return fmt.Errorf("%s database has unsupported pre-release or untracked schema; back up the complete root and use a new root (see docs/database-migrations.md)", kind)
	}
	return nil
}

// The receipt table is fixed updater metadata, not an independently migratable
// product schema. Refuse drift before trusting IDs or applying any batch.
func checkMigrationLedger(ctx context.Context, db migrationReader) error {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(pot_migrations)")
	if err != nil {
		return err
	}
	defer rows.Close()
	type column struct {
		name, kind        string
		required, primary int
	}
	var columns []column
	for rows.Next() {
		var cid int
		var c column
		var defaultValue any
		if err := rows.Scan(&cid, &c.name, &c.kind, &c.required, &defaultValue, &c.primary); err != nil {
			return err
		}
		c.kind = strings.ToUpper(c.kind)
		columns = append(columns, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	if len(columns) != 2 || columns[0] != (column{"id", "VARCHAR(255)", 1, 1}) || columns[1] != (column{"applied_at", "DATETIME", 0, 0}) {
		return fmt.Errorf("unsupported migration ledger layout; do not edit receipts; for unsupported pre-release data, back up the complete root and use a new root (see docs/database-migrations.md)")
	}
	var triggers int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='trigger' AND tbl_name='pot_migrations'").Scan(&triggers); err != nil {
		return err
	}
	if triggers != 0 {
		return fmt.Errorf("unsupported migration ledger triggers; do not edit receipts; for unsupported pre-release data, back up the complete root and use a new root (see docs/database-migrations.md)")
	}
	return nil
}

// LedgerDigest binds the fixed receipt DDL independently of product lineage.
func LedgerDigest() string { return fmt.Sprintf("%x", sha256.Sum256([]byte(migrationLedger))) }
