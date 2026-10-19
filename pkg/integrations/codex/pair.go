// Package codex validates owner-supplied Codex Bridge pairings. Credentials are
// stored by the host's ordinary encrypted integration store, never plugin config.
package codex

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jjyr/pot/pkg/integrations"
)

type Pairing struct {
	Endpoint string `json:"endpoint"`
	Token    string `json:"token"`
	ID       string `json:"id"`
	Route    string `json:"route"`
	CA       string `json:"ca,omitempty"`
}
type Adapter struct{}

func (Adapter) Descriptor() integrations.Descriptor {
	return integrations.Descriptor{ID: "codex", Name: "Codex Bridge"}
}
func (Adapter) Begin(context.Context) (integrations.Challenge, error) {
	return integrations.Challenge{}, errors.New("use_pot_codex_pair")
}
func (Adapter) Authorize(context.Context, integrations.Challenge) (integrations.Grant, error) {
	return integrations.Grant{}, errors.New("use_pot_codex_pair")
}
func (Adapter) Import(ctx context.Context, raw json.RawMessage) (integrations.Grant, error) {
	var p Pairing
	d := json.NewDecoder(strings.NewReader(string(raw)))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || len(raw) > 16384 {
		return integrations.Grant{}, errors.New("invalid_codex_pairing")
	}
	u, err := url.Parse(p.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !(u.Path == "" || u.Path == "/") || len(p.Token) < 32 || len(p.Token) > 128 || p.ID == "" || p.Route == "" {
		return integrations.Grant{}, errors.New("invalid_codex_pairing")
	}
	roots, err := x509.SystemCertPool()
	if err != nil {
		return integrations.Grant{}, err
	}
	if p.CA != "" && !roots.AppendCertsFromPEM([]byte(p.CA)) {
		return integrations.Grant{}, errors.New("invalid_codex_ca")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_denied") }}
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(p.Endpoint, "/")+"/v1/identity", nil)
	if err != nil {
		return integrations.Grant{}, errors.New("invalid_codex_endpoint")
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	resp, err := client.Do(req)
	if err != nil {
		return integrations.Grant{}, errors.New("codex_bridge_unreachable_check_tls_and_address")
	}
	defer resp.Body.Close()
	var identity struct{ ID, Control, Assistant, Role string }
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16385))
	if err != nil || len(data) > 16384 || resp.StatusCode != 200 || json.Unmarshal(data, &identity) != nil || identity.Role != "pot" || identity.ID != p.ID || identity.Control+"/"+identity.Assistant != p.Route {
		return integrations.Grant{}, errors.New("codex_pairing_identity_mismatch")
	}
	token, _ := json.Marshal(p)
	return integrations.Grant{Identity: integrations.Identity{ID: p.ID, Username: p.Route}, Credential: integrations.Credential{AccessToken: string(token)}}, nil
}
