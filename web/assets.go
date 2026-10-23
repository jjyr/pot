// Package webassets embeds the framework-free frontend shared by the browser and Wails.
package webassets

import (
	"embed"
	"io/fs"
	"strings"

	"github.com/jjyr/pot/pkg/storage/migrations"
)

//go:embed all:src
var resources embed.FS

// FS returns frontend resources rooted at index.html, suitable for http.FileServer
// or the Wails asset server. This package has no desktop dependencies.
func FS() fs.FS {
	root, err := fs.Sub(resources, "src")
	if err != nil {
		panic(err)
	}
	return frontendAssets{root}
}

type frontendAssets struct{ fs.FS }

func (f frontendAssets) Open(name string) (fs.File, error) {
	const prefix = "pkg/storage/migrations/"
	if strings.HasPrefix(name, prefix+"ui-cache/") {
		return migrations.FrontendSchemas().Open(strings.TrimPrefix(name, prefix))
	}
	return f.FS.Open(name)
}
