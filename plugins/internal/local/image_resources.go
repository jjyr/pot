package sandboxprovider

import (
	"encoding/json"
	"errors"
	"github.com/jjyr/pot/pkg/core"
	"github.com/jjyr/pot/pkg/sandbox"
)

type imageMetadata struct {
	ImageReference   string `json:"image_reference"`
	ManifestDigest   string `json:"manifest_digest"`
	BootstrapVersion string `json:"bootstrap_version"`
	OS               string `json:"os"`
}
type imageResources struct {
	BundleID       string `json:"bundle_id"`
	Architecture   string `json:"architecture"`
	Image          string `json:"image"`
	ImageReference string `json:"image_reference"`
	ManifestDigest string `json:"manifest_digest"`
}

func decodeImageResources(p sandbox.PreparedResources) (imageResources, error) {
	var m imageMetadata
	if err := core.DecodeJSON(p.Metadata, &m); err != nil {
		return imageResources{}, err
	}
	if p.Provider != PodmanID || len(p.Paths) != 1 || m.OS != "linux" || m.BootstrapVersion != "pot-executor-v2" {
		return imageResources{}, errors.New("incompatible Podman resource bundle")
	}
	return imageResources{BundleID: p.BundleID, Architecture: p.Architecture, Image: p.Paths["image"], ImageReference: m.ImageReference, ManifestDigest: m.ManifestDigest}, nil
}
func (r imageResources) prepared() sandbox.PreparedResources {
	metadata, _ := json.Marshal(imageMetadata{ImageReference: r.ImageReference, ManifestDigest: r.ManifestDigest, BootstrapVersion: "pot-executor-v2", OS: "linux"})
	return sandbox.PreparedResources{BundleID: r.BundleID, Provider: PodmanID, Architecture: r.Architecture, Paths: map[string]string{"image": r.Image}, Metadata: metadata}
}
