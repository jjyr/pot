package resource

import (
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"
)

// LoadPlugin snapshots a text-only plugin package. Execution revalidates its
// manifest, exact file set and digest; discovery alone grants no authority.
func LoadPlugin(directory string) (core.PluginBundle, error) {
	var bundle core.PluginBundle
	root, err := os.OpenRoot(directory)
	if err != nil {
		return bundle, err
	}
	defer root.Close()
	read := func(path string) (string, error) {
		info, err := root.Lstat(path)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Size() > 512*1024 {
			return "", errors.New("plugin files must be bounded regular text files")
		}
		file, err := root.Open(path)
		if err != nil {
			return "", err
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, 512*1024+1))
		if err != nil {
			return "", err
		}
		if len(data) > 512*1024 || !utf8.Valid(data) {
			return "", errors.New("plugin file is too large or not UTF-8")
		}
		return string(data), nil
	}
	manifest, err := read("plugin.json")
	if err != nil {
		return bundle, err
	}
	if err = core.DecodeJSON([]byte(manifest), &bundle.Manifest); err != nil {
		return bundle, err
	}
	bundle.Files = map[string]string{}
	for _, name := range bundle.Manifest.Files {
		if !core.SafeRelativePath(name) {
			return bundle, errors.New("invalid plugin file path")
		}
		data, err := read(filepath.FromSlash(name))
		if err != nil {
			return bundle, err
		}
		bundle.Files[name] = data
	}
	bundle.Digest, err = bundle.Hash()
	if err != nil {
		return bundle, err
	}
	return bundle, bundle.Validate()
}
