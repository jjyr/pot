package migrations

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestProfileCacheTTLUpgrade(t *testing.T) {
	for _, version := range []string{"1", "2"} {
		raw := []byte(`{"version":` + version + `,"snapshot":{"profiles":[{"auth_ref":"unchanged","model_settings":{"idle_compact_minutes":20,"catalog_provider":"custom"}}]},"requests":{"save":{"input_hash":"original","profile":{"model_settings":{"idle_compact_minutes":9}}}}}`)
		body, changed, err := UpgradeProfileCacheTTL(raw)
		if err != nil || !changed {
			t.Fatal(err, changed)
		}
		var file struct {
			Version  int
			Snapshot struct {
				Profiles []struct {
					AuthRef  string         `json:"auth_ref"`
					Settings map[string]any `json:"model_settings"`
				}
			}
			Requests map[string]struct {
				InputHash string `json:"input_hash"`
				Profile   struct {
					Settings map[string]any `json:"model_settings"`
				}
			}
		}
		if err = json.Unmarshal(body, &file); err != nil {
			t.Fatal(err)
		}
		p := file.Snapshot.Profiles[0]
		if file.Version != 3 || p.AuthRef != "unchanged" || p.Settings["catalog_provider"] != "custom" || file.Requests["save"].InputHash != "original" {
			t.Fatal(string(body))
		}
		if _, old := p.Settings["idle_compact_minutes"]; old {
			t.Fatal("kept retired setting")
		}
		if version == "2" && (p.Settings["prompt_cache_minutes"] != float64(20) || file.Requests["save"].Profile.Settings["prompt_cache_minutes"] != float64(9)) {
			t.Fatal(string(body))
		}
		next, changed, err := UpgradeProfileCacheTTL(body)
		if err != nil || changed || !bytes.Equal(next, body) {
			t.Fatal("non-idempotent", err)
		}
	}
	if _, _, err := UpgradeProfileCacheTTL([]byte(`{"version":4}`)); err == nil {
		t.Fatal("accepted unknown format")
	}
}
