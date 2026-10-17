// Package resources embeds trusted non-code product resource descriptors.
package resources

import "embed"

//go:embed model-directory.json
var ModelDirectory []byte

// ProviderLogos contains trusted, bundled brand artwork that takes precedence
// over the model directory's upstream monochrome marks.
//
//go:embed provider-logos/*.svg
var ProviderLogos embed.FS
