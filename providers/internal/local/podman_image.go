package sandboxprovider

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/jjyr/pot/pkg/sandbox"
)

type ociDescriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int64             `json:"size"`
	Annotations map[string]string `json:"annotations"`
}
type verifiedPodmanImage struct {
	ConfigDigest string
	Architecture string
	Files        []string
	Resources    imageResources
}

func validDigest(digest string) bool {
	if len(digest) != 71 || !strings.HasPrefix(digest, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(digest[7:])
	return err == nil && strings.ToLower(digest) == digest
}
func readOCIFile(root, name string, limit int64) ([]byte, error) {
	file := filepath.Join(root, name)
	canonical, err := filepath.EvalSymlinks(file)
	if err != nil || canonical != file {
		return nil, errors.New("OCI content must not contain symlinks")
	}
	info, err := os.Lstat(file)
	if err != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("invalid OCI metadata file")
	}
	return os.ReadFile(file)
}
func verifyPodmanImage(resources imageResources) (verifiedPodmanImage, error) {
	out := verifiedPodmanImage{Resources: resources}
	if err := validatePaths(resources); err != nil {
		return out, err
	}
	if !validDigest(resources.ManifestDigest) || (resources.Architecture != "amd64" && resources.Architecture != "arm64") {
		return out, errors.New("invalid prepared OCI manifest or architecture")
	}
	layout, err := readOCIFile(resources.Image, "oci-layout", 4096)
	if err != nil {
		return out, err
	}
	var version struct {
		ImageLayoutVersion string `json:"imageLayoutVersion"`
	}
	if json.Unmarshal(layout, &version) != nil || version.ImageLayoutVersion != "1.0.0" {
		return out, errors.New("invalid OCI layout version")
	}
	raw, err := readOCIFile(resources.Image, "index.json", 1024*1024)
	if err != nil {
		return out, err
	}
	var index struct {
		SchemaVersion int             `json:"schemaVersion"`
		Manifests     []ociDescriptor `json:"manifests"`
	}
	if json.Unmarshal(raw, &index) != nil || index.SchemaVersion != 2 || len(index.Manifests) == 0 || len(index.Manifests) > 64 {
		return out, errors.New("invalid OCI root index")
	}
	selected := false
	matched := false
	seen := map[string]bool{}
	out.Files = []string{"oci-layout", "index.json"}
	var walk func(ociDescriptor, bool, int) error
	walk = func(d ociDescriptor, chosen bool, depth int) error {
		if depth > 8 || !validDigest(d.Digest) || d.Size < 0 || d.Size > 16*1024*1024*1024 {
			return errors.New("invalid OCI descriptor")
		}
		if chosen && d.Digest == resources.ManifestDigest {
			matched = true
		}
		if seen[d.Digest] {
			return nil
		}
		seen[d.Digest] = true
		if len(seen) > 512 {
			return errors.New("OCI descriptor limit exceeded")
		}
		name := filepath.Join("blobs", "sha256", d.Digest[7:])
		path := filepath.Join(resources.Image, name)
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil || canonical != path {
			return errors.New("OCI blob must not use symlinks")
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() != d.Size {
			file.Close()
			return errors.New("OCI blob size mismatch")
		}
		hash := sha256.New()
		_, err = io.Copy(hash, file)
		file.Close()
		if err != nil || hex.EncodeToString(hash.Sum(nil)) != d.Digest[7:] {
			return errors.New("OCI blob checksum mismatch")
		}
		out.Files = append(out.Files, name)
		switch d.MediaType {
		case "application/vnd.oci.image.index.v1+json", "application/vnd.docker.distribution.manifest.list.v2+json":
			raw, err := readOCIFile(resources.Image, name, 4*1024*1024)
			if err != nil {
				return err
			}
			var child struct {
				SchemaVersion int             `json:"schemaVersion"`
				Manifests     []ociDescriptor `json:"manifests"`
			}
			if json.Unmarshal(raw, &child) != nil || child.SchemaVersion != 2 || len(child.Manifests) > 64 {
				return errors.New("invalid OCI nested index")
			}
			for _, next := range child.Manifests {
				if err = walk(next, chosen, depth+1); err != nil {
					return err
				}
			}
		case "application/vnd.oci.image.manifest.v1+json", "application/vnd.docker.distribution.manifest.v2+json":
			raw, err := readOCIFile(resources.Image, name, 4*1024*1024)
			if err != nil {
				return err
			}
			var child struct {
				SchemaVersion int             `json:"schemaVersion"`
				Config        ociDescriptor   `json:"config"`
				Layers        []ociDescriptor `json:"layers"`
			}
			if json.Unmarshal(raw, &child) != nil || child.SchemaVersion != 2 || len(child.Layers) > 128 {
				return errors.New("invalid OCI image manifest")
			}
			if err = walk(child.Config, false, depth+1); err != nil {
				return err
			}
			for _, next := range child.Layers {
				if err = walk(next, false, depth+1); err != nil {
					return err
				}
			}
			if chosen {
				config, err := readOCIFile(resources.Image, filepath.Join("blobs", "sha256", child.Config.Digest[7:]), 4*1024*1024)
				if err != nil {
					return err
				}
				var value struct {
					OS           string `json:"os"`
					Architecture string `json:"architecture"`
					Config       struct {
						Labels map[string]string `json:"Labels"`
					} `json:"config"`
				}
				if json.Unmarshal(config, &value) != nil {
					return errors.New("invalid OCI image configuration")
				}
				if value.OS != "linux" || value.Architecture != resources.Architecture {
					return errors.New("prepared OCI platform differs from requested guest")
				}
				if value.Config.Labels["dev.pot.bootstrap"] != "pot-executor-v2" {
					return errors.New("prepared OCI image lacks the Pot executor bootstrap")
				}
				if out.ConfigDigest != "" && out.ConfigDigest != child.Config.Digest {
					return errors.New("prepared OCI reference selects multiple different images")
				}
				out.ConfigDigest = child.Config.Digest
				out.Architecture = value.Architecture
			}
		}
		return nil
	}
	sort.SliceStable(index.Manifests, func(a, b int) bool {
		return index.Manifests[a].Annotations["org.opencontainers.image.ref.name"] == resources.ImageReference && index.Manifests[b].Annotations["org.opencontainers.image.ref.name"] != resources.ImageReference
	})
	for _, d := range index.Manifests {
		annotation := d.Annotations["org.opencontainers.image.ref.name"]
		chosen := annotation == resources.ImageReference
		if chosen {
			selected = true
		}
		if err = walk(d, chosen, 0); err != nil {
			return out, err
		}
	}
	if !selected || !matched || out.ConfigDigest == "" {
		return out, errors.New("prepared image reference or manifest digest is absent from the verified OCI graph")
	}
	sort.Strings(out.Files)
	return out, nil
}
func (p *Podman) preparedImage(ctx context.Context, instance sandbox.Instance) (string, error) {
	var resources imageResources
	if err := loadJSON(filepath.Join(instance.Directory(), "prepared-resources.json"), &resources); err != nil {
		return "", errors.New("prepare and bind the verified Sandbox OCI resource before starting")
	}
	image, err := verifyPodmanImage(resources)
	if err != nil {
		return "", err
	}
	if image.Architecture != runtime.GOARCH {
		return "", errors.New("prepared OCI architecture differs from this Runtime")
	}
	check := func() (bool, error) {
		raw, code, err := p.command(ctx, "image", "inspect", image.ConfigDigest)
		if err != nil {
			return false, err
		}
		if code != 0 {
			return false, nil
		}
		var rows []map[string]any
		if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 {
			return false, errors.New("invalid imported image response")
		}
		actual, _ := rows[0]["Id"].(string)
		actual = "sha256:" + strings.TrimPrefix(actual, "sha256:")
		if actual != image.ConfigDigest || nested(rows[0], "Labels", "dev.pot.bootstrap") != "pot-executor-v2" || rows[0]["Architecture"] != image.Architecture {
			return false, errors.New("loaded image differs from the verified OCI configuration")
		}
		return true, nil
	}
	if exists, err := check(); err != nil {
		return "", err
	} else if exists {
		return image.ConfigDigest[7:], nil
	}
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		archive := tar.NewWriter(writer)
		var result error
		for _, name := range image.Files {
			path := filepath.Join(resources.Image, name)
			file, err := os.Open(path)
			if err != nil {
				result = err
				break
			}
			info, err := file.Stat()
			if err != nil {
				file.Close()
				result = err
				break
			}
			header, err := tar.FileInfoHeader(info, "")
			if err == nil {
				header.Name = filepath.ToSlash(name)
				header.Mode = 0644
				err = archive.WriteHeader(header)
			}
			if err == nil {
				_, err = io.Copy(archive, file)
			}
			file.Close()
			if err != nil {
				result = err
				break
			}
		}
		if err := archive.Close(); result == nil {
			result = err
		}
		writer.CloseWithError(result)
		done <- result
	}()
	prefix := []string{"--remote=false"}
	if p.hostOS == "darwin" {
		name, _, _ := p.machineIdentity()
		prefix = []string{"--remote=true", "--connection=" + name}
	}
	_, code, err := p.runCommand(ctx, reader, append(prefix, "load")...)
	reader.Close()
	streamErr := <-done
	if err != nil || code != 0 || streamErr != nil {
		return "", errors.Join(err, streamErr, errors.New("verified Podman OCI import failed"))
	}
	if exists, err := check(); err != nil {
		return "", err
	} else if !exists {
		return "", fmt.Errorf("verified OCI image %s was not imported", image.ConfigDigest)
	}
	return image.ConfigDigest[7:], nil
}
