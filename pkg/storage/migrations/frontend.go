package migrations

import (
	"embed"
	"io/fs"
)

//go:embed ui-cache/*.mjs
var frontendSchemas embed.FS

// FrontendSchemas exposes only the versioned IndexedDB migrations to web hosts.
func FrontendSchemas() fs.FS { return frontendSchemas }
