package webassets

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

func TestNotificationMigrationIsServedWithoutBackendSchema(t *testing.T) {
	body, err := fs.ReadFile(FS(), "pkg/storage/migrations/ui-notifications/0001_installation.mjs")
	if err != nil || !strings.Contains(string(body), "migrateNotificationInstallation") {
		t.Fatal(string(body), err)
	}
	if _, err = fs.ReadFile(FS(), "pkg/storage/migrations/control/0004_system_notifications.sql"); err == nil {
		t.Fatal("notification SQL exposed to browser")
	}
}

func TestCompleteVersionedShell(t *testing.T) {
	body, err := fs.ReadFile(FS(), "service-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	const prefix = "const SHELL="
	text := string(body)
	start := strings.Index(text, prefix) + len(prefix)
	end := strings.Index(text[start:], ";\n") + start
	var shell struct {
		Version string
		Assets  []struct {
			URL    string
			SHA256 string
		}
	}
	if err := json.Unmarshal([]byte(text[start:end]), &shell); err != nil {
		t.Fatal(err)
	}
	if len(shell.Version) != 64 || len(shell.Assets) < 100 {
		t.Fatal("incomplete shell", shell.Version, len(shell.Assets))
	}
	found := map[string]bool{}
	for _, asset := range shell.Assets {
		data, err := fs.ReadFile(FS(), strings.TrimPrefix(asset.URL, "/"))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != asset.SHA256 {
			t.Fatal("wrong asset hash", asset.URL)
		}
		found[asset.URL] = true
	}
	for _, name := range []string{"/index.html", "/errors.js", "/app.js", "/components/ui.js", "/styles.css", "/assets/IBMPlexSans-Regular.ttf", "/locales/server.json", "/pkg/storage/migrations/ui-cache/0006_startup_identity.mjs", "/pkg/storage/migrations/ui-cache/0008_resource_bytes.mjs", "/resource-document.html", "/state/resource-bytes.mjs", "/state/resource-reader.mjs"} {
		if !found[name] {
			t.Fatal("startup dependency missing", name)
		}
	}
	info, err := fs.Stat(FS(), "service-worker.js")
	if err != nil || info.Size() != int64(len(body)) {
		t.Fatal("wrong generated content length", err)
	}
}
