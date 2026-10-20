// Package secrets stores authenticated encrypted credentials at their owning
// Control. Its API deliberately provides no bulk export or execution-node access.
package secrets

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jjyr/pot/pkg/storage"
)

// KeySource must never regenerate a missing key when allowCreate is false.
// It returns a caller-owned 32-byte copy, which Store clears after use.
type KeySource interface {
	Key(context.Context, bool) ([]byte, error)
}
type MemoryKey struct{ value [32]byte }

func NewMemoryKey(value []byte) (*MemoryKey, error) {
	if len(value) != 32 {
		return nil, errors.New("secret master key must contain exactly 32 bytes")
	}
	key := &MemoryKey{}
	copy(key.value[:], value)
	return key, nil
}
func (k *MemoryKey) Key(ctx context.Context, _ bool) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]byte(nil), k.value[:]...), nil
}

type Store struct {
	db   *sql.DB
	keys KeySource
}

func Open(path string, keys KeySource) (*Store, error) {
	if keys == nil {
		return nil, errors.New("secret KeySource is required")
	}
	db, err := storage.OpenDatabase(path, storage.ControlDatabase)
	if err != nil {
		return nil, err
	}
	if err = storage.FinalizeNodeIdentity(context.Background(), db, keys); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db, keys}, nil
}
func (s *Store) Close() error { return s.db.Close() }

// Delete removes a no-longer-referenced secret, including integration logout.
func (s *Store) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM secrets WHERE id=?", id)
	return err
}
func (s *Store) Put(ctx context.Context, id string, plaintext []byte) error {
	if id == "" || len(id) > 256 || len(plaintext) > 1024*1024 {
		return errors.New("invalid secret size or reference")
	}
	return storage.Transaction(ctx, s.db, func(tx *sql.Tx) error {
		var populated bool
		if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM secrets)").Scan(&populated); err != nil {
			return err
		}
		key, err := s.keys.Key(ctx, !populated)
		if err != nil {
			return err
		}
		defer clear(key)
		aead, err := makeAEAD(key)
		if err != nil {
			return err
		}
		existing, found, err := read(ctx, tx, id, aead)
		if err != nil {
			return err
		}
		if found {
			defer clear(existing)
			if subtle.ConstantTimeCompare(existing, plaintext) != 1 {
				return errors.New("secret reference already contains another value")
			}
			return nil
		}
		nonce := make([]byte, aead.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return err
		}
		sealed := aead.Seal(nil, nonce, plaintext, []byte(id))
		split := len(sealed) - aead.Overhead()
		_, err = tx.ExecContext(ctx, "INSERT INTO secrets VALUES (?,?,?,?)", id, nonce, sealed[:split], sealed[split:])
		return err
	})
}
func (s *Store) Get(ctx context.Context, id string) ([]byte, error) {
	key, err := s.keys.Key(ctx, false)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	aead, err := makeAEAD(key)
	if err != nil {
		return nil, err
	}
	value, found, err := read(ctx, s.db, id, aead)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errors.New("secret reference unavailable")
	}
	return value, nil
}
func makeAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != 32 {
		return nil, errors.New("invalid Control master key")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func read(ctx context.Context, db queryer, id string, aead cipher.AEAD) ([]byte, bool, error) {
	var nonce, encrypted, tag []byte
	err := db.QueryRowContext(ctx, "SELECT nonce,ciphertext,tag FROM secrets WHERE id=?", id).Scan(&nonce, &encrypted, &tag)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(nonce) != 12 || len(tag) != 16 {
		return nil, false, errors.New("invalid encrypted secret")
	}
	value, err := aead.Open(nil, nonce, append(encrypted, tag...), []byte(id))
	if err != nil {
		return nil, false, fmt.Errorf("secret authentication failed")
	}
	return value, true, nil
}
