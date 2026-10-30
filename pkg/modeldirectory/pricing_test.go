package modeldirectory

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPricingMatchingAndCacheRefresh(t *testing.T) {
	p := fixture()
	m := p["anthropic"].Models["claude-example"]
	m.Cost = json.RawMessage(`{"input":5,"output":25}`)
	p["anthropic"].Models["claude-example"] = m
	m = p["proxy"].Models["claude-example"]
	m.Cost = json.RawMessage(`{"input":7,"output":30}`)
	p["proxy"].Models["claude-example"] = m
	root := t.TempDir()
	b, _ := json.Marshal(snapshot{Providers: p, PricingVersion: 1, UpdatedAt: 123})
	if e := os.WriteFile(filepath.Join(root, "catalog.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	d, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	q := d.Price("proxy", "claude-example")
	if q == nil || q.Provider != "proxy" || q.Match != "provider" || string(q.Cost) != `{"input":7,"output":30}` {
		t.Fatal(q)
	}
	q = d.Price("unknown", "anthropic/claude-example")
	if q == nil || q.Provider != "anthropic" || q.Match != "origin_fallback" {
		t.Fatal(q)
	}
	// A known supplier with no quoted model also falls back to the origin.
	proxy := d.state.Providers["proxy"]
	entry := proxy.Models["claude-example"]
	entry.Cost = nil
	proxy.Models["claude-example"] = entry
	if fallback := d.Price("proxy", "claude-example"); fallback == nil || fallback.Provider != "anthropic" || fallback.Match != "origin_fallback" {
		t.Fatal(fallback)
	}
	old := q.Version
	// Unknown pricing dimensions must survive refresh and be visible to consumers.
	m = p["anthropic"].Models["claude-example"]
	m.Cost = json.RawMessage(`{"input":8,"output":40,"future_fee":99}`)
	p["anthropic"].Models["claude-example"] = m
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(p)
	}))
	defer server.Close()
	d.client = server.Client()
	d.source = server.URL + "/api"
	d.logoBase = server.URL + "/logos/"
	if e = d.Refresh(t.Context()); e != nil {
		t.Fatal(e)
	}
	d, e = Open(root)
	if e != nil {
		t.Fatal(e)
	}
	q = d.Price("anthropic", "claude-example")
	if q == nil || q.Version == old || q.UpdatedAt <= 123 || string(q.Cost) != string(m.Cost) {
		t.Fatal(q)
	}
	last := q.Version
	d.client = server.Client()
	d.source = server.URL + "/failure"
	if e = d.Refresh(t.Context()); e == nil {
		t.Fatal("expected refresh failure")
	}
	if preserved := d.Price("anthropic", "claude-example"); preserved == nil || preserved.Version != last {
		t.Fatal(preserved)
	}
}
func TestLegacyCachePricingUpgrade(t *testing.T) {
	root := t.TempDir()
	p := map[string]provider{"deepseek": {Name: "DeepSeek", Models: map[string]model{"deepseek-v4-flash": {Name: "Kept cached fact"}}}}
	tag := "old-etag"
	b, _ := json.Marshal(snapshot{Providers: p, ETag: &tag})
	if e := os.WriteFile(filepath.Join(root, "catalog.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	d, e := Open(root)
	if e != nil {
		t.Fatal(e)
	}
	q := d.Price("deepseek", "deepseek-v4-flash")
	if q == nil || d.state.ETag != nil || d.state.Providers["deepseek"].Models["deepseek-v4-flash"].Name != "Kept cached fact" {
		t.Fatal(q)
	}
}
