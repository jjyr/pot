// Package assistant owns assistant identities, instructions, conversations and
// durable peer mail. Runtime and Control inject execution and authority ports.
package assistant

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
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
	db        *sql.DB
	directory string
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
	directory := filepath.Dir(path)
	unlock := storage.LockAssistantFiles(directory)
	defer unlock()
	if err := storage.ExportAssistantFiles(db, directory); err != nil {
		db.Close()
		return nil, err
	}
	return &Registry{db: db, directory: directory}, nil
}
func (r *Registry) Close() error { return r.db.Close() }

// FirstID resolves an omitted local selector without loading profile images.
// An empty registry has no default assistant.
func (r *Registry) FirstID(ctx context.Context) (string, error) {
	var id string
	err := r.db.QueryRowContext(ctx, "SELECT id FROM assistant_definitions WHERE deleted=0 ORDER BY sequence LIMIT 1").Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

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
func (r *Registry) definition(ctx context.Context, id string) (Definition, error) {
	var d Definition
	if err := r.db.QueryRowContext(ctx, "SELECT id,request_id FROM assistant_definitions WHERE id=? AND deleted=0", id).Scan(&d.ID, &d.RequestID); err != nil {
		return d, err
	}
	profile, revision, avatar, err := storage.ReadAssistantProfile(r.Directory(id))
	if err != nil {
		return d, err
	}
	d.Name, d.Revision, d.Avatar = profile.Name, revision, avatar
	if err := validateDefinition(d); err != nil {
		return d, err
	}
	if err := ValidateAvatar(avatar); err != nil {
		return d, err
	}
	return d, nil
}
func (r *Registry) Directory(id string) string { return filepath.Join(r.directory, "assistants", id) }
func (r *Registry) Definitions(ctx context.Context) ([]Definition, error) {
	unlock := storage.LockAssistantFiles(r.directory)
	defer unlock()
	if err := storage.ExportAssistantFiles(r.db, r.directory); err != nil {
		return nil, err
	}
	rows, err := r.db.QueryContext(ctx, "SELECT id FROM assistant_definitions WHERE deleted=0 ORDER BY sequence")
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	result := []Definition{}
	for _, id := range ids {
		d, err := r.definition(ctx, id)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, nil
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
// Every creation allocates a two-word slug ID inside the creation transaction.
// An empty display name title-cases that slug, including the first assistant.
// The welcome template's {name} marker is filled once with the allocated name.
func (r *Registry) CreateWithIntent(ctx context.Context, request, name, bindingInput, rules string, intent json.RawMessage, welcome string, onlyFirst bool, resolve func() (*core.ResolvedSpec, error)) (Definition, error) {
	unlock := storage.LockAssistantFiles(r.directory)
	defer unlock()
	if err := storage.ExportAssistantFiles(r.db, r.directory); err != nil {
		return Definition{}, err
	}
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
			var id string
			err := tx.QueryRowContext(ctx, "SELECT id FROM assistant_definitions WHERE deleted=0 ORDER BY sequence LIMIT 1").Scan(&id)
			if err == nil {
				definition.ID = id
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		var deleted bool
		err := tx.QueryRowContext(ctx, "SELECT id,deleted FROM assistant_definitions WHERE request_id=?", request).Scan(&definition.ID, &deleted)
		if err == nil {
			if deleted {
				return errors.New("assistant creation request belongs to a deleted assistant")
			}
			var stored string
			if err := tx.QueryRowContext(ctx, "SELECT binding FROM assistant_initial WHERE id=?", definition.ID).Scan(&stored); err != nil {
				return err
			}
			if stored != binding {
				return errors.New("conflicting assistant creation request")
			}
			return nil
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
		var used []string
		rows, err := tx.QueryContext(ctx, "SELECT id,deleted FROM assistant_definitions")
		if err != nil {
			return err
		}
		type registered struct {
			id      string
			deleted bool
		}
		var existing []registered
		for rows.Next() {
			var v registered
			if err := rows.Scan(&v.id, &v.deleted); err != nil {
				rows.Close()
				return err
			}
			existing = append(existing, v)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, v := range existing {
			used = append(used, v.id)
			if !v.deleted {
				p, _, _, err := storage.ReadAssistantProfile(r.Directory(v.id))
				if err != nil {
					return err
				}
				used = append(used, p.Name)
			}
		}
		id, err := randomAssistantSlug(used)
		if err != nil {
			return err
		}
		if name == "" {
			name = slugDisplayName(id)
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
		if _, err = tx.ExecContext(ctx, "INSERT INTO assistant_initial VALUES(?,?,?)", id, binding, string(data)); err != nil {
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
		_, err = tx.ExecContext(ctx, "INSERT INTO assistant_definitions(id,request_id) VALUES (?,?)", id, request)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO assistant_file_export VALUES (?,?,?,?,?,?)", id, name, definition.Avatar, character, request, rules)
		return err
	})
	if err != nil {
		return definition, err
	}
	if err := storage.ExportAssistantFiles(r.db, r.directory); err != nil {
		return definition, err
	}
	return r.definition(ctx, definition.ID)
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

// UpdateProfile checks the current file revision so an outdated settings page
// cannot overwrite a newer profile edit.
func (r *Registry) UpdateProfile(ctx context.Context, id string, patch ProfileUpdate, revision uint64) (Definition, error) {
	if err := patch.Validate(); err != nil {
		return Definition{}, err
	}
	unlock := storage.LockAssistantFiles(r.Directory(id))
	defer unlock()
	d, err := r.definition(ctx, id)
	if err != nil {
		return d, err
	}
	if d.Revision != revision {
		return d, errors.New("assistant missing or changed; reload before saving")
	}
	if patch.Name != nil {
		d.Name = *patch.Name
	}
	if patch.Avatar != nil {
		avatar := *patch.Avatar
		if avatar == "" {
			avatar, err = r.DefaultAvatar(ctx, id)
			if err != nil {
				return d, err
			}
		}
		raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(avatar, avatarPrefix))
		if err != nil {
			return d, err
		}
		if err := storage.WriteAssistantFile(filepath.Join(r.Directory(id), "avatar.png"), raw); err != nil {
			return d, err
		}
	}
	if patch.Name != nil {
		data, _ := json.MarshalIndent(storage.AssistantProfile{SchemaVersion: 1, Name: d.Name}, "", "  ")
		if err := storage.WriteAssistantFile(filepath.Join(r.Directory(id), "profile.json"), append(data, '\n')); err != nil {
			return d, err
		}
	}
	return r.definition(ctx, id)
}

func (r *Registry) Initial(ctx context.Context, id string) (*core.ResolvedSpec, string, error) {
	var data string
	if err := r.db.QueryRowContext(ctx, "SELECT spec FROM assistant_initial WHERE id=?", id).Scan(&data); err != nil {
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
	current, err := storage.ReadAssistantInstructions(r.Directory(id))
	return spec, current.Text, err
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
	unlock := storage.LockAssistantFiles(r.Directory(id))
	defer unlock()
	return storage.Transaction(ctx, r.db, func(tx *sql.Tx) error {
		var deleted bool
		if err := tx.QueryRowContext(ctx, "SELECT deleted FROM assistant_definitions WHERE id=?", id).Scan(&deleted); err != nil {
			return err
		}
		_, current, _, err := storage.ReadAssistantProfile(r.Directory(id))
		if err != nil {
			return err
		}
		if current != revision {
			return protocol.APIError{Code: "assistant_changed"}
		}
		if deleted {
			return nil
		}
		_, err = tx.ExecContext(ctx, "UPDATE assistant_definitions SET deleted=1 WHERE id=?", id)
		return err
	})
}
func (r *Registry) DefaultAvatar(ctx context.Context, id string) (string, error) {
	deleted, err := r.Deleted(ctx, id)
	if err != nil {
		return "", err
	}
	if deleted {
		return "", sql.ErrNoRows
	}
	data, err := storage.ReadAssistantFile(r.Directory(id), "default-avatar.png", AvatarSourceLimit)
	if err != nil {
		return "", err
	}
	return avatarPrefix + base64.StdEncoding.EncodeToString(data), nil
}
