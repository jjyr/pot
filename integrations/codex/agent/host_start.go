package agent

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/jjyr/pot/internal/secrets"
	"github.com/jjyr/pot/pkg/storage"
)

func agentHostClient(token string) hostClient {
	return hostClient{token, &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_denied") }}}
}

// An active daemon owns migrations. Only the stable owner-secret reference is
// read here, so an older daemon can be rejected without rewriting its database.
func existingAgentHostClient(root string) (hostClient, error) {
	path := filepath.Join(root, "control.sqlite")
	db, err := storage.OpenDatabaseReadOnly(path, storage.ControlDatabase)
	if err != nil {
		return hostClient{}, err
	}
	defer db.Close()
	var ref string
	if err = db.QueryRow("SELECT owner_secret_ref FROM codex_agent_host WHERE singleton=1").Scan(&ref); err != nil {
		return hostClient{}, err
	}
	store, err := secrets.OpenReadOnly(path, secrets.FileKey{Path: filepath.Join(root, "master.key")})
	if err != nil {
		return hostClient{}, err
	}
	defer store.Close()
	token, err := store.Get(context.Background(), ref)
	if err != nil {
		return hostClient{}, err
	}
	defer clear(token)
	return agentHostClient(string(token)), nil
}

func (c hostClient) healthy() bool {
	var v struct {
		Version string `json:"version"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	return c.call(ctx, "/api/v1/agent-host/health", nil, &v) == nil && v.Version == hostVersion
}
