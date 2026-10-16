package eval

import (
	"fmt"
	"github.com/jjyr/pot/pkg/core"
	"math"
)

func value(n uint64) *uint64 { return &n }
func add(a, b *uint64) (*uint64, error) {
	if a == nil || b == nil {
		return nil, nil
	}
	if *a > math.MaxUint64-*b {
		return nil, fmt.Errorf("token overflow")
	}
	return value(*a + *b), nil
}
func equal(a, b *uint64) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
func fields(t *Totals) []**uint64 {
	return []**uint64{&t.InputTokens, &t.OutputTokens, &t.CacheReadTokens, &t.CacheWriteTokens, &t.ReasoningTokens}
}
func merge(a, b Totals, requireAgreement bool) (Totals, error) {
	af, bf := fields(&a), fields(&b)
	for i, p := range af {
		if requireAgreement {
			if *p != nil && *bf[i] != nil && **p != **bf[i] {
				return a, fmt.Errorf("usage sources disagree")
			}
			if *p == nil {
				*p = *bf[i]
			}
		} else {
			n, err := add(*p, *bf[i])
			if err != nil {
				return a, err
			}
			*p = n
		}
	}
	return a, nil
}
func checked(r UsageReport) (Totals, error) {
	if r.InputTokens != nil {
		for _, p := range []*uint64{r.CacheReadTokens, r.CacheWriteTokens} {
			if p != nil && *p > *r.InputTokens {
				return Totals{}, fmt.Errorf("cache subset exceeds input")
			}
		}
	}
	if r.ReasoningTokens != nil && r.OutputTokens != nil && *r.ReasoningTokens > *r.OutputTokens {
		return Totals{}, fmt.Errorf("reasoning subset exceeds output")
	}
	t := Totals{r.InputTokens, r.OutputTokens, r.CacheReadTokens, r.CacheWriteTokens, r.ReasoningTokens, 1}
	if r.Counter == "cumulative" {
		t.CacheReadTokens = nil
		t.CacheWriteTokens = nil
		t.ReasoningTokens = nil
		for _, p := range []struct {
			end, start *uint64
			out        **uint64
		}{{r.InputTokens, r.BaselineInput, &t.InputTokens}, {r.OutputTokens, r.BaselineOutput, &t.OutputTokens}} {
			if p.end == nil || p.start == nil {
				*p.out = nil
			} else {
				if *p.end < *p.start {
					return Totals{}, fmt.Errorf("cumulative counter reset")
				}
				*p.out = value(*p.end - *p.start)
			}
		}
	}
	if !r.Complete {
		t = Totals{Measurements: 1}
	}
	return t, nil
}

// Aggregate reconciles explicit measurement/source identities. Parent scopes,
// missing subsets and incomplete failed usage never become invented extra tokens.
func Aggregate(reports []UsageReport, scope string) (Totals, error) {
	type source struct {
		counter string
		total   Totals
		events  map[string]string
		last    *UsageReport
	}
	sources := map[string]*source{}
	for _, r := range reports {
		if r.Scope != scope {
			continue
		}
		if r.Source != "provider" && r.Source != "native" || r.MeasurementID == "" || r.SourceEventID == "" || (r.Counter != "snapshot" && r.Counter != "cumulative" && r.Counter != "delta") {
			return Totals{}, fmt.Errorf("invalid observed usage identity/semantics")
		}
		t, err := checked(r)
		if err != nil {
			return Totals{}, err
		}
		key := r.MeasurementID + "\x00" + r.Source
		s := sources[key]
		if s == nil {
			s = &source{counter: r.Counter, events: map[string]string{}}
			sources[key] = s
		}
		if s.counter != r.Counter {
			return Totals{}, fmt.Errorf("counter semantics changed")
		}
		serialized, err := core.HashJSON(r)
		if err != nil {
			return Totals{}, err
		}
		if old, ok := s.events[r.SourceEventID]; ok {
			if old != serialized {
				return Totals{}, fmt.Errorf("conflicting duplicate usage event")
			}
			continue
		}
		s.events[r.SourceEventID] = serialized
		if r.Counter == "cumulative" && s.last != nil {
			if !equal(s.last.BaselineInput, r.BaselineInput) || !equal(s.last.BaselineOutput, r.BaselineOutput) {
				return Totals{}, fmt.Errorf("cumulative baseline changed")
			}
			for _, pair := range [][2]*uint64{{s.last.InputTokens, r.InputTokens}, {s.last.OutputTokens, r.OutputTokens}} {
				if pair[0] != nil && pair[1] != nil && *pair[1] < *pair[0] {
					return Totals{}, fmt.Errorf("cumulative counter reset")
				}
			}
		}
		if r.Counter == "delta" && s.total.Measurements != 0 {
			s.total, err = merge(s.total, t, false)
			if err != nil {
				return Totals{}, err
			}
		} else {
			s.total = t
		}
		copy := r
		if r.Counter == "cumulative" && s.last != nil {
			if copy.InputTokens == nil {
				copy.InputTokens = s.last.InputTokens
			}
			if copy.OutputTokens == nil {
				copy.OutputTokens = s.last.OutputTokens
			}
		}
		s.last = &copy
	}
	unique := map[string]Totals{}
	for key, s := range sources {
		id := key[:len(key)-len(s.last.Source)-1]
		if old, ok := unique[id]; ok {
			t, err := merge(old, s.total, true)
			if err != nil {
				return Totals{}, err
			}
			unique[id] = t
		} else {
			unique[id] = s.total
		}
	}
	if len(unique) == 0 {
		return Totals{}, nil
	}
	total := Totals{value(0), value(0), value(0), value(0), value(0), len(unique)}
	for _, t := range unique {
		n, err := merge(total, t, false)
		if err != nil {
			return Totals{}, err
		}
		n.Measurements = total.Measurements
		total = n
	}
	return total, nil
}
