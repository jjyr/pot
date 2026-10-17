package github

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type redirectTransport struct{ base *url.URL }

func (r redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = r.base.Scheme
	u.Host = r.base.Host
	copy.URL = &u
	return http.DefaultTransport.RoundTrip(copy)
}
func TestDeviceAuthorizationIdentityAndRefresh(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		r.ParseForm()
		switch r.URL.Path {
		case "/login/device/code":
			if r.Form.Get("client_id") != "fixture-client" || r.Form.Get("scope") != "repo read:user" {
				t.Error("incorrect app or scopes")
			}
			json.NewEncoder(w).Encode(map[string]any{"device_code": "never-public-device", "user_code": "TEST-CODE", "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 5})
		case "/login/oauth/access_token":
			if r.Form.Get("grant_type") == "refresh_token" {
				if r.Form.Get("refresh_token") != "private-refresh" {
					t.Error("refresh token missing")
				}
				io.WriteString(w, `{"access_token":"next-access","refresh_token":"next-refresh","expires_in":3600}`)
				return
			}
			if r.Form.Get("device_code") != "never-public-device" {
				t.Error("device code missing")
			}
			if polls.Add(1) == 1 {
				io.WriteString(w, `{"error":"authorization_pending"}`)
			} else {
				io.WriteString(w, `{"access_token":"private-access","refresh_token":"private-refresh","expires_in":3600}`)
			}
		case "/user":
			if r.Header.Get("Authorization") != "Bearer private-access" {
				t.Error("identity request missing token")
			}
			io.WriteString(w, `{"id":42,"login":"fixture-user","name":"Fixture"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	adapter := New(Options{ClientID: "fixture-client", HTTPClient: &http.Client{Transport: redirectTransport{base}}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	challenge, err := adapter.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the protocol test fast while testing production interval normalization.
	state := challenge.Private.(device)
	if state.Interval != 5*time.Second {
		t.Fatal("polling interval not respected")
	}
	state.Interval = time.Millisecond
	challenge.Private = state
	grant, err := adapter.Authorize(ctx, challenge)
	if err != nil || grant.Identity.Username != "fixture-user" {
		t.Fatal(grant.Identity, err)
	}
	next, err := adapter.Refresh(ctx, grant.Credential)
	if err != nil || next.AccessToken != "next-access" {
		t.Fatal("refresh", err)
	}
}
func TestAuthorizationErrorsNeverExposeResponseBodies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, "private-token-device-secret")
	}))
	defer server.Close()
	base, _ := url.Parse(server.URL)
	a := New(Options{ClientID: "test", HTTPClient: &http.Client{Transport: redirectTransport{base}}})
	_, err := a.Begin(context.Background())
	if err == nil || strings.Contains(err.Error(), "private-") {
		t.Fatal("upstream response exposed", err)
	}
}
