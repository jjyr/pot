// Package sandbox owns platform-independent definitions, leases, execution
// receipts and generation fencing. Platform providers implement the private
// lifecycle/guest boundary; authorization and resource consent belong to Control.
package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/core"
)

const (
	AppleVM  = "macos.apple-virtualization"
	Podman   = "podman"
	SharedID = "shared"
)

type Capabilities struct {
	Shell           bool `json:"shell"`
	Files           bool `json:"files"`
	Desktop         bool `json:"desktop"`
	SharedExecution bool `json:"shared_execution"`
}
type Descriptor struct {
	ID           string       `json:"id"`
	OS           string       `json:"os"`
	HostOS       string       `json:"host_os"`
	Engine       string       `json:"engine"`
	Capabilities Capabilities `json:"capabilities"`
}
type Resources struct {
	CPUs      uint64 `json:"cpus"`
	MemoryGiB uint64 `json:"memory_gib"`
	DiskGiB   uint64 `json:"disk_gib"`
}
type PreparedResources struct {
	BundleID       string `json:"bundle_id"`
	Image          string `json:"image"`
	ImageReference string `json:"image_reference"`
	ManifestDigest string `json:"manifest_digest"`
	Architecture   string `json:"architecture"`
}

// Instance is constructed from durable owner configuration, never model paths.
type Instance struct {
	Root      string
	ID        string
	Resources Resources
	Mappings  []HostMapping
}

func (i Instance) Directory() string { return filepath.Join(i.Root, "sandboxes", i.ID) }

type Status struct {
	Phase      string `json:"phase"`
	Generation uint64 `json:"generation,string"`
}
type CleanupPolicy string

const (
	ProcessContainment CleanupPolicy = "process-containment"
	SessionClosure     CleanupPolicy = "session-closure"
)

func (p CleanupPolicy) Confirmed(response map[string]any) bool {
	switch p {
	case ProcessContainment:
		return response["settled"] == true
	case SessionClosure:
		return response["scope_closed"] == true
	default:
		return false
	}
}

type Provider interface {
	Descriptor() Descriptor
	DefaultResources() Resources
	ValidateResources(Instance, Resources) error
	Workspace(Instance) string
	GuestWorkspace() string
	CleanupPolicy() CleanupPolicy
	BindResources(context.Context, Instance, PreparedResources) error
	Lifecycle(context.Context, Instance, string) (Status, error)
	Health(context.Context, Instance) (bool, error)
	Request(context.Context, Instance, string, json.RawMessage, time.Duration) (json.RawMessage, error)
	Shutdown(context.Context) error
}

type Definition struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	Provider        string        `json:"provider"`
	Resources       Resources     `json:"resources"`
	Mappings        []HostMapping `json:"mappings"`
	ManagerEpoch    uint64        `json:"manager_epoch,string"`
	GuestGeneration *uint64       `json:"guest_generation,string"`
	Quarantined     bool          `json:"quarantined"`
	Retired         bool          `json:"retired"`
}
type AccessGrant struct {
	Target           core.ExecutionTarget
	Owner            string
	Turn             string
	WorkspaceOwner   string
	ThreadGeneration uint64
	Worker           bool
	Policy           core.ShellSettings
}

func validID(value string) bool {
	return value != "" && len(value) <= 48 && strings.Trim(value, "abcdefghijklmnopqrstuvwxyz0123456789-") == ""
}
func (g AccessGrant) validate(runtime string) error {
	if err := g.Target.Validate(); err != nil {
		return err
	}
	if !g.Target.IsLocalTo(runtime) || g.Target.Target.Kind != "sandbox" || !validID(g.Target.Target.SandboxID) {
		return fmt.Errorf("Sandbox grant targets a different Runtime or invalid Sandbox")
	}
	if g.Owner == "" || len(g.Owner) > 512 || g.Turn == "" || len(g.Turn) > 256 {
		return fmt.Errorf("missing execution owner/turn/epoch")
	}
	if g.Worker && (!core.Identifier(g.WorkspaceOwner) || strings.ContainsAny(g.WorkspaceOwner, "/\\")) {
		return fmt.Errorf("invalid Task workspace owner")
	}
	return g.Policy.Validate()
}
