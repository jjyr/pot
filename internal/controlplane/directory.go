package controlplane

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/jjyr/pot/internal/core"
	"github.com/jjyr/pot/internal/storage"
	"runtime"
	"sort"
	"sync"
	"time"
)

type Options struct {
	RuntimeID string
	Embedded  bool
	CanJoin   func() error
}
type member struct {
	Machine Machine `json:"machine"`
	Token   string  `json:"token"`
}
type invitation struct {
	Expires    int64        `json:"expires"`
	Request    *JoinRequest `json:"request,omitempty"`
	Enrollment *Enrollment  `json:"enrollment,omitempty"`
}
type book struct {
	Version     uint32                `json:"version"`
	RuntimeID   string                `json:"runtime_id"`
	Key         string                `json:"key"`
	Owner       string                `json:"owner"`
	Role        string                `json:"role"`
	LocalLabels []string              `json:"local_labels"`
	Members     map[string]member     `json:"members"`
	Invites     map[string]invitation `json:"invites"`
	Membership  *Membership           `json:"membership,omitempty"`
	Pending     *pendingJoin          `json:"pending,omitempty"`
}
type pendingJoin struct {
	Origin  string      `json:"origin"`
	Control Identity    `json:"control"`
	Request JoinRequest `json:"request"`
}
type Directory struct {
	mu       sync.Mutex
	db       *sql.DB
	options  Options
	state    book
	key      ed25519.PrivateKey
	identity Identity
	peers    map[string]*Peer
}

