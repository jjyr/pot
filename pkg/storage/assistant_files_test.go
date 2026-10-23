package storage

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/pkg/assistantavatar"
)

func pendingAssistantFiles(t *testing.T) (string, *sql.DB, []byte) {
	t.Helper()
	root := t.TempDir()
	db, err := OpenSQLite(filepath.Join(root, "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(t.Context(), db, ControlDatabase); err != nil {
		t.Fatal(err)
	}
	avatar, _ := assistantavatar.Render("glass", "custom")
	_, err = db.Exec(`INSERT INTO assistant_definitions(id,request_id) VALUES('main','original');
 INSERT INTO assistant_initial VALUES('main','immutable-binding','null');
 INSERT INTO assistant_initial_intent VALUES('main','null');
 INSERT INTO assistant_welcome VALUES('main','original welcome')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("INSERT INTO assistant_file_export VALUES('main','My existing helper',?,'glass','custom','initial rules')", avatar); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "assistants", "main")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	conversation, err := OpenSQLite(filepath.Join(directory, "conversation.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(t.Context(), conversation, ConversationDatabase); err != nil {
		t.Fatal(err)
	}
	rules, _ := json.Marshal(Rules{1, "current edited rules", "old-revision"})
	if _, err := conversation.Exec("INSERT INTO assistant_instruction_file_export VALUES(1,?)", string(rules)); err != nil {
		t.Fatal(err)
	}
	if _, err := conversation.Exec("INSERT INTO metadata VALUES('unchanged.history','keep')"); err != nil {
		t.Fatal(err)
	}
	conversation.Close()
	if err := Migrate(t.Context(), db, ControlDatabase); err != nil {
		t.Fatal(err)
	}
	data, _ := base64.StdEncoding.DecodeString(strings.TrimPrefix(avatar, "data:image/png;base64,"))
	return root, db, data
}

func TestAssistantFilesExportPreservesDataAndRetiresDBContent(t *testing.T) {
	root, db, avatar := pendingAssistantFiles(t)
	if err := ExportAssistantFiles(db, root); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "assistants", "main")
	profile, revision, _, err := ReadAssistantProfile(directory)
	if err != nil || profile.Name != "My existing helper" || revision == 0 {
		t.Fatal(profile, revision, err)
	}
	if data, err := os.ReadFile(filepath.Join(directory, "avatar.png")); err != nil || !bytes.Equal(data, avatar) {
		t.Fatal("custom avatar changed", err)
	}
	rules, err := ReadAssistantInstructions(directory)
	if err != nil || rules.Text != "current edited rules" {
		t.Fatal("initial rules overwrote current rules", rules, err)
	}
	var count int
	if err := db.QueryRow("SELECT count(*) FROM assistant_file_export").Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
	for _, table := range []string{"assistant_profiles", "assistant_profile_requests"} {
		if _, err := db.Query("SELECT * FROM " + table); err == nil {
			t.Fatal("retired table survives", table)
		}
	}
	if _, err := db.Query("SELECT name,revision,created_name FROM assistant_definitions"); err == nil {
		t.Fatal("live profile values remain in registry")
	}
	if _, err := db.Query("SELECT rules FROM assistant_initial"); err == nil {
		t.Fatal("initial rules remain in registry")
	}
	conversation, err := OpenDatabase(filepath.Join(directory, "conversation.sqlite"), ConversationDatabase)
	if err != nil {
		t.Fatal(err)
	}
	defer conversation.Close()
	if err := conversation.QueryRow("SELECT count(*) FROM metadata WHERE key='assistant.instructions.v1'").Scan(&count); err != nil || count != 0 {
		t.Fatal("old rule record survives", count, err)
	}
	if err := conversation.QueryRow("SELECT count(*) FROM assistant_instruction_file_export").Scan(&count); err != nil || count != 0 {
		t.Fatal("exported rule copy survives", count, err)
	}
	if _, err := conversation.Query("SELECT * FROM assistant_instruction_updates"); err == nil {
		t.Fatal("retired instruction proposal table survives")
	}
	var history string
	if err := conversation.QueryRow("SELECT value FROM metadata WHERE key='unchanged.history'").Scan(&history); err != nil || history != "keep" {
		t.Fatal("history changed", history, err)
	}
	if err := WriteAssistantFile(filepath.Join(directory, "AGENTS.md"), []byte("manual edit")); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := Migrate(context.Background(), db, ControlDatabase); err != nil {
			t.Fatal(err)
		}
		if err := ExportAssistantFiles(db, root); err != nil {
			t.Fatal(err)
		}
	}
	rules, _ = ReadAssistantInstructions(directory)
	if rules.Text != "manual edit" {
		t.Fatal("repeated startup restored DB content")
	}
}

func TestAssistantFilesExportConflictRetryAndInterruptedReceipt(t *testing.T) {
	root, db, _ := pendingAssistantFiles(t)
	directory := filepath.Join(root, "assistants", "main")
	path := filepath.Join(directory, "profile.json")
	if err := os.WriteFile(path, []byte("manual file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ExportAssistantFiles(db, root); err == nil {
		t.Fatal("overwrote conflicting profile")
	}
	var pending int
	if err := db.QueryRow("SELECT count(*) FROM assistant_file_export").Scan(&pending); err != nil || pending != 1 {
		t.Fatal("failed export lost source data", pending, err)
	}
	os.Remove(path)
	// Simulate a crash after the conversation export committed but before the
	// registry's file-publication receipt. Retrying must keep its current rules.
	conversation, err := OpenDatabase(filepath.Join(directory, "conversation.sqlite"), ConversationDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if err := ExportAssistantInstructions(conversation, directory); err != nil {
		t.Fatal(err)
	}
	conversation.Close()
	if err := ExportAssistantFiles(db, root); err != nil {
		t.Fatal(err)
	}
	rules, _ := ReadAssistantInstructions(directory)
	if rules.Text != "current edited rules" {
		t.Fatal("retry replaced exported current rules with initial rules", rules)
	}
}
