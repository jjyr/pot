package integrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"sync"
	"time"
)

type Options struct {
	Context  context.Context
	Accounts AccountStore
	Secrets  SecretStore
	Adapters []Adapter
	// Write serializes mutations with any host-level ownership changes.
	Write   func(func() error) error
	Changed func()
}
type session struct {
	Login
	cancel   context.CancelFunc
	revision uint64
}
type cached struct {
	token    string
	revision uint64
	expires  *time.Time
	loaded   time.Time
}
type Service struct {
	mu       sync.Mutex
	opts     Options
	adapters map[string]Adapter
	sessions map[string]*session
	scopes   map[string]map[string]cached
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	closed   bool
	now      func() time.Time
}

// New composes a service over caller-owned stores. Close stops sessions and
// clears caches, while the caller remains responsible for closing its stores.
func New(o Options) (*Service, error) {
	if o.Accounts == nil || o.Secrets == nil {
		return nil, errors.New("integration stores are required")
	}
	if o.Context == nil {
		o.Context = context.Background()
	}
	if o.Write == nil {
		o.Write = func(work func() error) error { return work() }
	}
	ctx, cancel := context.WithCancel(o.Context)
	s := &Service{opts: o, adapters: map[string]Adapter{}, sessions: map[string]*session{}, scopes: map[string]map[string]cached{}, ctx: ctx, cancel: cancel, now: time.Now}
	for _, a := range o.Adapters {
		d := a.Descriptor()
		if d.ID == "" || d.Name == "" || s.adapters[d.ID] != nil {
			cancel()
			return nil, errors.New("invalid or duplicate integration adapter")
		}
		s.adapters[d.ID] = a
	}
	return s, nil
}
func identifier() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (s *Service) changed() {
	if s.opts.Changed != nil {
		s.opts.Changed()
	}
}
func (s *Service) mutate(work func() error) error {
	return s.opts.Write(func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.closed {
			return errors.New("integrations closed")
		}
		return work()
	})
}
func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	for _, v := range s.sessions {
		v.cancel()
	}
	clear(s.scopes)
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}
func (s *Service) accounts(ctx context.Context) (map[string]Account, error) {
	items, err := s.opts.Accounts.List(ctx)
	result := map[string]Account{}
	for _, a := range items {
		result[a.Service] = a
	}
	return result, err
}
func (s *Service) List(ctx context.Context) ([]Integration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	accounts, err := s.accounts(ctx)
	if err != nil {
		return nil, err
	}
	items := []Integration{}
	for id, adapter := range s.adapters {
		v := Integration{Descriptor: adapter.Descriptor(), Status: "disconnected"}
		if a, ok := accounts[id]; ok {
			v.Account = &a
			v.Status = "connected"
			if a.ExpiresAt != nil && !a.ExpiresAt.After(s.now()) {
				v.Status = "expired"
				if _, can := adapter.(Refresher); can {
					c, err := s.readCredential(ctx, a)
					if err != nil {
						return nil, err
					}
					if c.RefreshToken != "" && (c.RefreshExpiresAt == nil || c.RefreshExpiresAt.After(s.now())) {
						v.Status = "connected"
					}
				}
			}
		}
		if pending := s.sessions[id]; pending != nil {
			login := s.login(pending)
			v.Login = &login
		}
		items = append(items, v)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, nil
}

// HasState includes pending logins, so a host cannot change ownership midway
// through authorization. It is safe to call from the host's Write gate.
func (s *Service) HasState(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items, err := s.opts.Accounts.List(ctx)
	if err != nil {
		return false, err
	}
	if len(items) > 0 {
		return true, nil
	}
	for _, v := range s.sessions {
		if s.login(v).Status == "pending" {
			return true, nil
		}
	}
	return false, nil
}
func (s *Service) login(v *session) Login {
	if v.Status == "pending" && !v.ExpiresAt.After(s.now()) {
		v.Status = "expired"
		v.cancel()
	}
	return v.Login
}
func (s *Service) BeginLogin(ctx context.Context, service string) (Login, error) {
	var v *session
	var adapter Adapter
	var beginContext context.Context
	var cancelBegin context.CancelFunc
	err := s.mutate(func() error {
		adapter = s.adapters[service]
		if adapter == nil {
			return ErrNotFound
		}
		if !adapter.Descriptor().LoginAvailable {
			return errors.New("login is not configured for this service")
		}
		accounts, err := s.accounts(ctx)
		if err != nil {
			return err
		}
		if _, ok := accounts[service]; ok {
			return errors.New("sign out before logging in again")
		}
		if previous := s.sessions[service]; previous != nil && s.login(previous).Status == "pending" {
			return errors.New("login is already in progress")
		}
		revision, err := s.opts.Accounts.Revision(ctx, service)
		if err != nil {
			return err
		}
		beginContext, cancelBegin = context.WithCancel(s.ctx)
		v = &session{Login: Login{ID: identifier(), Service: service, Status: "pending", ExpiresAt: s.now().Add(15 * time.Minute)}, cancel: cancelBegin, revision: revision}
		s.sessions[service] = v
		return nil
	})
	if err != nil {
		return Login{}, err
	}
	stopRequest := context.AfterFunc(ctx, cancelBegin)
	defer stopRequest()
	challenge, beginErr := adapter.Begin(beginContext)
	cancelBegin()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.sessions[service] != v || v.Status != "pending" {
		return Login{}, errors.New("login cancelled")
	}
	if beginErr != nil {
		v.Status = "failed"
		v.Error = "Could not start authorization. Try logging in again."
		return Login{}, errors.New(v.Error)
	}
	u, err := url.Parse(challenge.URL)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || !challenge.ExpiresAt.After(s.now()) || challenge.ExpiresAt.After(s.now().Add(time.Hour)) {
		v.Status = "failed"
		return Login{}, errors.New("invalid authorization challenge")
	}
	runctx, cancel := context.WithDeadline(s.ctx, challenge.ExpiresAt)
	v.cancel = cancel
	v.AuthorizationURL = challenge.URL
	v.UserCode = challenge.UserCode
	v.ExpiresAt = challenge.ExpiresAt
	s.wg.Add(1)
	go s.authorize(runctx, v, adapter, challenge)
	return v.Login, nil
}
func (s *Service) authorize(ctx context.Context, v *session, adapter Adapter, challenge Challenge) {
	defer s.wg.Done()
	defer v.cancel()
	grant, authErr := adapter.Authorize(ctx, challenge)
	err := s.mutate(func() error {
		if s.sessions[v.Service] != v || s.login(v).Status != "pending" {
			return nil
		}
		if authErr != nil || ctx.Err() != nil {
			v.Status = "failed"
			v.Error = "Authorization did not complete. Try logging in again."
			if !v.ExpiresAt.After(s.now()) {
				v.Status = "expired"
			}
			return nil
		}
		if grant.Identity.ID == "" || grant.Identity.Username == "" || grant.Credential.AccessToken == "" {
			v.Status = "failed"
			v.Error = "The service returned an incomplete account."
			return nil
		}
		accounts, err := s.accounts(ctx)
		if err != nil {
			return err
		}
		if _, ok := accounts[v.Service]; ok {
			return ErrChanged
		}
		revision, err := s.opts.Accounts.Revision(ctx, v.Service)
		if err != nil {
			return err
		}
		if revision != v.revision {
			return ErrChanged
		}
		_, err = s.save(ctx, Account{Service: v.Service, Identity: grant.Identity}, grant.Credential, revision)
		if err == nil {
			v.Status = "succeeded"
			v.AuthorizationURL = ""
			v.UserCode = ""
		}
		return err
	})
	if err != nil {
		s.mu.Lock()
		if !s.closed && s.sessions[v.Service] == v && v.Status == "pending" {
			v.Status = "failed"
			v.Error = "Could not save the account. Try logging in again."
		}
		s.mu.Unlock()
	}
	s.changed()
}
func (s *Service) Login(ctx context.Context, service, id string) (Login, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Login{}, err
	}
	v := s.sessions[service]
	if v == nil || v.ID != id {
		return Login{}, ErrNotFound
	}
	return s.login(v), nil
}
func (s *Service) CancelLogin(ctx context.Context, service, id string) error {
	err := s.mutate(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		v := s.sessions[service]
		if v == nil || v.ID != id {
			return ErrNotFound
		}
		if s.login(v).Status == "pending" {
			v.Status = "cancelled"
			v.cancel()
		}
		return nil
	})
	if err == nil {
		s.changed()
	}
	return err
}
func (s *Service) Disconnect(ctx context.Context, service string, revision uint64) error {
	err := s.mutate(func() error {
		if s.adapters[service] == nil {
			return ErrNotFound
		}
		accounts, err := s.accounts(ctx)
		if err != nil {
			return err
		}
		a, ok := accounts[service]
		if !ok || a.Revision != revision {
			return ErrChanged
		}
		if err = s.opts.Accounts.Delete(ctx, service, revision); err != nil {
			return err
		}
		if v := s.sessions[service]; v != nil {
			v.Status = "cancelled"
			v.cancel()
		}
		clear(s.scopes)
		return s.opts.Secrets.Delete(ctx, a.SecretRef)
	})
	if err == nil {
		s.changed()
	}
	return err
}
func (s *Service) save(ctx context.Context, a Account, c Credential, revision uint64) (Account, error) {
	raw, err := json.Marshal(c)
	if err != nil {
		return Account{}, err
	}
	defer clear(raw)
	ref := "integration/" + identifier()
	if err = s.opts.Secrets.Put(ctx, ref, raw); err != nil {
		return Account{}, err
	}
	previous := a.SecretRef
	a.SecretRef = ref
	a.ExpiresAt = c.ExpiresAt
	next, err := s.opts.Accounts.Save(ctx, a, revision)
	if err != nil {
		return Account{}, errors.Join(err, s.opts.Secrets.Delete(context.WithoutCancel(ctx), ref))
	}
	clear(s.scopes)
	if previous != "" {
		err = s.opts.Secrets.Delete(ctx, previous)
	}
	return next, err
}
func (s *Service) readCredential(ctx context.Context, a Account) (Credential, error) {
	raw, err := s.opts.Secrets.Get(ctx, a.SecretRef)
	if err != nil {
		return Credential{}, err
	}
	defer clear(raw)
	var c Credential
	if err = json.Unmarshal(raw, &c); err != nil {
		return Credential{}, errors.New("invalid stored credential")
	}
	return c, nil
}

