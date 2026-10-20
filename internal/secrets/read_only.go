package secrets

import (
	"context"
	"errors"

	"github.com/jjyr/pot/pkg/storage"
)

// OpenReadOnly opens existing encrypted credentials without initializing or
// upgrading the owning database. Reads never permit KeySource key creation;
// SQLite also rejects Put and Delete through this handle.
func OpenReadOnly(path string, keys KeySource) (*Store, error) {
	if keys == nil {
		return nil, errors.New("secret KeySource is required")
	}
	db, err := storage.OpenDatabaseReadOnly(path, storage.ControlDatabase)
	if err != nil {
		return nil, err
	}
	return &Store{db, readOnlyKeySource{keys}}, nil
}

type readOnlyKeySource struct{ KeySource }

func (k readOnlyKeySource) Key(ctx context.Context, _ bool) ([]byte, error) {
	return k.KeySource.Key(ctx, false)
}
