// Package buildinfo exposes metadata set by the product packaging linker flags.
package buildinfo

var Version = "development"
var Revision = "unknown"

func String() string { return "pot " + Version + " (" + Revision + ")" }
