// Package providerplugin hosts trusted, independently packaged Sandbox Providers.
// Plugins run on the host; tool plugins remain a separate execution mechanism.
package providerplugin

import (
	"encoding/json"
	"github.com/jjyr/pot/pkg/sandbox"
)

const Version = 1

// The guest may return 8 MiB of JSON, in addition to this RPC envelope.
const maxMessage = 9 * 1024 * 1024

type Config struct {
	Root          string `json:"root"`
	UserSkills    string `json:"user_skills"`
	BuiltinSkills string `json:"builtin_skills"`
}
type bootstrap struct {
	Version int    `json:"version"`
	Config  Config `json:"config"`
	Socket  string `json:"socket"`
	Token   string `json:"token"`
}
type info struct {
	Descriptor     sandbox.Descriptor          `json:"descriptor"`
	Resources      sandbox.Resources           `json:"resources"`
	Workspace      string                      `json:"workspace"`
	GuestWorkspace string                      `json:"guest_workspace"`
	Cleanup        sandbox.CleanupPolicy       `json:"cleanup"`
	Desktop        sandbox.DesktopCapabilities `json:"desktop"`
}
type hello struct {
	Version int  `json:"version"`
	Info    info `json:"info"`
}
type request struct {
	Version   int                       `json:"version"`
	ID        string                    `json:"id"`
	Method    string                    `json:"method"`
	Instance  sandbox.Instance          `json:"instance"`
	Resources sandbox.Resources         `json:"resources"`
	Prepared  sandbox.PreparedResources `json:"prepared"`
	Operation string                    `json:"operation"`
	Input     json.RawMessage           `json:"input,omitempty"`
	TimeoutMS int64                     `json:"timeout_ms"`
}
type remoteError struct {
	Message            string `json:"message"`
	CleanupUnconfirmed bool   `json:"cleanup_unconfirmed"`
}
type response struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *remoteError    `json:"error,omitempty"`
}
