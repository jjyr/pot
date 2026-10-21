package migrations

import (
	"encoding/json"
	"errors"
	"os"
)

// RetireBrowserTailnet removes the obsolete embedded-node publication flags.
// Browser credentials, session state and inactive Tailscale keys are untouched.
// It runs under the browser service's exclusive data-root lock.
func RetireBrowserTailnet(directory string) error {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer root.Close()
	const name = "access-settings.json"
	info, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 8192 {
		return errors.New("retired browser settings must be a private regular file below 8 KiB")
	}
	raw, err := root.ReadFile(name)
	if err != nil {
		return err
	}
	var flags map[string]json.RawMessage
	if err = json.Unmarshal(raw, &flags); err != nil {
		return err
	}
	if flags == nil {
		return errors.New("invalid retired browser settings")
	}
	for key, value := range flags {
		if key != "connected" && key != "enabled" {
			return errors.New("unrecognized retired browser settings")
		}
		var flag *bool
		if json.Unmarshal(value, &flag) != nil || flag == nil {
			return errors.New("invalid retired browser setting")
		}
	}
	return root.Remove(name)
}
