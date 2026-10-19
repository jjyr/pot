// Package buildinfo exposes metadata set by the product packaging linker flags.
package buildinfo

var Version = "development"
var Revision = "unknown"

func String() string { return "pot " + Version + " (" + Revision + ")" }

// UpdatePublicKey is the base64 Ed25519 release verification key, pinned at build.
// Empty disables self-update for source and unsigned development builds.
var UpdatePublicKey string

// UpdateSchemaDigest pins the centralized migration set for safe binary rollback.
var UpdateSchemaDigest string
