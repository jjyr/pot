package eval

import (
	"errors"
	"math/big"
)

// Compare reports only differences between comparable complete measurements;
// no two-run comparison alone establishes an efficiency claim.
func Compare(base, candidate Report) (map[string]any, error) {
	if base.SchemaVersion != candidate.SchemaVersion || base.ScenarioHash != candidate.ScenarioHash || base.Mode != candidate.Mode || !equalString(base.TransportHash, candidate.TransportHash) {
		return nil, errors.New("evaluation runs are not comparable")
	}
	comparable := base.Success && candidate.Success && base.MeasurementComplete && candidate.MeasurementComplete
	delta := map[string]any{}
	for name, pair := range map[string][2]*uint64{"input_tokens": {base.Usage.InputTokens, candidate.Usage.InputTokens}, "output_tokens": {base.Usage.OutputTokens, candidate.Usage.OutputTokens}, "cache_read_tokens": {base.Usage.CacheReadTokens, candidate.Usage.CacheReadTokens}, "cache_write_tokens": {base.Usage.CacheWriteTokens, candidate.Usage.CacheWriteTokens}, "reasoning_tokens": {base.Usage.ReasoningTokens, candidate.Usage.ReasoningTokens}} {
		if !comparable || pair[0] == nil || pair[1] == nil {
			delta[name] = nil
			continue
		}
		difference := new(big.Int).Sub(new(big.Int).SetUint64(*pair[1]), new(big.Int).SetUint64(*pair[0]))
		delta[name] = difference
	}
	return map[string]any{"schema_version": Schema, "baseline": base.RunID, "candidate": candidate.RunID, "comparable": comparable, "usage_delta": delta, "empirical_efficiency_claim": false}, nil
}
func equalString(a, b *string) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }
