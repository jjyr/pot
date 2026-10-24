package agenthost

import (
	"context"
	"database/sql"
	"io"
	"os"
	"strings"

	assistantprofile "github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/agentlink"
	"github.com/jjyr/pot/pkg/assistantavatar"
	"github.com/jjyr/pot/pkg/storage"
)

func validateProfileName(name string) error {
	if assistantprofile.ValidateName(name) != nil {
		return agentlink.ErrInvalid
	}
	return nil
}

func (b *backend) Profile(ctx context.Context, r agentlink.ProfileRequest) (agentlink.Profile, error) {
	a, err := b.resolve(ctx, r.AssistantID)
	if err != nil {
		return agentlink.Profile{}, err
	}
	result := agentlink.Profile{ID: a.ID, Name: a.Name, Avatar: a.Avatar, Revision: a.Revision}
	switch r.Action {
	case "get":
		if r.Name != nil || r.Avatar != nil {
			return result, agentlink.ErrInvalid
		}
		return result, nil
	case "read":
		if r.Name != nil || r.Avatar != nil {
			return result, agentlink.ErrInvalid
		}
		result.Avatar, err = assistantavatar.Render(a.Character, a.ID)
		return result, err
	case "update":
		if r.Name == nil && r.Avatar == nil {
			return result, agentlink.ErrInvalid
		}
	default:
		return result, agentlink.ErrUnsupported
	}
	if r.Name != nil {
		if err = validateProfileName(*r.Name); err != nil {
			return result, err
		}
		result.Name = *r.Name
	}
	if r.Avatar != nil {
		result.Avatar = *r.Avatar
		if result.Avatar == "" {
			result.Avatar, err = assistantavatar.Render(a.Character, a.ID)
		} else {
			err = assistantprofile.ValidateAvatar(result.Avatar)
		}
		if err != nil {
			return result, agentlink.ErrInvalid
		}
	}
	err = storage.Transaction(ctx, b.db, func(tx *sql.Tx) error {
		updated, err := tx.ExecContext(ctx, "UPDATE agent_adapter_assistants SET name=?,avatar=?,revision=revision+1 WHERE id=? AND revision=? AND deleted=0", result.Name, result.Avatar, a.ID, r.Revision)
		if err != nil {
			return err
		}
		count, err := updated.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return agentlink.ErrConflict
		}
		return nil
	})
	result.Revision = r.Revision + 1
	return result, err
}

// MCP image paths are bounded to the selected assistant's existing workspace.
func (b *backend) profileImage(ctx context.Context, id, path string) (string, error) {
	a, err := b.resolve(ctx, id)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", agentlink.ErrInvalid
	}
	root, err := os.OpenRoot(a.Workspace)
	if err != nil {
		return "", err
	}
	defer root.Close()
	file, err := root.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > assistantprofile.AvatarSourceLimit {
		return "", agentlink.ErrInvalid
	}
	raw, err := io.ReadAll(io.LimitReader(file, assistantprofile.AvatarSourceLimit+1))
	if err != nil {
		return "", err
	}
	return assistantprofile.NormalizeAvatar(raw)
}
