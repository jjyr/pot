package webassets

import (
	"io/fs"
	"strings"
	"testing"
)

func TestFrontendIncludesChatCacheMigration(t *testing.T) {
	data, err := fs.ReadFile(FS(), "pkg/storage/migrations/ui-cache/0001_chat_cache.mjs")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "migrateChatCache") {
		t.Fatal("frontend lacks the IndexedDB migration")
	}
	if _, err := fs.ReadFile(FS(), "pkg/storage/migrations/control/0001_baseline.sql"); err == nil {
		t.Fatal("backend database migrations must not be served to the frontend")
	}
}
