package agenthost

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexPeerIdentityEncryptedAndStableAcrossRestart(t *testing.T) {
	h, root := testHost(t)
	identity := h.peerIdentity
	key := append(ed25519.PrivateKey(nil), h.peerKey...)
	defer clear(key)
	owner := h.token
	var public, ref string
	if err := h.db.QueryRow("SELECT peer_public_key,peer_secret_ref FROM agent_adapter_host").Scan(&public, &ref); err != nil {
		t.Fatal(err)
	}
	if ref == "" || public != base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey)) {
		t.Fatal("invalid public identity metadata")
	}
	encrypted, err := os.ReadFile(filepath.Join(root, "control.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted, key) || bytes.Contains(encrypted, key.Seed()) {
		t.Fatal("private identity leaked into SQLite plaintext")
	}
	raw, err := h.secrets.Get(context.Background(), ref)
	if err != nil || !bytes.Equal(raw, key) {
		t.Fatal("encrypted private key is unavailable", err)
	}
	clear(raw)
	h.close()
	h, err = openAgentHost(root)
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	if h.peerIdentity != identity || !bytes.Equal(h.peerKey, key) || h.token != owner {
		t.Fatal("restart changed host identity or owner credential")
	}
}
func TestCodexPeerIdentityNeverRegeneratesMissingOrChangedKey(t *testing.T) {
	for _, test := range []string{"missing-secret", "changed-public"} {
		t.Run(test, func(t *testing.T) {
			h, root := testHost(t)
			var ref string
			if err := h.db.QueryRow("SELECT peer_secret_ref FROM agent_adapter_host").Scan(&ref); err != nil {
				t.Fatal(err)
			}
			if test == "missing-secret" {
				if err := h.secrets.Delete(context.Background(), ref); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := h.db.Exec("UPDATE agent_adapter_host SET peer_public_key='changed'"); err != nil {
					t.Fatal(err)
				}
			}
			h.close()
			replacement, err := openAgentHost(root)
			if err == nil {
				replacement.close()
				t.Fatal("silently regenerated an existing host identity")
			}
		})
	}
}
