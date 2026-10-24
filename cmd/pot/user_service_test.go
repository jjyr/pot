package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUserServiceNamingCompatibility(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			home := t.TempDir()
			path, name, label, err := userServiceDefinition(home, goos)
			if err != nil || strings.Contains(name, "pot-go") || label != userServiceLabel {
				t.Fatalf("new definition: %s %s %v", path, label, err)
			}
			if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
				t.Fatal("discovery created a service directory")
			}
			legacyName := "pot-go-runtime.service"
			if goos == "darwin" {
				legacyName = "com.jjyr.pot.go.runtime.plist"
			}
			legacy := filepath.Join(filepath.Dir(path), legacyName)
			if err := os.MkdirAll(filepath.Dir(legacy), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(legacy, []byte("operator configuration"), 0600); err != nil {
				t.Fatal(err)
			}
			selected, selectedName, selectedLabel, err := userServiceDefinition(home, goos)
			if err != nil || selected != legacy || selectedName != legacyName {
				t.Fatalf("legacy: %s %v", selected, err)
			}
			if goos == "darwin" && selectedLabel != "com.jjyr.pot.go.runtime" {
				t.Fatal(selectedLabel)
			}
			if err := os.WriteFile(path, []byte("duplicate"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, _, _, err := userServiceDefinition(home, goos); err == nil {
				t.Fatal("duplicate supervisors accepted")
			}
			body, _ := os.ReadFile(legacy)
			if string(body) != "operator configuration" {
				t.Fatal("legacy file modified")
			}
		})
	}
}
