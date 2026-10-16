// Package resources embeds trusted non-code product resource descriptors.
package resources

import _ "embed"

//go:embed sandbox/catalog.json
var SandboxCatalog []byte

//go:embed model-directory.json
var ModelDirectory []byte
