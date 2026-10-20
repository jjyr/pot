package migrations

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestProfileCompactionUpgradePreservesRoutesAndReceipts(t *testing.T) {
	raw := []byte(`{"version":1,"snapshot":{"profiles":[{"id":"a","auth_ref":"secret:reference","model_settings":{"catalog_provider":"custom","input_modalities":{"m":["text"]}}}],"connections":[{"endpoint":"https://fixture.test"}],"configured_models":{"a":["m"]}},"requests":{"receipt":{"input_hash":"unchanged","profile":{"id":"a","model_settings":null}}}}`)
	upgraded, changed, err := UpgradeProfileCompaction(raw)
	if err != nil || !changed {
		t.Fatal(err, changed)
	}
	var value map[string]any
	_ = json.Unmarshal(upgraded, &value)
	snapshot := value["snapshot"].(map[string]any)
	profile := snapshot["profiles"].([]any)[0].(map[string]any)
	settings := profile["model_settings"].(map[string]any)
	_, hasIdleTiming := settings["idle_compact_minutes"]
	if profile["auth_ref"] != "secret:reference" || settings["catalog_provider"] != "custom" || settings["input_modalities"] == nil || !hasIdleTiming {
		t.Fatal(value)
	}
	receipt := value["requests"].(map[string]any)["receipt"].(map[string]any)
	if receipt["input_hash"] != "unchanged" || receipt["profile"].(map[string]any)["model_settings"] == nil {
		t.Fatal(receipt)
	}
	next, changed, err := UpgradeProfileCompaction(upgraded)
	if err != nil || changed || !bytes.Equal(next, upgraded) {
		t.Fatal("upgrade not idempotent", err)
	}
	if _, _, err = UpgradeProfileCompaction([]byte(`{"version":3}`)); err == nil {
		t.Fatal("accepted unknown version")
	}
}
