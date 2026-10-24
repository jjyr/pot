package agenthost

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"

	"github.com/jjyr/pot/pkg/peer"
)

// A host key is shared by its message connections, not derived from an assistant
// ID or regenerated at each chat. Only its public identity is stored in metadata.
func (h *agentHost) loadPeerIdentity(ctx context.Context) error {
	var public, ref string
	if err := h.db.QueryRowContext(ctx, "SELECT peer_public_key,peer_secret_ref FROM agent_adapter_host WHERE singleton=1").Scan(&public, &ref); err != nil {
		return err
	}
	if public == "" && ref == "" {
		pub, key, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return err
		}
		defer clear(key)
		secretRef := "codex-host-key-" + identifier()
		if err = h.secrets.Put(ctx, secretRef, key); err != nil {
			return err
		}
		if _, err = h.db.ExecContext(ctx, "UPDATE agent_adapter_host SET peer_public_key=?,peer_secret_ref=? WHERE singleton=1 AND peer_public_key='' AND peer_secret_ref=''", base64.RawURLEncoding.EncodeToString(pub), secretRef); err != nil {
			return err
		}
		if err = h.db.QueryRowContext(ctx, "SELECT peer_public_key,peer_secret_ref FROM agent_adapter_host WHERE singleton=1").Scan(&public, &ref); err != nil {
			return err
		}
	}
	if public == "" || ref == "" {
		return errors.New("codex_peer_identity_missing_restore_original_key")
	}
	raw, err := h.secrets.Get(ctx, ref)
	if err != nil {
		return err
	}
	defer clear(raw)
	if len(raw) != ed25519.PrivateKeySize {
		return errors.New("codex_peer_identity_invalid")
	}
	key := ed25519.PrivateKey(raw)
	seed := key.Seed()
	defer clear(seed)
	reconstructed := ed25519.NewKeyFromSeed(seed)
	defer clear(reconstructed)
	if !bytes.Equal(key, reconstructed) || base64.RawURLEncoding.EncodeToString(key.Public().(ed25519.PublicKey)) != public {
		return errors.New("codex_peer_identity_mismatch_restore_original_key")
	}
	h.peerIdentity = peer.NewIdentity(h.identity.Address.NodeID, key.Public().(ed25519.PublicKey))
	if err = h.peerIdentity.Validate(); err != nil {
		return err
	}
	h.peerKey = append(ed25519.PrivateKey(nil), key...)
	return nil
}
