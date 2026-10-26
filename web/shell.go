package webassets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"sync"

	"github.com/jjyr/pot/pkg/storage/migrations"
)

// The embedded tree is the build boundary. Hash every dependency, including
// dynamic imports and IndexedDB upgrades, rather than maintaining a second list.
var shellOnce sync.Once
var shellWorker []byte

func prepareShell() {
	root, err := fs.Sub(resources, "src")
	if err != nil {
		panic(err)
	}
	type asset struct {
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	}
	assets := []asset{}
	add := func(tree fs.FS, prefix string) {
		err := fs.WalkDir(tree, ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || name == "service-worker.js" {
				return nil
			}
			body, err := fs.ReadFile(tree, name)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(body)
			assets = append(assets, asset{prefix + name, hex.EncodeToString(digest[:])})
			return nil
		})
		if err != nil {
			panic(err)
		}
	}
	add(root, "/")
	add(migrations.FrontendSchemas(), "/pkg/storage/migrations/")
	manifest, err := json.Marshal(assets)
	if err != nil {
		panic(err)
	}
	body, err := fs.ReadFile(root, "service-worker.js")
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(append(manifest, body...))
	config, err := json.Marshal(struct {
		Version string  `json:"version"`
		Assets  []asset `json:"assets"`
	}{hex.EncodeToString(digest[:]), assets})
	if err != nil {
		panic(err)
	}
	shellWorker = bytes.Replace(body, []byte("/* POT_SHELL_MANIFEST */ null"), config, 1)
}

// Open a generated worker through the same filesystem used by browser and native
// hosts. Its response changes whenever any embedded startup dependency changes.
func generatedWorker(file fs.File) (fs.File, error) {
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	shellOnce.Do(prepareShell)
	return &shellFile{Reader: bytes.NewReader(shellWorker), info: info}, nil
}

type shellFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *shellFile) Close() error { return nil }
func (f *shellFile) Stat() (fs.FileInfo, error) {
	return shellInfo{FileInfo: f.info, size: int64(len(shellWorker))}, nil
}

type shellInfo struct {
	fs.FileInfo
	size int64
}

func (s shellInfo) Size() int64 { return s.size }
