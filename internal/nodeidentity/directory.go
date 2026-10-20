// Package nodeidentity owns this autonomous node's stable signing identity and
// local machine labels. It grants no remote execution or configuration authority.
package nodeidentity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"runtime"
	"sync"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/peer"
	"github.com/jjyr/pot/pkg/storage"
)

type Options struct{ RuntimeID string }
type Identity struct {
	RuntimeID   string `json:"runtime_id"`
	PublicKey   string `json:"public_key"`
	Fingerprint string `json:"fingerprint"`
}
type state struct {
	Version     int      `json:"version"`
	RuntimeID   string   `json:"runtime_id"`
	Key         string   `json:"key"`
	LocalLabels []string `json:"local_labels"`
}
type Directory struct {
	mu       sync.Mutex
	db       *sql.DB
	state    state
	key      ed25519.PrivateKey
	identity Identity
}

func Open(path string, options Options) (*Directory, error) {
	if !core.Identifier(options.RuntimeID) {
		return nil, errors.New("invalid node identity")
	}
	db, err := storage.OpenDatabase(path, storage.MachinesDatabase)
	if err != nil {
		return nil, err
	}
	d := &Directory{db: db}
	err = storage.Transaction(context.Background(), db, func(tx *sql.Tx) error {
		var raw []byte
		err := tx.QueryRow("SELECT body FROM node_identity WHERE id=1").Scan(&raw)
		if err == sql.ErrNoRows {
			_, key, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				return err
			}
			d.state = state{Version: 1, RuntimeID: options.RuntimeID, Key: base64.RawURLEncoding.EncodeToString(key), LocalLabels: []string{"local", runtime.GOOS}}
			raw, err = json.Marshal(d.state)
			if err != nil {
				return err
			}
			_, err = tx.Exec("INSERT INTO node_identity(id,body)VALUES(1,?)", string(raw))
			return err
		}
		if err != nil {
			return err
		}
		return core.DecodeJSON(raw, &d.state)
	})
	if err == nil {
		var key []byte
		key, err = base64.RawURLEncoding.DecodeString(d.state.Key)
		if err == nil && (d.state.Version != 1 || d.state.RuntimeID != options.RuntimeID || len(key) != ed25519.PrivateKeySize) {
			err = errors.New("invalid node identity state")
		}
		if err == nil {
			d.key = ed25519.PrivateKey(key)
			public := d.key.Public().(ed25519.PublicKey)
			sum := sha256.Sum256(public)
			d.identity = Identity{options.RuntimeID, base64.RawURLEncoding.EncodeToString(public), hex.EncodeToString(sum[:])}
		}
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return d, nil
}
func (d *Directory) Close() error       { return d.db.Close() }
func (d *Directory) Identity() Identity { return d.identity }
func (d *Directory) TransportIdentity() (peer.Identity, ed25519.PrivateKey) {
	return peer.Identity{NodeID: d.identity.RuntimeID, PublicKey: d.identity.PublicKey}, append(ed25519.PrivateKey(nil), d.key...)
}
func (d *Directory) OwnerAction(fn func() error) error { d.mu.Lock(); defer d.mu.Unlock(); return fn() }
func (d *Directory) LocalLabels() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string{}, d.state.LocalLabels...)
}
func (d *Directory) SetLabels(ctx context.Context, id string, tags []string) error {
	if id != d.identity.RuntimeID {
		return errors.New("machine labels belong to the local node")
	}
	if len(tags) > 64 {
		return errors.New("too many machine labels")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, tag := range tags {
		if len(tag) == 0 || len(tag) > 64 {
			return errors.New("invalid machine label")
		}
		for _, c := range tag {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
				return errors.New("invalid machine label")
			}
		}
		if !seen[tag] {
			out = append(out, tag)
			seen[tag] = true
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	next := d.state
	next.LocalLabels = out
	body, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if _, err = d.db.ExecContext(ctx, "UPDATE node_identity SET body=? WHERE id=1", string(body)); err != nil {
		return err
	}
	d.state = next
	return nil
}
