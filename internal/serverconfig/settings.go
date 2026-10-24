// Package serverconfig owns operator-selected startup settings. It does not
// store credentials or mutate a running Runtime's listeners.
package serverconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/instance"
	"github.com/jjyr/pot/pkg/storage/migrations"
)

type Settings struct {
	Version   int    `json:"version"`
	Workspace string `json:"workspace"`
	Resources string `json:"resources"`
}

func (s Settings) Validate() error {
	if s.Version != 0 && s.Version != 1 {
		return errors.New("unsupported server settings version")
	}
	for _, path := range []string{s.Workspace, s.Resources} {
		if strings.ContainsAny(path, "\x00\r\n") || path != "" && !filepath.IsAbs(path) {
			return errors.New("server file/directory settings require absolute paths")
		}
	}
	return nil
}
func Load(root string) (Settings, error) {
	var result Settings
	path := filepath.Join(root, "server.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 1024*1024 {
		return result, errors.New("server settings must be an owner-private regular file at most1MiB")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	raw, upgraded, err := migrations.UpgradeServerConfig(raw)
	if err != nil {
		return result, err
	}
	if err = core.DecodeJSON(raw, &result); err != nil {
		return result, err
	}
	if err = result.Validate(); err != nil {
		return result, err
	}
	if upgraded {
		if err = instance.WriteJSON(path, result); err != nil {
			return result, err
		}
	}
	return result, nil
}
func Save(root string, settings Settings) error {
	settings.Version = 1
	if err := settings.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("Runtime root must be owner-private")
	}
	anchor, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer anchor.Close()
	identityInfo, err := anchor.Lstat("identity.json")
	if err != nil || !identityInfo.Mode().IsRegular() || identityInfo.Mode().Perm()&0077 != 0 {
		return errors.New("initialize the Pot Runtime root before saving server settings")
	}
	raw, err := anchor.ReadFile("identity.json")
	if err != nil {
		return err
	}
	var identity instance.Identity
	if err = core.DecodeJSON(raw, &identity); err != nil {
		return err
	}
	if identity.Format != instance.IdentityFormat || identity.Version != 1 || !core.Identifier(identity.RuntimeID) {
		return errors.New("server settings require a recognized Pot Runtime root")
	}
	return instance.WriteJSON(filepath.Join(root, "server.json"), settings)
}
