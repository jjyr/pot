package control

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/jjyr/pot/internal/assistant"
	"github.com/jjyr/pot/pkg/assistantavatar"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/storage"
)

// Existing execution/recovery fixtures intentionally retain their historical
// main identity. New-creation tests use Registry.Create and generated slug IDs.
func legacyMainAssistant(ctx context.Context, s *Service, request, name string) (assistant.Definition, error) {
	var exists bool
	if err := s.integrationDB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM assistant_definitions WHERE request_id=?)", request).Scan(&exists); err != nil {
		return assistant.Definition{}, err
	}
	if exists {
		return s.Registry.Create(ctx, request, name)
	}
	character := assistantavatar.ForName(name, request)
	avatar, err := assistantavatar.Render(character, request)
	if err != nil {
		return assistant.Definition{}, err
	}
	binding, err := core.HashJSON([]any{name, "", "", json.RawMessage(nil), ""})
	if err != nil {
		return assistant.Definition{}, err
	}
	err = storage.Transaction(ctx, s.integrationDB, func(tx *sql.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{"INSERT INTO assistant_definitions(id,request_id) VALUES('main',?)", []any{request}},
			{"INSERT INTO assistant_initial VALUES('main',?,'null')", []any{binding}},
			{"INSERT INTO assistant_initial_intent VALUES('main','null')", nil},
			{"INSERT INTO assistant_welcome VALUES('main','')", nil},
			{"INSERT INTO assistant_file_export VALUES('main',?,?,?,?,'')", []any{name, avatar, character, request}},
		} {
			if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return assistant.Definition{}, err
	}
	if err := storage.ExportAssistantFiles(s.integrationDB, s.options.Root); err != nil {
		return assistant.Definition{}, err
	}
	definitions, err := s.Registry.Definitions(ctx)
	for _, d := range definitions {
		if d.ID == "main" {
			return d, err
		}
	}
	return assistant.Definition{}, err
}
