package webui

import (
	"context"
	"errors"
	"github.com/jjyr/pot/internal/core"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// AccessStore serializes operator changes and browser session revocations across
// processes. A stale logout can never overwrite a newer token generation.
type AccessStore struct{ Path string }

func (s AccessStore) locked(fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(s.Path), 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.Path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	stat, err := f.Stat()
	if err != nil {
		return err
	}
	owned, ok := stat.Sys().(*syscall.Stat_t)
	if !ok || owned.Uid != uint32(os.Geteuid()) || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
		return errors.New("access lock must be private")
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}
func (s AccessStore) read() (Config, error) {
	var c Config
	f, err := os.OpenFile(s.Path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return c, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return c, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4<<20 {
		return c, errors.New("access configuration must be a private owned regular file below 4 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(f, 4<<20+1))
	if err != nil {
		return c, err
	}
	if len(data) > 4<<20 {
		return c, errors.New("access configuration too large")
	}
	if err = core.DecodeJSON(data, &c); err != nil {
		return c, err
	}
	if len(c.Token) < 20 || len(c.SessionKey) < 20 || c.Generation == 0 {
		return c, errors.New("invalid access configuration")
	}
	return c, nil
}
func (s AccessStore) Load(create bool) (Config, error) {
	var c Config
	err := s.locked(func() error {
		var err error
		c, err = s.read()
		if os.IsNotExist(err) && create {
			c = NewConfig()
			err = c.Save(s.Path)
		}
		return err
	})
	return c, err
}
func (s AccessStore) Change(action string) (Config, error) {
	var result Config
	err := s.locked(func() error {
		c, err := s.read()
		if err != nil {
			return err
		}
		if c.Generation >= core.MaxSafeInteger {
			return errors.New("access generation exhausted")
		}
		switch action {
		case "regenerate-token":
			next := NewConfig()
			next.Generation = c.Generation + 1
			next.Disabled = c.Disabled
			c = next
		case "enable":
			c.Disabled = false
			c.Generation++
			c.Revoked = nil
		case "disable":
			c.Disabled = true
			c.Generation++
			c.Revoked = nil
		default:
			return errors.New("unknown access action")
		}
		if err = c.Save(s.Path); err != nil {
			return err
		}
		result = c
		return nil
	})
	return result, err
}
func (s AccessStore) SaveSessions(c Config) error {
	return s.locked(func() error {
		current, err := s.read()
		if err != nil {
			return err
		}
		if current.Generation != c.Generation || current.Token != c.Token || current.SessionKey != c.SessionKey {
			return errors.New("browser credentials changed")
		}
		if current.Revoked == nil {
			current.Revoked = map[string]int64{}
		}
		for id, expires := range c.Revoked {
			current.Revoked[id] = expires
		}
		for id, expires := range current.Revoked {
			if expires <= time.Now().Unix() {
				delete(current.Revoked, id)
			}
		}
		return current.Save(s.Path)
	})
}
func (s *Server) WatchAccess(ctx context.Context, store AccessStore) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			c, err := store.Load(false)
			if err != nil {
				return err
			}
			s.mu.Lock()
			generation := s.config.Generation
			s.mu.Unlock()
			if c.Generation > generation {
				if err = s.ReplaceAccess(c); err != nil {
					return err
				}
			}
		}
	}
}
