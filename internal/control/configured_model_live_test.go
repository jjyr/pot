package control

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
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
