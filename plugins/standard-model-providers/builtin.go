// Package standardproviders retains the declarative provider contribution when
// a headless embedding has no packaged resource directory. It uses the same
// manifest validation and extension enablement as physically installed packages.
package standardproviders

import _ "embed"

//go:embed plugin.json
var Manifest []byte
