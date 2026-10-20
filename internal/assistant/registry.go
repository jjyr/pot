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

	"github.com/jjyr/pot/pkg/assistantavatar"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
	"github.com/jjyr/pot/pkg/storage"
)

type Definition struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Revision  uint64 `json:"revision"`
	RequestID string `json:"request_id"`
	Avatar    string `json:"avatar,omitempty"`
}
type Registry struct {
	db   *sql.DB
	root string
}

func OpenRegistry(path, root string) (*Registry, error) {
	if !core.Identifier(root) {
		return nil, errors.New("invalid root assistant identity")
	}
	db, err := storage.OpenDatabase(path, storage.ControlDatabase)
	if err != nil {
		return nil, err
	}
	err = storage.Transaction(context.Background(), db, func(tx *sql.Tx) error {
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
	rows, err := r.db.QueryContext(ctx, "SELECT id,name,revision,request_id,COALESCE((SELECT avatar FROM assistant_profiles WHERE assistant_id=assistant_definitions.id),'') FROM assistant_definitions WHERE deleted=0 ORDER BY sequence")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Definition{}
	for rows.Next() {
		var d Definition
		if err := rows.Scan(&d.ID, &d.Name, &d.Revision, &d.RequestID, &d.Avatar); err != nil {
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
	return r.CreateWithIntent(ctx, request, name, profile, rules, nil, "", false, resolve)
}

// CreateWithIntent binds creation intent before publishing the assistant identity.
// An empty name allocates an unused kitchen name inside the creation transaction.
// The welcome template's {name} marker is filled once with the allocated name.
func (r *Registry) CreateWithIntent(ctx context.Context, request, name, bindingInput, rules string, intent json.RawMessage, welcome string, onlyFirst bool, resolve func() (*core.ResolvedSpec, error)) (Definition, error) {
	binding, err := core.HashJSON([]any{name, bindingInput, rules, intent, welcome})
	if err != nil {
		return Definition{}, err
	}
	if len(rules) > 262144 {
		return Definition{}, errors.New("assistant instructions exceed 256 KiB")
	}

	if request == "" || len(request) > 128 {
		return Definition{}, errors.New("invalid assistant creation request")
	}
	if name != "" {
		if err := ValidateName(name); err != nil {
			return Definition{}, err
		}
	}
	var definition Definition
	err = storage.Transaction(ctx, r.db, func(tx *sql.Tx) error {
		if onlyFirst {
			err := tx.QueryRowContext(ctx, "SELECT id,name,revision,request_id,COALESCE((SELECT avatar FROM assistant_profiles WHERE assistant_id=assistant_definitions.id),'') FROM assistant_definitions WHERE deleted=0 ORDER BY sequence LIMIT 1").Scan(&definition.ID, &definition.Name, &definition.Revision, &definition.RequestID, &definition.Avatar)
			if err == nil {
				return validateDefinition(definition)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		var createdName string
		var deleted bool
		err := tx.QueryRowContext(ctx, "SELECT id,name,revision,request_id,created_name,COALESCE((SELECT avatar FROM assistant_profiles WHERE assistant_id=assistant_definitions.id),''),deleted FROM assistant_definitions WHERE request_id=?", request).Scan(&definition.ID, &definition.Name, &definition.Revision, &definition.RequestID, &createdName, &definition.Avatar, &deleted)
		if err == nil {
			if deleted {
				return errors.New("assistant creation request belongs to a deleted assistant")
			}
			var stored string
			if err := tx.QueryRowContext(ctx, "SELECT binding FROM assistant_initial WHERE id=?", definition.ID).Scan(&stored); err != nil {
				return err
			}
			if (name != "" && createdName != name) || stored != binding {
				return errors.New("conflicting assistant creation request")
			}
			return validateDefinition(definition)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM assistant_definitions WHERE deleted=0").Scan(&count); err != nil {
			return err
		}
		if count >= 64 {
			return errors.New("assistant limit reached")
		}
		if name == "" {
			rows, err := tx.QueryContext(ctx, "SELECT name FROM assistant_definitions WHERE deleted=0")
			if err != nil {
				return err
			}
			var used []string
			for rows.Next() {
				var value string
				if err := rows.Scan(&value); err != nil {
					rows.Close()
					return err
				}
				used = append(used, value)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return err
			}
			name = randomAssistantName(used)
		}
		id := storage.ID()
		var historical int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM assistant_definitions").Scan(&historical); err != nil {
			return err
		}
		if historical == 0 {
			id = r.root
		}
		definition = Definition{ID: id, Name: name, Revision: 1, RequestID: request}
		character := assistantavatar.ForName(name, request)
		definition.Avatar, err = assistantavatar.Render(character, request)
		if err != nil {
			return err
		}
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
		intentBody := string(intent)
		if len(intent) == 0 {
			intentBody = "null"
		} else if !json.Valid(intent) {
			return errors.New("invalid initial configuration intent")
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO assistant_initial_intent VALUES (?,?)", id, intentBody); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO assistant_welcome VALUES (?,?)", id, strings.ReplaceAll(welcome, "{name}", name)); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO assistant_definitions(id,name,revision,request_id,created_name) VALUES (?,?,?,?,?)", id, name, 1, request, name)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO assistant_profiles(assistant_id,avatar,character) VALUES (?,?,?)", id, definition.Avatar, character)
		return err
	})
	return definition, err
}
func (r *Registry) Welcome(ctx context.Context, id string) (string, error) {
	var text string
	err := r.db.QueryRowContext(ctx, "SELECT text FROM assistant_welcome WHERE id=?", id).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return text, err
}

func (r *Registry) Rename(ctx context.Context, id, name string, revision uint64) (Definition, error) {
	return r.UpdateProfile(ctx, id, ProfileUpdate{Name: &name}, revision)
}

// UpdateProfile applies a settings edit under the same identity revision as chat
// profile changes, so an outdated settings page cannot overwrite a newer edit.
func (r *Registry) UpdateProfile(ctx context.Context, id string, patch ProfileUpdate, revision uint64) (Definition, error) {
	if err := patch.Validate(); err != nil {
		return Definition{}, err
	}
	if revision == 0 || revision >= core.MaxSafeInteger {
		return Definition{}, errors.New("invalid assistant revision")
	}
	var definition Definition
	err := storage.Transaction(ctx, r.db, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT id,name,revision,request_id,COALESCE((SELECT avatar FROM assistant_profiles WHERE assistant_id=assistant_definitions.id),'') FROM assistant_definitions WHERE id=? AND revision=? AND deleted=0", id, revision).Scan(&definition.ID, &definition.Name, &definition.Revision, &definition.RequestID, &definition.Avatar)
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("assistant missing or changed; reload before saving")
		}
		if err != nil {
			return err
		}
		if patch.Name != nil {
			definition.Name = *patch.Name
		}
		if patch.Avatar != nil {
			definition.Avatar = *patch.Avatar
			if definition.Avatar == "" {
				definition.Avatar, err = defaultAvatar(ctx, tx, id)
				if err != nil {
					return err
				}
			}
		}
		definition.Revision++
		if _, err = tx.ExecContext(ctx, "UPDATE assistant_definitions SET name=?,revision=? WHERE id=?", definition.Name, definition.Revision, id); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE assistant_profiles SET avatar=? WHERE assistant_id=?", definition.Avatar, id)
		return err
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

func (r *Registry) InitialIntent(ctx context.Context, id string) (json.RawMessage, error) {
	var value string
	err := r.db.QueryRowContext(ctx, "SELECT intent FROM assistant_initial_intent WHERE id=?", id).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return json.RawMessage(value), err
}

// HasHistory keeps explicit deletion from triggering first-use bootstrap again.
func (r *Registry) HasHistory(ctx context.Context) (bool, error) {
	var exists bool
	err := r.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM assistant_definitions)").Scan(&exists)
	return exists, err
}

func (r *Registry) Deleted(ctx context.Context, id string) (bool, error) {
	var deleted bool
	err := r.db.QueryRowContext(ctx, "SELECT deleted FROM assistant_definitions WHERE id=?", id).Scan(&deleted)
	return deleted, err
}

// Delete retains the identity and creation receipt so retries cannot resurrect it.
func (r *Registry) Delete(ctx context.Context, id string, revision uint64) error {
	if !core.Identifier(id) || revision == 0 || revision > core.MaxSafeInteger {
		return errors.New("invalid assistant deletion")
	}
	return storage.Transaction(ctx, r.db, func(tx *sql.Tx) error {
		var current uint64
		var deleted bool
		if err := tx.QueryRowContext(ctx, "SELECT revision,deleted FROM assistant_definitions WHERE id=?", id).Scan(&current, &deleted); err != nil {
			return err
		}
		if current != revision {
			return protocol.APIError{Code: "assistant_changed"}
		}
		if deleted {
			return nil
		}
		_, err := tx.ExecContext(ctx, "UPDATE assistant_definitions SET deleted=1 WHERE id=?", id)
		return err
	})
}

// DefaultAvatar returns the originally assigned character and background, even
// after renaming or uploading a custom profile image.
func (r *Registry) DefaultAvatar(ctx context.Context, id string) (string, error) {
	return defaultAvatar(ctx, r.db, id)
}
func defaultAvatar(ctx context.Context, query interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id string) (string, error) {
	var character, seed string
	err := query.QueryRowContext(ctx, "SELECT p.character,d.request_id FROM assistant_profiles p JOIN assistant_definitions d ON d.id=p.assistant_id WHERE d.id=? AND d.deleted=0", id).Scan(&character, &seed)
	if err != nil {
		return "", err
	}
	return assistantavatar.Render(character, seed)
}