func Open(path string, options Options) (*Directory, error) {
	if !core.Identifier(options.RuntimeID) {
		return nil, fmt.Errorf("invalid Runtime identity")
	}
	db, err := storage.OpenSQLite(path)
	if err != nil {
		return nil, err
	}
	d := &Directory{db: db, options: options, peers: map[string]*Peer{}}
	fail := func(err error) (*Directory, error) { db.Close(); return nil, err }
	if _, err = db.Exec(`CREATE TABLE IF NOT EXISTS controlplane_state(id INTEGER PRIMARY KEY CHECK(id=1),body TEXT NOT NULL)`); err != nil {
		return fail(err)
	}
	err = storage.Transaction(context.Background(), db, func(tx *sql.Tx) error {
		var raw []byte
		err := tx.QueryRow("SELECT body FROM controlplane_state WHERE id=1").Scan(&raw)
		if err == sql.ErrNoRows {
			_, key, e := ed25519.GenerateKey(rand.Reader)
			if e != nil {
				return e
			}
			d.state = book{Version: 1, RuntimeID: options.RuntimeID, Key: base64.RawURLEncoding.EncodeToString(key), Owner: secret(), Role: "neutral", Members: map[string]member{}, Invites: map[string]invitation{}, LocalLabels: []string{"local", runtime.GOOS}}
			raw, err = json.Marshal(d.state)
			if err != nil {
				return err
			}
			_, err = tx.Exec("INSERT INTO controlplane_state(id,body)VALUES(1,?)", string(raw))
			return err
		}
		if err != nil {
			return err
		}
		return core.DecodeJSON(raw, &d.state)
	})
	if err != nil {
		return fail(err)
	}
	if d.state.Version != 1 || d.state.RuntimeID != options.RuntimeID || (d.state.Role != "neutral" && d.state.Role != "control" && d.state.Role != "runner") || (options.Embedded && d.state.Role != "neutral") || d.state.Members == nil || d.state.Invites == nil || len(d.state.Owner) != 43 {
		return fail(fmt.Errorf("invalid Control directory schema/role/owner"))
	}
	key, err := base64.RawURLEncoding.DecodeString(d.state.Key)
	if err != nil || len(key) != ed25519.PrivateKeySize {
		return fail(fmt.Errorf("invalid Control private identity"))
	}
	d.key = ed25519.PrivateKey(key)
	d.identity = identity(options.RuntimeID, d.key.Public().(ed25519.PublicKey))
	if d.state.Role == "runner" {
		if d.state.Membership == nil {
			return fail(fmt.Errorf("Runner membership missing"))
		}
		if err := validateMembership(*d.state.Membership, d.identity.RuntimeID); err != nil {
			return fail(err)
		}
	}
	for id, m := range d.state.Members {
		if m.Machine.Identity.RuntimeID != id || m.Machine.Identity.Validate() != nil || m.Machine.Epoch == 0 || len(m.Token) != 43 {
			return fail(fmt.Errorf("invalid saved membership"))
		}
		if _, err := labels(m.Machine.Labels); err != nil {
			return fail(err)
		}
	}
	return d, nil
}
func (d *Directory) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, p := range d.peers {
		p.Close()
	}
	return d.db.Close()
}
func (d *Directory) Identity() Identity      { return d.identity }
func (d *Directory) Role() string            { d.mu.Lock(); defer d.mu.Unlock(); return d.state.Role }
func (d *Directory) OwnerCredential() string { d.mu.Lock(); defer d.mu.Unlock(); return d.state.Owner }
func (d *Directory) AuthenticateOwner(token string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.state.Role == "control" && subtle.ConstantTimeCompare([]byte(token), []byte(d.state.Owner)) == 1
}
func (d *Directory) update(fn func(*book) error) error {
	raw, err := json.Marshal(d.state)
	if err != nil {
		return err
	}
	var next book
	if err = core.DecodeJSON(raw, &next); err != nil {
		return err
	}
	if err = fn(&next); err != nil {
		return err
	}
	raw, err = json.Marshal(next)
	if err != nil {
		return err
	}
	if _, err = d.db.Exec("UPDATE controlplane_state SET body=? WHERE id=1", string(raw)); err != nil {
		return err
	}
	d.state = next
	return nil
}
func (d *Directory) EnableControl() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.update(func(b *book) error {
		if d.options.Embedded || b.Role == "runner" || b.Pending != nil {
			return fmt.Errorf("Runtime cannot become Control")
		}
		b.Role = "control"
		return nil
	})
}
func (d *Directory) Invitation(ctx context.Context, ttl time.Duration) (Invitation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if ttl <= 0 || ttl > 24*time.Hour {
		return Invitation{}, fmt.Errorf("invitation lifetime must be within 24 hours")
	}
	i := Invitation{secret(), time.Now().Add(ttl).UnixMilli(), d.identity}
	err := d.update(func(b *book) error {
		if b.Role != "control" {
			return fmt.Errorf("standalone Control required")
		}
		for key, v := range b.Invites {
			if v.Expires <= time.Now().UnixMilli() {
				delete(b.Invites, key)
			}
		}
		if len(b.Invites) >= 1024 {
			return fmt.Errorf("invitation capacity reached")
		}
		b.Invites[tokenHash(i.Code)] = invitation{Expires: i.ExpiresAtMS}
		return nil
	})
	return i, err
}
func (d *Directory) Join(ctx context.Context, s Signed) (Signed, error) {
	var r JoinRequest
	if err := core.DecodeJSON(s.Payload, &r); err != nil {
		return Signed{}, err
	}
	if err := verify(r.Caller, s, &r); err != nil {
		return Signed{}, err
	}
	if r.Version != 1 || !core.Identifier(r.RequestID) || r.ControlID != d.identity.RuntimeID || r.Caller.RuntimeID == r.ControlID || len(r.Invitation) != 43 {
		return Signed{}, fmt.Errorf("invalid enrollment request")
	}
	if _, err := labels([]string{r.OS}); err != nil {
		return Signed{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var result Enrollment
	err := d.update(func(b *book) error {
		if b.Role != "control" {
			return ErrUnauthorized
		}
		i, ok := b.Invites[tokenHash(r.Invitation)]
		if !ok || i.Expires <= time.Now().UnixMilli() {
			return ErrUnauthorized
		}
		if i.Request != nil {
			a, _ := core.HashJSON(*i.Request)
			z, _ := core.HashJSON(r)
			if a != z || i.Enrollment == nil {
				return fmt.Errorf("invitation already consumed")
			}
			m, ok := b.Members[r.Caller.RuntimeID]
			if !ok || !m.Machine.Enabled || m.Machine.Epoch != i.Enrollment.Epoch || m.Token != i.Enrollment.Token {
				return ErrUnauthorized
			}
			result = *i.Enrollment
			return nil
		}
		old := b.Members[r.Caller.RuntimeID]
		if old.Machine.Enabled {
			return fmt.Errorf("machine already enrolled")
		}
		epoch := old.Machine.Epoch + 1
		if epoch == 0 || epoch > core.MaxSafeInteger {
			return fmt.Errorf("membership epoch exhausted")
		}
		result = Enrollment{1, r.RequestID, d.identity, r.Caller.RuntimeID, epoch, secret()}
		tags := old.Machine.Labels
		if tags == nil {
			tags = []string{"runner", r.OS}
		}
		b.Members[r.Caller.RuntimeID] = member{Machine: Machine{Identity: r.Caller, Epoch: epoch, Labels: tags, Enabled: true, OS: r.OS}, Token: result.Token}
		i.Request = &r
		i.Enrollment = &result
		b.Invites[tokenHash(r.Invitation)] = i
		return nil
	})
	if err != nil {
		return Signed{}, err
	}
	return sign(d.key, result)
}
func (d *Directory) Machines(ctx context.Context) ([]Machine, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []Machine{}
	for id, m := range d.state.Members {
		if m.Machine.Enabled {
			v := m.Machine
			v.Labels = append([]string{}, v.Labels...)
			v.Online = d.peers[id] != nil
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Identity.RuntimeID < out[j].Identity.RuntimeID })
	return out, nil
}
func (d *Directory) SetLabels(ctx context.Context, id string, tags []string) error {
	tags, err := labels(tags)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.update(func(b *book) error {
		if id == d.identity.RuntimeID && b.Role != "runner" {
			b.LocalLabels = tags
			return nil
		}
		m, ok := b.Members[id]
		if b.Role != "control" || !ok || !m.Machine.Enabled {
			return fmt.Errorf("unknown machine")
		}
		m.Machine.Labels = tags
		b.Members[id] = m
		return nil
	})
}
func (d *Directory) LocalLabels() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]string{}, d.state.LocalLabels...)
}
func (d *Directory) Remove(ctx context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	err := d.update(func(b *book) error {
		m, ok := b.Members[id]
		if b.Role != "control" || !ok {
			return fmt.Errorf("unknown machine")
		}
		m.Machine.Enabled = false
		m.Token = secret()
		b.Members[id] = m
		return nil
	})
	if err == nil {
		if p := d.peers[id]; p != nil {
			p.Close()
			delete(d.peers, id)
		}
	}
	return err
}
func validateMembership(m Membership, runtimeID string) error {
	if _, err := ClientForCA(nil, m.CAPEM); err != nil {
		return err
	}
	if err := ValidOrigin(m.Origin); err != nil {
		return err
	}
	e := m.Enrollment
	if e.Version != 1 || e.RuntimeID != runtimeID || e.Control.RuntimeID == runtimeID || e.Epoch == 0 || e.Epoch > core.MaxSafeInteger || len(e.Token) != 43 {
		return fmt.Errorf("invalid membership")
	}
	return e.Control.Validate()
}
func (d *Directory) Membership() *Membership {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.state.Membership == nil {
		return nil
	}
	m := *d.state.Membership
	return &m
}
func (d *Directory) SaveMembership(ctx context.Context, m Membership) error {
	if err := validateMembership(m, d.identity.RuntimeID); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.update(func(b *book) error {
		if d.options.Embedded || b.Role == "control" {
			return fmt.Errorf("Runtime cannot join Control")
		}
		if d.options.CanJoin != nil {
			if err := d.options.CanJoin(); err != nil {
				return err
			}
		}
		if b.Membership != nil {
			a, _ := core.HashJSON(*b.Membership)
			z, _ := core.HashJSON(m)
			if a != z {
				return fmt.Errorf("Runtime already joined a Control")
			}
		}
		b.Membership = &m
		b.Role = "runner"
		b.Pending = nil
		return nil
	})
}
func (d *Directory) prepareJoin(origin string, control Identity, code string, ids ...string) (Signed, error) {
	if err := ValidOrigin(origin); err != nil {
		return Signed{}, err
	}
	if err := control.Validate(); err != nil {
		return Signed{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	var request JoinRequest
	requestID := secret()
	if len(ids) > 1 {
		return Signed{}, fmt.Errorf("one enrollment request identity required")
	}
	if len(ids) == 1 {
		requestID = ids[0]
		if !core.Identifier(requestID) {
			return Signed{}, fmt.Errorf("invalid enrollment request identity")
		}
	}
	err := d.update(func(b *book) error {
		if d.options.Embedded || b.Role != "neutral" {
			return fmt.Errorf("neutral standalone Runtime required")
		}
		if d.options.CanJoin != nil {
			if err := d.options.CanJoin(); err != nil {
				return err
			}
		}
		if b.Pending != nil {
			p := b.Pending
			if p.Origin != origin || p.Control != control || p.Request.Invitation != code || (len(ids) == 1 && p.Request.RequestID != requestID) {
				return fmt.Errorf("enrollment already pending; retry original identity")
			}
			request = p.Request
			return nil
		}
		request = JoinRequest{1, requestID, control.RuntimeID, d.identity, code, runtime.GOOS}
		b.Pending = &pendingJoin{origin, control, request}
		return nil
	})
	if err != nil {
		return Signed{}, err
	}
	return sign(d.key, request)
}

// Leave first closes outward sessions, then requires Runtime to fence and settle
// all member leases. Failed/unknown cleanup retains membership and Runner role.
func (d *Directory) Leave(ctx context.Context, settle func(context.Context) error) error {
	if settle == nil {
		return fmt.Errorf("member cleanup confirmation required")
	}
	d.mu.Lock()
	if d.state.Role != "runner" {
		d.mu.Unlock()
		return fmt.Errorf("Runner membership required")
	}
	for id, p := range d.peers {
		p.Close()
		delete(d.peers, id)
	}
	d.mu.Unlock()
	if err := settle(ctx); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.update(func(b *book) error { b.Membership = nil; b.Pending = nil; b.Role = "neutral"; return nil })
}
