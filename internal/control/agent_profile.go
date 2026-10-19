package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/protocol"
)

type avatarSource struct {
	Path   string `json:"path" required:"true"`
	TaskID string `json:"task_id,omitempty"`
}
type agentProfileUpdate struct {
	Name   *string       `json:"name,omitempty"`
	Avatar *avatarSource `json:"avatar,omitempty"`
}

func (s *Service) updateAgentProfile(ctx context.Context, owner, request string, args json.RawMessage) (json.RawMessage, error) {
	var input agentProfileUpdate
	if err := core.DecodeJSON(args, &input); err != nil {
		return nil, err
	}
	if input.Name == nil && input.Avatar == nil {
		return nil, errors.New("profile update requires at least one field")
	}
	if input.Name != nil {
		if err := assistant.ValidateName(*input.Name); err != nil {
			return nil, err
		}
	}
	hash, err := core.HashJSON([]any{owner, input})
	if err != nil {
		return nil, err
	}
	receipt, err := s.Registry.ProfileReceipt(ctx, request, hash)
	if err != nil {
		return nil, err
	}
	if receipt != nil {
		return profileResult(*receipt)
	}
	patch := assistant.ProfileUpdate{Name: input.Name}
	if input.Avatar != nil {
		avatar := ""
		source := *input.Avatar
		if source.Path == "" && source.TaskID != "" {
			return nil, errors.New("avatar reset cannot select a Task")
		}
		if source.Path != "" {
			raw, err := s.readAvatarSource(ctx, owner, source)
			if err != nil {
				return nil, err
			}
			avatar, err = assistant.NormalizeAvatar(raw)
			if err != nil {
				return nil, err
			}
		}
		patch.Avatar = &avatar
	}
	definition, err := s.Registry.UpdateProfileOnce(ctx, owner, patch, request, hash)
	if err != nil {
		return nil, err
	}
	s.Changes.Notify("assistants")
	return profileResult(definition)
}

func profileResult(d assistant.Definition) (json.RawMessage, error) {
	// Keep raster bytes out of model history and tool receipts in the Agent loop.
	return json.Marshal(map[string]any{"id": d.ID, "name": d.Name, "revision": d.Revision, "has_avatar": d.Avatar != ""})
}

func (s *Service) readAvatarSource(ctx context.Context, owner string, source avatarSource) ([]byte, error) {
	if !protocol.ValidFilePath(source.Path) {
		return nil, errors.New("avatar path must be relative to its workspace")
	}
	if source.TaskID != "" {
		var raw []byte
		revision := ""
		for {
			chunk, err := s.ReadTaskFileChunk(ctx, owner, source.TaskID, source.Path, uint64(len(raw)), revision)
			if err != nil {
				return nil, err
			}
			if chunk.Size > assistant.AvatarSourceLimit {
				return nil, errors.New("avatar source exceeds 8 MiB")
			}
			data, err := base64.StdEncoding.DecodeString(chunk.Data)
			if err != nil {
				return nil, err
			}
			raw = append(raw, data...)
			revision = chunk.Revision
			if uint64(len(raw)) == chunk.Size {
				return raw, nil
			}
		}
	}
	directory, err := s.ExecutionWorkspace(owner)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(source.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > assistant.AvatarSourceLimit {
		return nil, errors.New("avatar source must be a regular file at most 8 MiB")
	}
	return io.ReadAll(io.LimitReader(f, assistant.AvatarSourceLimit+1))
}
