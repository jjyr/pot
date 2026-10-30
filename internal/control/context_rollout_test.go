package control

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/jjyr/pot/pkg/rollout"
	"github.com/jjyr/pot/pkg/storage"
)

// Read a live conversation checkpoint without opening another Store, which
// would perform startup recovery against the active conversation. Keep its SQL
// cursor stable while validating and reconstructing the associated rollout.
func readConversationCheckpoint(ctx context.Context, db *sql.DB, databasePath string) (json.RawMessage, error) {
	var body json.RawMessage
	err := storage.Transaction(ctx, db, func(tx *sql.Tx) error {
		var ref rollout.Ref
		if err := tx.QueryRowContext(ctx, "SELECT sequence,hash FROM context_rollout WHERE singleton=1 AND imported=1").Scan(&ref.Sequence, &ref.Hash); err != nil {
			return err
		}
		path := filepath.Join(filepath.Dir(databasePath), "sessions", strings.TrimSuffix(filepath.Base(databasePath), filepath.Ext(databasePath))+".jsonl")
		var err error
		body, err = rollout.Load(path, ref)
		return err
	})
	return body, err
}
