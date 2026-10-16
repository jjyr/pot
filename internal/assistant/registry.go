// Package assistant owns assistant identities, instructions, conversations and
// durable peer mail. Runtime and Control inject execution and authority ports.
package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/storage"
)

type Definition struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Revision  uint64 `json:"revision"`
	RequestID string `json:"request_id"`
}
type Registry struct {
	db   *sql.DB
	root string
}

func OpenRegistry(path, root string) (*Registry, error) {
	if !core.Identifier(root) {
		return nil, errors.New("invalid root assistant identity")
	}
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	err = storage.Transaction(context.Background(), db, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS assistant_registry_identity(singleton INTEGER PRIMARY KEY CHECK(singleton=1),root TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_definitions(sequence INTEGER PRIMARY KEY AUTOINCREMENT,id TEXT NOT NULL UNIQUE,name TEXT NOT NULL,revision INTEGER NOT NULL,request_id TEXT NOT NULL UNIQUE,created_name TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS assistant_initial(id TEXT PRIMARY KEY, binding TEXT NOT NULL, spec TEXT NOT NULL,rules TEXT NOT NULL);`); err != nil {
			return err
		}
		if _, err := tx.Exec("INSERT OR IGNORE INTO assistant_registry_identity VALUES (1,?)", root); err != nil {
			return err
		}
		var stored string
		if err := tx.QueryRow("SELECT root FROM assistant_registry_identity WHERE singleton=1").Scan(&stored); err != nil {
			return err
		}
		if stored != root {
			return errors.New("assistant registry belongs to another root")
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Registry{db, root}, nil
}
func (r *Registry) Close() error { return r.db.Close() }
func ValidateName(name string) error {
	if !utf8.ValidString(name) || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 80 {
		return errors.New("invalid assistant name")
	}
	for _, char := range name {
		if unicode.IsControl(char) {
			return errors.New("invalid assistant name")
		}
	}
	return nil
}
func (r *Registry) Definitions(ctx context.Context) ([]Definition, error) {
	rows, err := r.db.QueryContext(ctx, "SELECT id,name,revision,request_id FROM assistant_definitions ORDER BY sequence")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Definition{}
	for rows.Next() {
		var d Definition
		if err := rows.Scan(&d.ID, &d.Name, &d.Revision, &d.RequestID); err != nil {
			return nil, err
		}
		if err := validateDefinition(d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
func validateDefinition(d Definition) error {
	if !core.Identifier(d.ID) || d.RequestID == "" || len(d.RequestID) > 128 || d.Revision == 0 || d.Revision > core.MaxSafeInteger {
		return errors.New("invalid assistant identity")
	}
	return ValidateName(d.Name)
}
func (r *Registry) Create(ctx context.Context, request, name string) (Definition, error) {
	return r.CreateConfigured(ctx, request, name, "", "", nil)
}

// CreateConfigured binds the entire creation input before publishing identity.
// Initial configuration is immutable and only applied to a new conversation.
func (r *Registry) CreateConfigured(ctx context.Context, request, name, profile, rules string, resolve func() (*core.ResolvedSpec, error)) (Definition, error) {
	binding, err := core.HashJSON([]string{name, profile, rules})
	if err != nil {
		return Definition{}, err
	}
	if len(rules) > 262144 {
		return Definition{}, errors.New("assistant instructions exceed 256 KiB")
	}

	if request == "" || len(request) > 128 {
		return Definition{}, errors.New("invalid assistant creation request")
	}
	if err := ValidateName(name); err != nil {
		return Definition{}, err
	}
	var definition Definition
	err = storage.Transaction(ctx, r.db, func(tx *sql.Tx) error {
		var createdName string
		err := tx.QueryRowContext(ctx, "SELECT id,name,revision,request_id,created_name FROM assistant_definitions WHERE request_id=?", request).Scan(&definition.ID, &definition.Name, &definition.Revision, &definition.RequestID, &createdName)
		if err == nil {
			var stored string
			if err := tx.QueryRowContext(ctx, "SELECT binding FROM assistant_initial WHERE id=?", definition.ID).Scan(&stored); err != nil {
				return err
			}
			if createdName != name || stored != binding {
				return errors.New("conflicting assistant creation request")
			}
			return validateDefinition(definition)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM assistant_definitions").Scan(&count); err != nil {
			return err
		}
		if count >= 64 {
			return errors.New("assistant limit reached")
		}
		id := storage.ID()
		if count == 0 {
			id = r.root
		}
		definition = Definition{id, name, 1, request}
		var spec *core.ResolvedSpec
		if resolve != nil {
			spec, err = resolve()
			if err != nil {
				return err
			}
		}
		data, err := json.Marshal(spec)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO assistant_initial VALUES(?,?,?,?)", id, binding, string(data), rules); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO assistant_definitions(id,name,revision,request_id,created_name) VALUES (?,?,?,?,?)", id, name, 1, request, name)
		return err
	})
	return definition, err
}
func (r *Registry) Rename(ctx context.Context, id, name string, revision uint64) (Definition, error) {
	if err := ValidateName(name); err != nil {
		return Definition{}, err
	}
	if revision == 0 || revision >= core.MaxSafeInteger {
		return Definition{}, errors.New("invalid assistant revision")
	}
	var definition Definition
	err := storage.Transaction(ctx, r.db, func(tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, "UPDATE assistant_definitions SET name=?,revision=revision+1 WHERE id=? AND revision=?", name, id, revision)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return errors.New("assistant missing or changed; reload before saving")
		}
		return tx.QueryRowContext(ctx, "SELECT id,name,revision,request_id FROM assistant_definitions WHERE id=?", id).Scan(&definition.ID, &definition.Name, &definition.Revision, &definition.RequestID)
	})
	return definition, err
}

func (r *Registry) Initial(ctx context.Context, id string) (*core.ResolvedSpec, string, error) {
	var data, rules string
	if err := r.db.QueryRowContext(ctx, "SELECT spec,rules FROM assistant_initial WHERE id=?", id).Scan(&data, &rules); err != nil {
		return nil, "", err
	}
	var spec *core.ResolvedSpec
	if err := core.DecodeJSON([]byte(data), &spec); err != nil {
		return nil, "", err
	}
	if spec != nil {
		if err := spec.Validate(); err != nil {
			return nil, "", err
		}
	}
	return spec, rules, nil
}
