package serverconfig

import (
	"github.com/jjyr/pot/pkg/instance"
	"os"
	"path/filepath"
	"testing"
)

func TestServerSettingsRemainPrivateAndRequireCompleteTLS(t *testing.T) {
	root := t.TempDir()
	os.Chmod(root, 0700)
	initialized, err := instance.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	initialized.Close()
	settings := Settings{Listen: "127.0.0.1:17701", TLSCert: "/tmp/certificate.pem", TLSKey: "/tmp/private.pem"}
	if err := Save(root, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil || loaded != settings {
		t.Fatal(loaded, err)
	}
	info, _ := os.Stat(filepath.Join(root, "server.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("settings not private")
	}
	settings.TLSKey = ""
	if err = Save(root, settings); err == nil {
		t.Fatal("partial TLS accepted")
	}
	os.Chmod(filepath.Join(root, "server.json"), 0644)
	if _, err = Load(root); err == nil {
		t.Fatal("public settings accepted")
	}
}
