package assistant

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jjyr/pot/pkg/assistantavatar"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

// ProfileUpdate changes only supplied fields. Avatar is a normalized PNG data URL;
// an empty value restores the default Companion.
type ProfileUpdate struct {
	Name   *string `json:"name,omitempty"`
	Avatar *string `json:"avatar,omitempty"`
}

func (p ProfileUpdate) Validate() error {
	if p.Name == nil && p.Avatar == nil {
		return errors.New("profile update requires at least one field")
	}
	if p.Name != nil {
		if err := ValidateName(*p.Name); err != nil {
			return err
		}
	}
	if p.Avatar != nil && *p.Avatar != "" {
		return ValidateAvatar(*p.Avatar)
	}
	return nil
}

// ProfileReceipt is checked before resolving a mutable or deleted source file.
func (r *Registry) ProfileReceipt(ctx context.Context, request, hash string) (*Definition, error) {
	var stored, response string
	err := r.db.QueryRowContext(ctx, "SELECT input_hash,response FROM assistant_profile_requests WHERE id=?", request).Scan(&stored, &response)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if stored != hash {
		return nil, errors.New("conflicting assistant profile request")
	}
	var definition Definition
	if err = core.DecodeJSON([]byte(response), &definition); err != nil {
		return nil, err
	}
	return &definition, nil
}

// UpdateProfileOnce atomically updates identity and saves the original receipt.
// A retry cannot overwrite later edits. The hash binds owner and source arguments.
func (r *Registry) UpdateProfileOnce(ctx context.Context, owner string, patch ProfileUpdate, request, hash string) (Definition, error) {
	if err := patch.Validate(); err != nil {
		return Definition{}, err
	}
	var definition Definition
	err := storage.Transaction(ctx, r.db, func(tx *sql.Tx) error {
		var stored, response string
		err := tx.QueryRowContext(ctx, "SELECT input_hash,response FROM assistant_profile_requests WHERE id=?", request).Scan(&stored, &response)
		if err == nil {
			if stored != hash {
				return errors.New("conflicting assistant profile request")
			}
			return core.DecodeJSON([]byte(response), &definition)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err = tx.QueryRowContext(ctx, "SELECT id,name,revision,request_id,COALESCE((SELECT avatar FROM assistant_profiles WHERE assistant_id=assistant_definitions.id),'') FROM assistant_definitions WHERE id=? AND deleted=0", owner).Scan(&definition.ID, &definition.Name, &definition.Revision, &definition.RequestID, &definition.Avatar); err != nil {
			return err
		}
		if definition.Revision >= core.MaxSafeInteger {
			return errors.New("assistant revision exhausted")
		}
		if patch.Name != nil {
			definition.Name = *patch.Name
		}
		if patch.Avatar != nil {
			definition.Avatar = *patch.Avatar
			if definition.Avatar == "" {
				definition.Avatar, err = assistantavatar.Default(definition.RequestID)
				if err != nil {
					return err
				}
			}
		}
		definition.Revision++
		if _, err = tx.ExecContext(ctx, "UPDATE assistant_definitions SET name=?,revision=? WHERE id=?", definition.Name, definition.Revision, owner); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO assistant_profiles VALUES (?,?) ON CONFLICT(assistant_id) DO UPDATE SET avatar=excluded.avatar", owner, definition.Avatar); err != nil {
			return err
		}
		body, err := json.Marshal(definition)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO assistant_profile_requests VALUES (?,?,?)", request, hash, string(body))
		return err
	})
	return definition, err
}
