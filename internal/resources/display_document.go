package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
)

// DisplayDocument is a complete inert transport template, not a captured DOM.
// Its token is a public placeholder; each iframe receives a new mount token.
type DisplayDocument struct {
	Version  int    `json:"version"`
	Resource string `json:"resource"`
	Entry    string `json:"entry"`
	Files    []File `json:"files"`
	Token    string `json:"token"`
	HTML     string `json:"html"`
	Digest   string `json:"digest"`
}

func RenderDisplayDocument(resource Resource, read func(string) ([]byte, error)) (DisplayDocument, error) {
	const token = "pot-resource-mount-token-v1"
	if resource.MIMEType != "text/html" || resource.Size > BundleLimit || len(resource.Files) == 0 || len(resource.Files) > 64 {
		return DisplayDocument{}, errors.New("invalid HTML resource")
	}
	// Recheck dependency completeness for historical registrations too. The reader
	// can access only manifest members, never a directory, URL or source path.
	var declared uint64
	for _, file := range resource.Files {
		declared += file.Size
		if declared > BundleLimit {
			return DisplayDocument{}, errors.New("invalid HTML bundle size")
		}
	}
	if declared != resource.Size {
		return DisplayDocument{}, errors.New("invalid HTML manifest size")
	}
	verified := map[string][]byte{}
	for _, file := range resource.Files {
		raw, err := read(file.Path)
		if err != nil {
			return DisplayDocument{}, err
		}
		digest := sha256.Sum256(raw)
		if uint64(len(raw)) != file.Size || hex.EncodeToString(digest[:]) != file.Digest {
			return DisplayDocument{}, errors.New("resource digest mismatch")
		}
		verified[file.Path] = raw
	}
	original, ok := verified[resource.Entry]
	if !ok {
		return DisplayDocument{}, ErrDependency
	}
	if _, _, err := CollectHTML(resource.Entry, original, func(path string, limit int) ([]byte, error) {
		raw, ok := verified[path]
		if !ok || len(raw) > limit {
			return nil, ErrDependency
		}
		return raw, nil
	}); err != nil {
		return DisplayDocument{}, err
	}
	raw, err := RenderHTML(resource, func(path string) ([]byte, error) {
		value, ok := verified[path]
		if !ok {
			return nil, ErrDependency
		}
		return value, nil
	}, token)
	if err != nil {
		return DisplayDocument{}, err
	}
	// Fits the shared bounded JSON operation port, including JSON escaping.
	if len(raw) > 8*1024*1024 {
		return DisplayDocument{}, errors.New("HTML offline display limit")
	}
	// The independent frame response owns HTMLPolicy; retain the subresource CSP
	// when document.write replaces its shell. iframe sandbox retains opaque origin.
	document := strings.Replace(string(raw), "<head>", `<head><meta http-equiv="Content-Security-Policy" content="`+strings.TrimSuffix(HTMLPolicy, "; sandbox allow-scripts")+`">`, 1)
	digest := sha256.Sum256([]byte(document))
	result := DisplayDocument{1, resource.ID, resource.Entry, resource.Files, token, document, hex.EncodeToString(digest[:])}
	encoded, err := json.Marshal(result)
	if err != nil {
		return DisplayDocument{}, err
	}
	if len(encoded) > 16*1024*1024 {
		return DisplayDocument{}, errors.New("HTML display JSON limit")
	}
	return result, nil
}
