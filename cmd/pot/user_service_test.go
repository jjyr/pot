package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUserServiceNames(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			home := t.TempDir()
			path, name, label, err := userServiceDefinition(home, goos)
			if err != nil || name == "" || label != userServiceLabel {
				t.Fatalf("new definition: %s %s %v", path, label, err)
			}
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatal("discovery created a service directory")
			}
		})
	}
}
