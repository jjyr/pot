package storage

import (
	"context"
	"fmt"
	migrate "github.com/rubenv/sql-migrate"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
)

// SchemaCatalog binds each kind to its ordered checksum-bearing migration IDs.
// It describes paths, not permission to run arbitrary SQL from a manifest.
type SchemaCatalog map[Database][]string

func Schemas() (SchemaCatalog, error) {
	result := SchemaCatalog{}
	for _, kind := range []Database{ControlDatabase, TasksDatabase, RunnerDatabase, ConversationDatabase, MachinesDatabase, UsageDatabase, OperationsDatabase} {
		definitions, err := loadMigrations(migrationFiles, kind)
		if err != nil {
			return nil, err
		}
		for _, definition := range definitions {
			result[kind] = append(result[kind], definition.Id)
		}
	}
	return result, nil
}

func ExtendsSchemas(target, source SchemaCatalog) bool {
	if len(target) != len(source) || len(source) == 0 {
		return false
	}
	for kind, ids := range source {
		for index, id := range target[kind] {
			if !regexp.MustCompile(fmt.Sprintf(`^%04d_[a-z][a-z0-9_]*_%s_[a-f0-9]{64}$`, index+1, regexp.QuoteMeta(string(kind)))).MatchString(id) {
				return false
			}
		}
		next := target[kind]
		if len(ids) == 0 || len(next) < len(ids) || !reflect.DeepEqual(next[:len(ids)], ids) {
			return false
		}
	}
	return true
}

type DatabaseState struct {
	Path    string   `json:"path"`
	Kind    Database `json:"kind"`
	History []string `json:"history"`
}

// InspectRoot reads only the owned database locations, never workspaces or
// arbitrary user files. Callers hold the root's process lock. Unknown files,
// symlinks and unknown history stop an update before a new binary sees data.
func InspectRoot(ctx context.Context, root string) ([]DatabaseState, error) {
	schemas, err := Schemas()
	if err != nil {
		return nil, err
	}
	return InspectRootSchemas(ctx, root, schemas)
}

// InspectRootSchemas validates history against an authenticated target catalog.
// It only reads receipts, and never executes SQL supplied by the caller.
func InspectRootSchemas(ctx context.Context, root string, schemas SchemaCatalog) ([]DatabaseState, error) {
	paths := map[string]Database{}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sqlite") {
			continue
		}
		kind := Database(strings.TrimSuffix(entry.Name(), ".sqlite"))
		if !knownDatabase(kind) || kind == ConversationDatabase {
			return nil, fmt.Errorf("unknown database %s", entry.Name())
		}
		paths[filepath.Join(root, entry.Name())] = kind
	}
	assistants := filepath.Join(root, "assistants")
	if info, err := os.Lstat(assistants); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("invalid assistant data directory")
		}
		entries, err := os.ReadDir(assistants)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("linked assistant data directory")
			}
			if !entry.IsDir() {
				continue
			}
			path := filepath.Join(assistants, entry.Name(), "conversation.sqlite")
			if _, err := os.Lstat(path); err == nil {
				paths[path] = ConversationDatabase
			} else if !os.IsNotExist(err) {
				return nil, err
			}
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	ordered := make([]string, 0, len(paths))
	for path := range paths {
		ordered = append(ordered, path)
	}
	sort.Strings(ordered)
	result := make([]DatabaseState, 0, len(paths))
	for _, path := range ordered {
		kind := paths[path]
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("non-regular database %s", path)
		}
		db, err := OpenDatabaseReadOnly(path, kind)
		if err != nil {
			return nil, err
		}
		definitions := []*migrate.Migration{}
		for _, id := range schemas[kind] {
			definitions = append(definitions, &migrate.Migration{Id: id})
		}
		if len(definitions) == 0 {
			err = fmt.Errorf("missing schema for %s", kind)
		} else {
			_, err = checkMigrationHistory(ctx, db, kind, definitions)
		}
		state := DatabaseState{Path: path, Kind: kind, History: []string{}}
		if err == nil {
			var exists bool
			exists, err = tableExists(ctx, db, "pot_migrations")
			if err == nil && exists {
				rows, e := db.QueryContext(ctx, "SELECT id FROM pot_migrations ORDER BY id")
				err = e
				if err == nil {
					for rows.Next() {
						var id string
						if err = rows.Scan(&id); err != nil {
							break
						}
						state.History = append(state.History, id)
					}
					if err == nil {
						err = rows.Err()
					}
					rows.Close()
				}
			}
		}
		err = firstError(err, db.Close())
		if err != nil {
			return nil, fmt.Errorf("inspect %s: %w", path, err)
		}
		result = append(result, state)
	}
	return result, nil
}

func firstError(a, b error) error {
	if a != nil {
		return a
	}
	return b
}

// MigrateRoot reuses OpenDatabase's transactional runner for every existing
// database, including conversations normally opened lazily. There is no global
// transaction: committed files are skipped on retry after a later file fails.
func MigrateRoot(ctx context.Context, root string) error {
	states, err := InspectRoot(ctx, root)
	if err != nil {
		return err
	}
	for _, state := range states {
		if err := ctx.Err(); err != nil {
			return err
		}
		db, err := OpenDatabase(state.Path, state.Kind)
		if err != nil {
			return err
		}
		if err := db.Close(); err != nil {
			return err
		}
	}
	return nil
}
