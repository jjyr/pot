// Package selfupdate owns signed release discovery and installation-local updates.
// It never changes data formats, runs migrations, or coordinates remote machines.
package selfupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jjyr/pot/internal/buildinfo"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// StorageFormat versions non-SQL persisted contracts. The build additionally pins
// the centralized SQL migration fingerprint; automatic schema changes are rejected.
const StorageFormat = "pot-go-2026-10-4"
const releaseOrigin = "https://github.com/jjyr/pot/releases/download/"

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

type Artifact struct {
	Kind     string `json:"kind"`
	Platform string `json:"platform"`
	URL      string `json:"url"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}
type Release struct {
	Version       string     `json:"version"`
	StorageFormat string     `json:"storage_format"`
	Artifacts     []Artifact `json:"artifacts"`
}
type Envelope struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

func newer(candidate, current string) bool {
	if !versionPattern.MatchString(candidate) || !versionPattern.MatchString(current) {
		return false
	}
	a, b := strings.Split(candidate, "."), strings.Split(current, ".")
	for i := range a {
		x, e1 := strconv.ParseUint(a[i], 10, 64)
		y, e2 := strconv.ParseUint(b[i], 10, 64)
		if e1 != nil || e2 != nil {
			return false
		}
		if x != y {
			return x > y
		}
	}
	return false
}
func Verify(data []byte, key string) (Release, error) {
	var e Envelope
	var r Release
	if err := json.Unmarshal(data, &e); err != nil {
		return r, err
	}
	pub, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return r, errors.New("invalid update public key")
	}
	payload, err := base64.StdEncoding.DecodeString(e.Payload)
	if err != nil {
		return r, err
	}
	signature, err := base64.StdEncoding.DecodeString(e.Signature)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(pub), payload, signature) {
		return r, errors.New("invalid release signature")
	}
	if err = json.Unmarshal(payload, &r); err != nil {
		return r, err
	}
	if !versionPattern.MatchString(r.Version) || r.StorageFormat != storageFormat() {
		return r, errors.New("unsupported release version or storage format")
	}
	seen := map[string]bool{}
	for _, a := range r.Artifacts {
		digest, err := hex.DecodeString(a.SHA256)
		id := a.Kind + "/" + a.Platform
		if err != nil || len(digest) != sha256.Size || a.Size <= 0 || a.Size > 1<<30 || seen[id] || !strings.HasPrefix(a.URL, releaseOrigin+"v"+r.Version+"/") {
			return r, errors.New("invalid release artifact")
		}
		u, err := url.Parse(a.URL)
		if err != nil || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
			return r, errors.New("invalid release URL")
		}
		seen[id] = true
	}
	return r, nil
}
func client() *http.Client {
	return &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || req.URL.Scheme != "https" {
			return errors.New("unsafe update redirect")
		}
		return nil
	}}
}
func fetch(ctx context.Context, c *http.Client, address string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("update HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err == nil && int64(len(data)) > limit {
		err = errors.New("update size limit")
	}
	return data, err
}

func storageFormat() string {
	if buildinfo.UpdateSchemaDigest == "" {
		return StorageFormat
	}
	return StorageFormat + "-" + buildinfo.UpdateSchemaDigest
}
