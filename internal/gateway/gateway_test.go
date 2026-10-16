package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jjyr/pot/internal/secrets"
)

func provider(endpoint, protocol string) Provider {
	u, _ := url.Parse(endpoint)
	return Provider{"test", endpoint, []string{u.Hostname()}, protocol, "provider/test"}
}
func TestForwardProtocolsNoRedirectNoRetry(t *testing.T) {
	var second atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { second.Add(1) }))
	defer target.Close()
	for _, protocol := range []string{"openai_chat", "openai_responses", "anthropic_messages", "gemini"} {
		t.Run(protocol, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if r.Method != "POST" {
					t.Error(r.Method)
				}
				body, _ := io.ReadAll(r.Body)
				if string(body) != `{"test":1}` {
					t.Error(string(body))
				}
				switch protocol {
				case "anthropic_messages":
					if r.Header.Get("x-api-key") != "private-test" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("anthropic-beta") != "test-beta" {
						t.Error("missing Anthropic headers")
					}
				case "gemini":
					if r.Header.Get("x-goog-api-key") != "private-test" || r.URL.Query().Get("alt") != "sse" {
						t.Error("missing Gemini headers")
					}
				default:
					if r.Header.Get("Authorization") != "Bearer private-test" {
						t.Error("missing auth")
					}
				}
				w.Header().Set("Location", target.URL)
				w.WriteHeader(http.StatusTemporaryRedirect)
			}))
			defer server.Close()
			response, err := Forward(context.Background(), provider(server.URL, protocol), []byte("private-test"), json.RawMessage(`{"test":1}`), "test-beta", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 307 || calls.Load() != 1 || second.Load() != 0 {
				t.Fatal("redirect or duplicate", response.StatusCode, calls.Load(), second.Load())
			}
		})
	}
}
func TestRegistryImmutableEncryptedAndPinned(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control.sqlite")
	keys, _ := secrets.NewMemoryKey(make([]byte, 32))
	r, err := Open(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	p := provider("https://example.com/v1/responses", "openai_responses")
	key := []byte("private-provider-token")
	if err = r.Save(context.Background(), p, key); err != nil {
		t.Fatal(err)
	}
	if err = r.Save(context.Background(), p, []byte("changed")); err == nil {
		t.Fatal("replaced immutable credential")
	}
	changed := p
	changed.Endpoint = "https://example.com/other"
	if err = r.Save(context.Background(), changed, key); err == nil {
		t.Fatal("replaced immutable route")
	}
	if _, err = r.Credential(context.Background(), changed); err == nil {
		t.Fatal("unregistered route got key")
	}
	r.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), string(key)) {
		t.Fatal("plaintext credential in DB")
	}
	r, err = Open(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	value, err := r.Credential(context.Background(), p)
	if err != nil || string(value) != string(key) {
		t.Fatal(err)
	}
}
func TestValidationAndCancelledTransport(t *testing.T) {
	p := provider("ftp://public.example/v1/chat/completions", "openai_chat")
	if p.Validate() == nil {
		t.Fatal("accepted non-HTTP endpoint")
	}
	p = provider("https://example.com/v1/chat/completions", "openai_chat")
	p.AllowedHosts = []string{"another.example"}
	if p.Validate() == nil {
		t.Fatal("accepted unpinned host")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Forward(ctx, provider(server.URL, "openai_chat"), []byte("private"), json.RawMessage(`{}`), "", time.Second); err != context.Canceled {
		t.Fatal(err)
	}
}
