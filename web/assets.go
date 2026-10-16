// Package webassets embeds the framework-free frontend shared by the browser and Wails.
package webassets

import (
	"embed"
	"io/fs"
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
	return root
}
