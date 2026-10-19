package storage

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// MigrationKeys supplies the existing encryption key. A conversion must never
// generate or replace it. Secret references and AEAD associated data stay stable.
type MigrationKeys interface {
	Key(context.Context, bool) ([]byte, error)
}

// FinalizeNodeIdentity completes the key-dependent stage before a secret store
// is exposed to its owning services. All writes and the receipt commit together;
// a crash or unavailable key leaves the stage pending and safe to retry.
func FinalizeNodeIdentity(ctx context.Context, db *sql.DB, keys MigrationKeys) error {
	return Transaction(ctx, db, func(tx *sql.Tx) error {
		var pending int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM node_identity_secret_migration").Scan(&pending); err != nil {
			return err
		}
		if pending == 0 {
			return nil
		}
		var count int
		if err := tx.QueryRowContext(ctx, "SELECT (SELECT COUNT(*) FROM agent_link_connections WHERE secret_ref!='' AND (state!='revoked' OR json_extract(metadata,'$.revocation_pending')=1))+(SELECT COUNT(*) FROM agent_link_invitations WHERE claim_hash!='')").Scan(&count); err != nil {
			return err
		}
		if count == 0 {
			_, err := tx.ExecContext(ctx, "DELETE FROM node_identity_secret_migration")
			return err
		}
		if keys == nil {
			return fmt.Errorf("Agent Link identity migration requires the existing encryption key")
		}
		key, err := keys.Key(ctx, false)
		if err != nil {
			return err
		}
		defer clear(key)
		block, err := aes.NewCipher(key)
		if err != nil {
			return err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT DISTINCT secret_ref FROM agent_link_connections WHERE secret_ref!='' AND (state!='revoked' OR json_extract(metadata,'$.revocation_pending')=1)")
		if err != nil {
			return err
		}
		var refs []string
		for rows.Next() {
			var ref string
			if err = rows.Scan(&ref); err != nil {
				rows.Close()
				return err
			}
			refs = append(refs, ref)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, ref := range refs {
			raw, err := readNodeSecret(ctx, tx, aead, ref)
			if err != nil {
				return err
			}
			converted, err := migrateNodeJSON(raw, "credential")
			clear(raw)
			if err != nil {
				return err
			}
			if err = writeNodeSecret(ctx, tx, aead, ref, converted); err != nil {
				clear(converted)
				return err
			}
			clear(converted)
		}
		rows, err = tx.QueryContext(ctx, "SELECT i.id,i.secret_ref,c.metadata FROM agent_link_invitations i JOIN agent_link_connections c ON c.id=i.connection_id WHERE i.claim_hash!=''")
		if err != nil {
			return err
		}
		type claimed struct {
			id, ref string
			raw     []byte
		}
		var claims []claimed
		for rows.Next() {
			var c claimed
			if err = rows.Scan(&c.id, &c.ref, &c.raw); err != nil {
				rows.Close()
				return err
			}
			claims = append(claims, c)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for _, claim := range claims {
			code, err := readNodeSecret(ctx, tx, aead, claim.ref)
			if err != nil {
				return err
			}
			var connection struct {
				ID          string `json:"id"`
				Local, Peer json.RawMessage
				PeerHost    json.RawMessage `json:"peer_host"`
			}
			if err = json.Unmarshal(claim.raw, &connection); err != nil {
				clear(code)
				return err
			}
			// Pair request order and host identity order are part of this historical
			// digest. Values and invitation IDs remain the original durable binding.
			request := struct {
				ID         string          `json:"id"`
				Invitation string          `json:"invitation"`
				Local      json.RawMessage `json:"local"`
				Peer       json.RawMessage `json:"peer"`
				Code       string          `json:"code"`
			}{connection.ID, claim.id, connection.Peer, connection.Local, string(code)}
			raw, err := json.Marshal([]any{request, connection.PeerHost})
			clear(code)
			if err != nil {
				return err
			}
			hash := sha256.Sum256(raw)
			clear(raw)
			if _, err = tx.ExecContext(ctx, "UPDATE agent_link_invitations SET claim_hash=? WHERE id=?", hex.EncodeToString(hash[:]), claim.id); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, "DELETE FROM node_identity_secret_migration")
		return err
	})
}
func readNodeSecret(ctx context.Context, tx *sql.Tx, aead cipher.AEAD, ref string) ([]byte, error) {
	var nonce, body, tag []byte
	if err := tx.QueryRowContext(ctx, "SELECT nonce,ciphertext,tag FROM secrets WHERE id=?", ref).Scan(&nonce, &body, &tag); err != nil {
		return nil, err
	}
	if len(nonce) != aead.NonceSize() || len(tag) != aead.Overhead() {
		return nil, fmt.Errorf("invalid encrypted migration secret")
	}
	return aead.Open(nil, nonce, append(body, tag...), []byte(ref))
}
func writeNodeSecret(ctx context.Context, tx *sql.Tx, aead cipher.AEAD, ref string, raw []byte) error {
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	sealed := aead.Seal(nil, nonce, raw, []byte(ref))
	split := len(sealed) - aead.Overhead()
	_, err := tx.ExecContext(ctx, "UPDATE secrets SET nonce=?,ciphertext=?,tag=? WHERE id=?", nonce, sealed[:split], sealed[split:], ref)
	return err
}

// CheckNodeIdentityReady fences public Agent Link hosts using custom secret
// stores. Such hosts own their secret conversion; SQL migration alone cannot
// decrypt an external store. New databases with no credential rows are ready.
func CheckNodeIdentityReady(ctx context.Context, db *sql.DB) error {
	var count int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_identity_secret_migration WHERE EXISTS(SELECT 1 FROM agent_link_connections WHERE secret_ref!='' AND (state!='revoked' OR json_extract(metadata,'$.revocation_pending')=1)) OR EXISTS(SELECT 1 FROM agent_link_invitations WHERE claim_hash!='')`).Scan(&count)
	if err != nil {
		return err
	}
	if count != 0 {
		return fmt.Errorf("Agent Link node identity secret migration is incomplete")
	}
	return nil
}