func (s *Service) Credential(ctx context.Context, scope, service string, force bool) (string, error) {
	if scope == "" {
		return "", errors.New("credential scope is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return "", errors.New("integrations closed")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	adapter := s.adapters[service]
	if adapter == nil {
		return "", ErrNotFound
	}
	accounts, err := s.accounts(ctx)
	if err != nil {
		return "", err
	}
	a, ok := accounts[service]
	if !ok {
		return "", errors.New("service is not logged in: " + service)
	}
	now := s.now()
	cache := s.scopes[scope]
	value, ok := cache[service]
	if ok && !force && value.revision == a.Revision && (value.expires == nil || now.Add(30*time.Second).Before(*value.expires) || now.Sub(value.loaded) < time.Second) && (value.expires == nil || value.expires.After(now)) {
		return value.token, nil
	}
	c, err := s.readCredential(ctx, a)
	if err != nil {
		return "", err
	}
	if c.RefreshToken != "" && (force || c.ExpiresAt != nil && !now.Add(30*time.Second).Before(*c.ExpiresAt)) {
		refresher, can := adapter.(Refresher)
		if can && (c.RefreshExpiresAt == nil || c.RefreshExpiresAt.After(now)) {
			c, err = refresher.Refresh(ctx, c)
			if err != nil {
				return "", errors.New("credential refresh failed")
			}
			if c.AccessToken == "" {
				return "", errors.New("empty refreshed credential")
			}
			a, err = s.save(ctx, a, c, a.Revision)
			if err != nil {
				return "", err
			}
		}
	}
	if c.AccessToken == "" || c.ExpiresAt != nil && !c.ExpiresAt.After(s.now()) {
		return "", errors.New("login expired; sign out and log in again")
	}
	cache = s.scopes[scope]
	if cache == nil {
		cache = map[string]cached{}
		s.scopes[scope] = cache
	}
	cache[service] = cached{token: c.AccessToken, revision: a.Revision, expires: c.ExpiresAt, loaded: now}
	return c.AccessToken, nil
}
func (s *Service) Forget(scope string) { s.mu.Lock(); defer s.mu.Unlock(); delete(s.scopes, scope) }

// Importer is an optional adapter port for manually paired services. Import must
// verify the service identity before returning a grant; input is never logged.
type Importer interface {
	Import(context.Context, json.RawMessage) (Grant, error)
}

func (s *Service) Connect(ctx context.Context, service string, payload json.RawMessage) (Account, error) {
	s.mu.Lock()
	adapter, ok := s.adapters[service].(Importer)
	s.mu.Unlock()
	if !ok {
		return Account{}, ErrNotFound
	}
	grant, err := adapter.Import(ctx, payload)
	if err != nil {
		return Account{}, err
	}
	var result Account
	err = s.mutate(func() error {
		accounts, e := s.accounts(ctx)
		if e != nil {
			return e
		}
		if old, exists := accounts[service]; exists {
			previous, e := s.readCredential(ctx, old)
			if e != nil {
				return e
			}
			if old.Identity == grant.Identity && previous.AccessToken == grant.Credential.AccessToken {
				result = old
				return nil
			}
			return ErrChanged
		}
		revision, e := s.opts.Accounts.Revision(ctx, service)
		if e != nil {
			return e
		}
		result, e = s.save(ctx, Account{Service: service, Identity: grant.Identity}, grant.Credential, revision)
		return e
	})
	if err == nil {
		s.changed()
	}
	return result, err
}
