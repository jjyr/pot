package usage

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"

	"github.com/jjyr/pot/pkg/modeldirectory"
)

type PriceTier struct {
	Above      int64  `json:"above"`
	Input      string `json:"input"`
	Output     string `json:"output"`
	CacheRead  string `json:"cache_read,omitempty"`
	CacheWrite string `json:"cache_write,omitempty"`
}

func CatalogPrice(q *modeldirectory.PriceQuote) *Price {
	if q == nil {
		return nil
	}
	p, e := parseCatalogCost(q.Cost)
	if e != nil {
		p.Unsupported = e.Error()
	}
	p.Source = q.Source
	p.Provider = q.Provider
	p.Model = q.Model
	p.Match = q.Match
	p.Version = q.Version
	p.UpdatedAt = q.UpdatedAt
	return &p
}
func catalogRate(raw json.RawMessage) (string, error) {
	if len(raw) == 0 {
		return "", nil
	}
	// json.Number preserves upstream decimal literals, including exponents.
	var n json.Number
	if e := json.Unmarshal(raw, &n); e != nil || n.String() == "" {
		return "", errors.New("invalid catalog rate")
	}
	r, ok := new(big.Rat).SetString(n.String())
	if !ok {
		return "", errors.New("invalid catalog decimal")
	}
	s := r.FloatString(9)
	again, _ := new(big.Rat).SetString(s)
	if r.Cmp(again) != 0 {
		return "", errors.New("catalog rate precision unsupported")
	}
	if _, e := parseRate(s); e != nil {
		return "", e
	}
	return s, nil
}
func parseCatalogCost(raw json.RawMessage) (Price, error) {
	p := Price{Version: "catalog"}
	var obj map[string]json.RawMessage
	if e := json.Unmarshal(raw, &obj); e != nil {
		return p, e
	}
	rates := func(m map[string]json.RawMessage, base Price) (Price, error) {
		for key, dst := range map[string]*string{"input": &base.Input, "output": &base.Output, "cache_read": &base.CacheRead, "cache_write": &base.CacheWrite} {
			if v, ok := m[key]; ok {
				rate, e := catalogRate(v)
				if e != nil {
					return base, e
				}
				*dst = rate
			}
		}
		if v, ok := m["reasoning"]; ok {
			rate, e := catalogRate(v)
			if e != nil {
				return base, e
			}
			a, _ := parseRate(rate)
			b, e := parseRate(base.Output)
			if e != nil || a.Cmp(b) != 0 {
				return base, errors.New("separate reasoning rate unsupported")
			}
		}
		return base, base.Validate()
	}
	for key := range obj {
		switch key {
		case "input", "output", "cache_read", "cache_write", "reasoning", "tiers", "context_over_200k":
		default:
			return p, fmt.Errorf("unsupported price dimension: %s", key)
		}
	}
	var e error
	p, e = rates(obj, p)
	if e != nil {
		return p, e
	}
	var tiers []map[string]json.RawMessage
	if raw, ok := obj["tiers"]; ok {
		if e = json.Unmarshal(raw, &tiers); e != nil {
			return p, e
		}
	}
	if len(tiers) == 0 {
		if raw, ok := obj["context_over_200k"]; ok {
			var tier map[string]json.RawMessage
			if e = json.Unmarshal(raw, &tier); e != nil || tier == nil {
				return p, errors.New("invalid legacy context tier")
			}
			tier["tier"] = json.RawMessage(`{"type":"context","size":200000}`)
			tiers = append(tiers, tier)
		}
	}
	seen := map[int64]bool{}
	for _, t := range tiers {
		for key := range t {
			switch key {
			case "input", "output", "cache_read", "cache_write", "reasoning", "tier":
			default:
				return p, fmt.Errorf("unsupported tier dimension: %s", key)
			}
		}
		var spec struct {
			Type string `json:"type"`
			Size int64  `json:"size"`
		}
		if e = json.Unmarshal(t["tier"], &spec); e != nil || spec.Type != "context" || spec.Size < 1 || spec.Size > MaxSafeInteger || seen[spec.Size] {
			return p, errors.New("unsupported or ambiguous context tier")
		}
		seen[spec.Size] = true
		tier, e := rates(t, Price{Version: p.Version, Input: p.Input, Output: p.Output, CacheRead: p.CacheRead, CacheWrite: p.CacheWrite})
		if e != nil {
			return p, e
		}
		p.Tiers = append(p.Tiers, PriceTier{spec.Size, tier.Input, tier.Output, tier.CacheRead, tier.CacheWrite})
	}
	sort.Slice(p.Tiers, func(i, j int) bool { return p.Tiers[i].Above < p.Tiers[j].Above })
	// Both upstream representations must agree, not silently mask a conflict.
	if legacy, ok := obj["context_over_200k"]; ok && len(obj["tiers"]) > 0 {
		var m map[string]json.RawMessage
		if e = json.Unmarshal(legacy, &m); e != nil {
			return p, e
		}
		for key := range m {
			switch key {
			case "input", "output", "cache_read", "cache_write", "reasoning":
			default:
				return p, fmt.Errorf("unsupported legacy tier dimension: %s", key)
			}
		}
		l, e := rates(m, Price{Version: p.Version, Input: p.Input, Output: p.Output, CacheRead: p.CacheRead, CacheWrite: p.CacheWrite})
		if e != nil {
			return p, e
		}
		found := false
		for _, t := range p.Tiers {
			if t.Above == 200000 {
				found = true
				if strings.Join([]string{t.Input, t.Output, t.CacheRead, t.CacheWrite}, "|") != strings.Join([]string{l.Input, l.Output, l.CacheRead, l.CacheWrite}, "|") {
					return p, errors.New("conflicting context tier representations")
				}
			}
		}
		if !found {
			return p, errors.New("unmatched legacy context tier")
		}
	}
	return p, nil
}
