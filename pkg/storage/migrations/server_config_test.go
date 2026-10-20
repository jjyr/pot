package migrations

import (
	"strings"
	"testing"
)

func TestRetiredOwnerListenerSettingsUpgradeOnce(t *testing.T) {
	raw := []byte(`{"listen":"0.0.0.0:17701","tls_cert":"/cert","tls_key":"/key","workspace":"/workspace","resources":"/resources"}`)
	upgraded, changed, err := UpgradeServerConfig(raw)
	if err != nil || !changed || strings.Contains(string(upgraded), "listen") || strings.Contains(string(upgraded), "tls_") {
		t.Fatal(string(upgraded), changed, err)
	}
	if !strings.Contains(string(upgraded), "/workspace") || !strings.Contains(string(upgraded), "/resources") {
		t.Fatal(string(upgraded))
	}
	again, changed, err := UpgradeServerConfig(upgraded)
	if err != nil || changed || string(again) != string(upgraded) {
		t.Fatal(string(again), changed, err)
	}
}
