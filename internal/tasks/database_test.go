package tasks

import (
	"path/filepath"
	"testing"

	"github.com/jjyr/pot/pkg/storage"
)

func openTasks(t *testing.T, path string, options Options) (*Service, error) {
	t.Helper()
	db, err := storage.OpenDatabase(path, storage.TasksDatabase)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { db.Close() })
	return Open(db, options)
}

func openJournal(t *testing.T, path, node, boot string) (*Journal, error) {
	t.Helper()
	db, err := storage.OpenDatabase(path, storage.TasksDatabase)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { db.Close() })
	return OpenJournal(db, filepath.Join(filepath.Dir(path), "cache", "resources"), node, boot)
}
