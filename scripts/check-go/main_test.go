package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDatabaseBoundary(t *testing.T) {
	for _, test := range []struct {
		name, path, source, want string
	}{
		{"business DDL", "internal/example/store.go", "package example\nconst query = `CREATE TABLE records(id TEXT)`", "declares schema"},
		{"storage Go DDL", "pkg/storage/example.go", "package storage\nconst query = `ALTER TABLE records ADD COLUMN x TEXT`", "declares schema"},
		{"business raw open", "pkg/example/store.go", "package example\nfunc open() { storage.OpenSQLite(\"db\") }", "must use storage.OpenDatabase"},
		{"business query", "internal/example/store.go", "package example\nconst query = `UPDATE records SET state='running'`", ""},
		{"test fixture", "internal/example/store_test.go", "package example\nconst query = `CREATE TABLE records(id TEXT)`", ""},
		{"storage connection", "pkg/storage/example.go", "package storage\nfunc open() { storage.OpenSQLite(\"db\") }", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			for _, directory := range []string{"cmd", "pkg", "internal", filepath.Dir(test.path)} {
				if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(root, test.path), []byte(test.source), 0600); err != nil {
				t.Fatal(err)
			}
			if err := checkDatabaseBoundary(root); test.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatal(err)
			}
		})
	}
}
