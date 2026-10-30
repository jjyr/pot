package usage

import (
	"encoding/json"
	"testing"

	"github.com/jjyr/pot/pkg/modeldirectory"
)

// These exact decimal expectations are independently hand-calculated from the
// bundled models.dev price evidence. No test fetches metadata or calls a model.
func TestCatalogPricingExamples(t *testing.T) {
	d, e := modeldirectory.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range []struct {
		name, provider, model, origin, match string
		in, out, read, write                 int64
		want                                 string
	}{
		{"DeepSeek provider", "deepseek", "deepseek-v4-flash", "deepseek", "provider", 100000, 10000, 80000, 0, "0.009240000000000"},
		{"DeepSeek custom gateway", "custom-proxy", "deepseek-v4-flash", "deepseek", "origin_fallback", 100000, 10000, 80000, 0, "0.009240000000000"},
		{"Opus origin fallback", "", "anthropic/claude-opus-4.6", "anthropic", "origin_fallback", 100000, 5000, 20000, 10000, "0.547500000000000"},
		{"OpenRouter long context", "openrouter", "anthropic/claude-opus-4.6", "openrouter", "provider", 250000, 10000, 50000, 0, "2.425000000000000"},
		{"Opus original provider same tokens", "anthropic", "claude-opus-4-6", "anthropic", "provider", 250000, 10000, 50000, 0, "1.275000000000000"},
		{"Gemini long context", "google", "gemini-2.5-pro", "google", "provider", 250000, 10000, 50000, 0, "0.662500000000000"},
		{"Gemini threshold inclusive base", "google", "gemini-2.5-pro", "google", "provider", 200000, 10000, 0, 0, "0.350000000000000"},
		{"GPT4.1 cache", "openai", "gpt-4.1", "openai", "provider", 100000, 10000, 40000, 0, "0.220000000000000"},
	} {
		t.Run(c.name, func(t *testing.T) {
			q := d.Price(c.provider, c.model)
			if q == nil || q.Source != "models.dev" || q.Provider != c.origin || q.Match != c.match || q.Version == "" || q.UpdatedAt == 0 {
				t.Fatalf("%+v", q)
			}
			p := CatalogPrice(q)
			got, e := Cost(*p, &c.in, &c.out, &c.read, &c.write)
			if e != nil || got != c.want {
				t.Fatalf("got %s want %s price=%+v error=%v", got, c.want, p, e)
			}
			t.Logf("%s/%s %s: USD %s", q.Provider, q.Model, q.Match, got)
		})
	}
	for _, id := range []string{"opus", "claude-opus", "claude-opus-99", "deepseek-missing", "some-random-model"} {
		if q := d.Price("", id); q != nil {
			t.Fatalf("invented model match: %s %+v", id, q)
		}
	}
}
func TestCatalogUnsupportedAndMissingDimensions(t *testing.T) {
	for _, raw := range []string{
		`{"input":1,"output":2,"input_audio":3}`,
		`{"input":1,"output":2,"reasoning":4}`,
		`{"input":1,"output":2,"tiers":[{"input":3,"tier":{"type":"batch","size":200000}}]}`,
		`{"input":1,"output":2,"tiers":[{"input":3,"tier":{"type":"context","size":200000}}],"context_over_200k":{"input":4}}`,
		`{"input":1,"output":null}`, `{"input":-1,"output":2}`,
	} {
		p := CatalogPrice(&modeldirectory.PriceQuote{Source: "models.dev", Version: "test", Cost: json.RawMessage(raw)})
		if p.Unsupported == "" {
			t.Fatal("unsupported price accepted", raw)
		}
		if _, e := Cost(*p, number(1), number(1), number(0), number(0)); e == nil {
			t.Fatal("unsupported quote priced")
		}
	}
	p := CatalogPrice(&modeldirectory.PriceQuote{Version: "test", Cost: json.RawMessage(`{"input":1.25,"output":10,"cache_read":0.125,"context_over_200k":{"input":2.5,"output":15,"cache_read":0.25}}`)})
	if p.Unsupported != "" {
		t.Fatal(p)
	}
	got, e := Cost(*p, number(200001), number(10000), number(0), nil)
	if e != nil || got != "0.650002500000000" {
		t.Fatal(got, e)
	}
	if _, e = Cost(*p, number(200001), number(10000), nil, nil); e == nil {
		t.Fatal("unknown cache silently priced")
	}
}
func TestCatalogSnapshotPersistsAcrossRefreshAndRestart(t *testing.T) {
	l, path := ledger(t)
	d, e := modeldirectory.Open(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	r := reservation("catalog", 123)
	r.Price = CatalogPrice(d.Price("deepseek", "deepseek-v4-flash"))
	if _, e = l.Reserve(t.Context(), r); e != nil {
		t.Fatal(e)
	}
	// A later refresh returns changed rates; replay cannot rewrite the snapshot.
	changed := *r.Price
	changed.Input = "999"
	changed.Version = "new-catalog"
	r.Price = &changed
	if v, e := l.Reserve(t.Context(), r); e != nil || !v.Duplicate {
		t.Fatal(v, e)
	}
	if e = l.Settle(t.Context(), r.ID, Frame{Input: number(100000), Output: number(10000), Cache: number(80000)}); e != nil {
		t.Fatal(e)
	}
	l.Close()
	l, e = Open(path)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	v, e := l.Accounting(t.Context(), AccountingQuery{Query: Query{Start: "1970-01-01", End: "1970-01-01", Timezone: "UTC"}, Details: true}, 1000)
	if e != nil || v.Total.USD != "0.009240000000000" || len(v.Records) != 1 || v.Records[0].Price.Version == "new-catalog" || v.Records[0].Price.Source != "models.dev" {
		t.Fatal(v, e)
	}
}
