package core

import (
	"encoding/json"
	"errors"
	"net/url"
	"slices"
	"strings"
	"time"
)

// PluginBackground is a local, owner-enabled service capability. It never enters
// a transferable tool bundle and does not confer model/tool execution authority.
type PluginBackground struct {
	Runtime     string             `json:"runtime"`
	Execution   string             `json:"execution"`
	Entrypoint  string             `json:"entrypoint"`
	Files       []string           `json:"files"`
	Credentials CredentialBindings `json:"credentials,omitempty"`
}

func (b PluginBackground) Validate() error {
	if b.Runtime != "node" || b.Execution != "control" || len(b.Files) == 0 || len(b.Files) > 32 || !slices.Contains(b.Files, b.Entrypoint) {
		return errors.New("invalid background capability")
	}
	seen := map[string]bool{}
	for _, f := range b.Files {
		if !SafeRelativePath(f) || seen[f] {
			return errors.New("invalid background file")
		}
		seen[f] = true
	}
	return b.Credentials.Validate()
}

// PluginEvent is data, never an instruction or an address. Host binds provenance
// and routing from the installed package and owner configuration.
type PluginEvent struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	URL        string `json:"url,omitempty"`
	OccurredAt string `json:"occurred_at"`
}

func (e PluginEvent) Validate() error {
	if strings.TrimSpace(e.ID) == "" || len(e.ID) > 256 || len(e.Title) > 512 || strings.TrimSpace(e.Title) == "" || len(e.Body) > 24000 || strings.TrimSpace(e.Body) == "" || len(e.URL) > 2048 {
		return errors.New("invalid plugin event bounds")
	}
	if _, err := time.Parse(time.RFC3339, e.OccurredAt); err != nil {
		return errors.New("invalid plugin event time")
	}
	if e.URL != "" {
		u, err := url.Parse(e.URL)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return errors.New("invalid plugin event URL")
		}
	}
	encoded, _ := json.Marshal(e)
	if len(encoded) > 65536 {
		return errors.New("plugin event exceeds inbox text limit")
	}
	return nil
}

type BackgroundRequest struct {
	Version  int               `json:"version"`
	Now      int64             `json:"now"`
	Config   json.RawMessage   `json:"config"`
	State    json.RawMessage   `json:"state"`
	Accounts map[string]string `json:"accounts"`
}
type BackgroundResponse struct {
	State        json.RawMessage `json:"state"`
	Events       []PluginEvent   `json:"events"`
	DelaySeconds int             `json:"delay_seconds"`
	Status       string          `json:"status"`
}

func (r BackgroundResponse) Validate() error {
	if len(r.State) > 2*1024*1024 || !json.Valid(r.State) || len(r.Events) > 8 || r.DelaySeconds < 60 || r.DelaySeconds > 86400 || !slices.Contains([]string{"ready", "baseline", "backoff", "rate_limited", "authorization", "permission", "overflow"}, r.Status) {
		return errors.New("invalid background response")
	}
	for _, e := range r.Events {
		if err := e.Validate(); err != nil {
			return err
		}
	}
	return nil
}
