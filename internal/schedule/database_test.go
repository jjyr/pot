package schedule

import (
	"testing"

	"github.com/jjyr/pot/internal/tasks"
	"github.com/jjyr/pot/pkg/storage"
)

func openTasks(t *testing.T, path string, options tasks.Options) (*tasks.Service, error) {
	t.Helper()
	db, err := storage.OpenDatabase(path, storage.TasksDatabase)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { db.Close() })
	return tasks.Open(db, options)
}
