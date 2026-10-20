package core

import (
	"encoding/hex"
	"errors"
	"strings"
)

type ResourceFile struct {
	Path     string `json:"path"`
	MIMEType string `json:"mime_type"`
	Size     uint64 `json:"size"`
	Digest   string `json:"digest"`
}
type Resource struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	MIMEType string         `json:"mime_type"`
	Size     uint64         `json:"size"`
	Entry    string         `json:"entry"`
	Files    []ResourceFile `json:"files"`
	Download ResourceFile   `json:"download"`
}
type ResourcePresentation struct {
	Resource Resource `json:"resource"`
	Mode     string   `json:"mode"`
	Caption  string   `json:"caption,omitempty"`
}

func (p ResourcePresentation) Validate() error {
	r := p.Resource
	if !Identifier(r.ID) || r.Name == "" || len(r.Name) > 255 || strings.ContainsAny(r.Name, "/\\\x00") || len(p.Caption) > 1000 || p.Mode != "preview" && p.Mode != "download" || len(r.Files) == 0 || len(r.Files) > 64 || r.Size > 64<<20 {
		return errors.New("invalid resource presentation")
	}
	var total uint64
	found := false
	seen := map[string]bool{}
	validFile := func(file ResourceFile) bool {
		digest, err := hex.DecodeString(file.Digest)
		return SafeRelativePath(file.Path) && len(file.Path) <= 4096 && file.Size <= 64<<20 && file.MIMEType != "" && len(file.MIMEType) < 128 && err == nil && len(digest) == 32
	}
	for _, file := range r.Files {
		if !validFile(file) || seen[file.Path] {
			return errors.New("invalid resource manifest")
		}
		seen[file.Path] = true
		total += file.Size
		if file.Path == r.Entry && file.MIMEType == r.MIMEType {
			found = true
		}
	}
	if !found || total != r.Size || !validFile(r.Download) {
		return errors.New("invalid resource entry or download")
	}
	return nil
}
