package sandboxprovider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
)

// This raw applehv disk is the single layer of the aarch64/applehv artifact in
// defaultPodmanMachineImage. Keep its exact bytes pinned independently of the
// registry tag: Podman 6.1's machine importer appends a tag to digest references.
type machineImageArtifact struct {
	URL, SHA256 string
	Size        int64
}

var podmanAppleHVImage = machineImageArtifact{
	URL:    "https://quay.io/v2/podman/machine-os/blobs/sha256:d92af11c2980615ad1645fd36999f5abb53e4118a7a19fc981fbaccf57de1737",
	SHA256: "d92af11c2980615ad1645fd36999f5abb53e4118a7a19fc981fbaccf57de1737",
	Size:   929229980,
}

func preparePodmanMachineImage(ctx context.Context, cacheRoot, reference string) (string, error) {
	if reference != defaultPodmanMachineImage || runtime.GOARCH != "arm64" {
		return "", errors.New("unsupported Podman machine OS; the managed macOS machine requires the pinned Apple Silicon image")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != "https" || len(via) >= 5 {
			return errors.New("invalid machine image redirect")
		}
		return nil
	}}
	return fetchPodmanMachineImage(ctx, cacheRoot, podmanAppleHVImage, client)
}

func machineImageMatches(ctx context.Context, path string, source machineImageArtifact) (bool, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return false, errors.New("machine image cache must be an owner-private regular file")
	}
	if info.Size() != source.Size {
		return false, nil
	}
	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	for {
		if err = ctx.Err(); err != nil {
			return false, err
		}
		n, readErr := file.Read(buffer)
		if n > 0 {
			_, _ = hash.Write(buffer[:n])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return false, readErr
		}
	}
	return hex.EncodeToString(hash.Sum(nil)) == source.SHA256, nil
}

func fetchPodmanMachineImage(ctx context.Context, cacheRoot string, source machineImageArtifact, client *http.Client) (string, error) {
	if !filepath.IsAbs(cacheRoot) || !validDigest("sha256:"+source.SHA256) || source.Size <= 0 {
		return "", errors.New("invalid pinned machine image")
	}
	if err := os.MkdirAll(cacheRoot, 0700); err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(cacheRoot)
	if err != nil || canonical != cacheRoot {
		return "", errors.New("machine image cache must not use symlinks")
	}
	path := filepath.Join(cacheRoot, "machine-os-"+source.SHA256+".raw.zst")
	if ok, err := machineImageMatches(ctx, path, source); err != nil || ok {
		return path, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch pinned Podman machine image: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength >= 0 && response.ContentLength != source.Size {
		return "", errors.New("machine image response status or size differs from the pin")
	}
	file, err := os.CreateTemp(cacheRoot, ".machine-download-")
	if err != nil {
		return "", err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if err = file.Chmod(0600); err != nil {
		return "", err
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, source.Size+1))
	if err != nil {
		return "", err
	}
	if written != source.Size || hex.EncodeToString(hash.Sum(nil)) != source.SHA256 {
		return "", errors.New("machine image checksum or size differs from the pin")
	}
	if err = file.Sync(); err != nil {
		return "", err
	}
	if err = file.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
