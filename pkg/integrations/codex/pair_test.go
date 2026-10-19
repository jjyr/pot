package codex

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPairingVerifiesTLSIdentityRoleAndRoute(t *testing.T) {
	var role = "pot"
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("x", 48) {
			t.Error("authentication missing")
		}
		if r.URL.Path != "/v1/identity" {
			t.Error(r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "device", "control": "control", "assistant": "main", "role": role})
	}))
	defer api.Close()
	pairing := Pairing{Endpoint: api.URL, Token: strings.Repeat("x", 48), ID: "device", Route: "control/main", CA: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw}))}
	raw, _ := json.Marshal(pairing)
	grant, err := (Adapter{}).Import(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	if grant.Identity.ID != "device" || grant.Identity.Username != "control/main" || !strings.Contains(grant.Credential.AccessToken, "https://") {
		t.Fatal(grant.Identity)
	}
	for _, mutate := range []func(*Pairing){func(p *Pairing) { p.CA = "" }, func(p *Pairing) { p.Route = "another/main" }, func(p *Pairing) { p.Endpoint = "http://127.0.0.1:1" }, func(p *Pairing) { p.Endpoint += "?token=forbidden" }, func(p *Pairing) { p.Endpoint += "/unexpected" }} {
		bad := pairing
		mutate(&bad)
		raw, _ := json.Marshal(bad)
		if _, err = (Adapter{}).Import(context.Background(), raw); err == nil {
			t.Fatal("invalid pairing accepted")
		}
	}
	role = "codex"
	raw, _ = json.Marshal(pairing)
	if _, err = (Adapter{}).Import(context.Background(), raw); err == nil {
		t.Fatal("local MCP credential accepted as Pot pairing")
	}
}
