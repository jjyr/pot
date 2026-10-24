package assistant

import (
	"testing"

	"github.com/jjyr/pot/pkg/storage"
)

func openMailbox(t *testing.T, path, node string) (*Mailbox, error) {
	t.Helper()
	db, err := storage.OpenDatabase(path, storage.TasksDatabase)
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { db.Close() })
	return OpenMailbox(db, node, MailboxOptions{})
}
