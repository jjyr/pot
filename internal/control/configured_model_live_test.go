package control

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jjyr/pot/internal/gateway"
	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/core"
)

// Existing configuration is read only; decrypted credentials stay on the
// configured machine and must never be logged or copied into evidence.
func configuredLiveModel(t *testing.T, ctx context.Context, production string) (core.Profile, gateway.Provider, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(production, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config profileFile
	if err = json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if len(config.Profiles) == 0 {
		t.Fatal("no configured model")
	}
	profile := config.Profiles[0]
	db, err := sql.Open("sqlite", "file:"+filepath.Join(production, "control.sqlite")+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var metadata string
	if err = db.QueryRowContext(ctx, "SELECT metadata FROM providers WHERE id=?", profile.Provider).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	var provider gateway.Provider
	if err = json.Unmarshal([]byte(metadata), &provider); err != nil {
		t.Fatal(err)
	}
	key, err := (secrets.SystemKey{Account: "reference-live"}).Key(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	var nonce, encrypted, tag []byte
	if err = db.QueryRowContext(ctx, "SELECT nonce,ciphertext,tag FROM secrets WHERE id=?", provider.SecretRef).Scan(&nonce, &encrypted, &tag); err != nil {
		t.Fatal(err)
	}
	credential, err := aead.Open(nil, nonce, append(encrypted, tag...), []byte(provider.SecretRef))
	if err != nil {
		t.Fatal("provider credential authentication failed")
	}
	t.Cleanup(func() { clear(credential) })
	return profile, provider, credential
}

// Pi is opt-in and only its selected DeepSeek provider is read. The source key
// remains local; isolated Control stores encrypt it using a disposable memory key.
func notificationLiveModel(t *testing.T, ctx context.Context) (core.Profile, gateway.Provider, []byte) {
	t.Helper()
	if os.Getenv("POT_LIVE") != "1" {
		t.Skip("requires POT_LIVE=1")
	}
	if production := os.Getenv("POT_CONFIGURED_LIVE_ROOT"); production != "" {
		return configuredLiveModel(t, ctx, production)
	}
	root, model := os.Getenv("POT_PI_LIVE_ROOT"), os.Getenv("POT_PI_LIVE_MODEL")
	if root == "" || model == "" {
		t.Skip("requires a configured Control root or explicit Pi root and model")
	}
	raw, err := os.ReadFile(filepath.Join(root, "models.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer clear(raw)
	var config struct {
		Providers map[string]struct {
			BaseURL string `json:"baseUrl"`
			API     string `json:"api"`
			APIKey  string `json:"apiKey"`
			Models  []struct {
				ID string `json:"id"`
			} `json:"models"`
		} `json:"providers"`
	}
	if json.Unmarshal(raw, &config) != nil {
		t.Fatal("invalid Pi model configuration")
	}
	selected := config.Providers["deepseek"]
	found := false
	for _, candidate := range selected.Models {
		found = found || candidate.ID == model
	}
	if !found || selected.API != "openai-completions" {
		t.Fatal("selected Pi DeepSeek model or protocol unavailable")
	}
	key := selected.APIKey
	if strings.HasPrefix(key, "!") {
		t.Fatal("command-backed Pi keys are not supported by this fixture")
	}
	if value, ok := os.LookupEnv(key); ok {
		key = value
	}
	if key == "" {
		t.Fatal("Pi DeepSeek credential missing")
	}
	credential := []byte(key)
	t.Cleanup(func() { clear(credential) })
	reasoning := "high"
	return core.Profile{Model: model, Params: core.Parameters{ReasoningEffort: &reasoning}}, gateway.Provider{Endpoint: selected.BaseURL, Protocol: "openai_chat"}, credential
}
