package schedule

import (
	"github.com/jjyr/pot/pkg/cronexpr"
	"github.com/jjyr/pot/pkg/protocol"
)

// Preview only evaluates time rules; it cannot admit Tasks or change definitions.
// Field and code are stable presentation identifiers; diagnostics remain verbatim.
type PreviewRequest struct {
	Kind         string `json:"kind"`
	Cron         string `json:"cron"`
	Timezone     string `json:"timezone"`
	At           string `json:"at"`
	EveryMinutes uint64 `json:"every_minutes"`
}
type PreviewResult struct {
	Timezone   string  `json:"timezone"`
	Times      []int64 `json:"times"`
	Field      string  `json:"field,omitempty"`
	Code       string  `json:"code,omitempty"`
	Diagnostic string  `json:"diagnostic,omitempty"`
}

func Preview(request PreviewRequest, at int64) PreviewResult {
	result := PreviewResult{Timezone: request.Timezone, Times: []int64{}}
	fail := func(field, code string, err error) PreviewResult {
		result.Field, result.Code, result.Diagnostic = field, code, err.Error()
		return result
	}
	if _, err := cronexpr.Location(request.Timezone); err != nil {
		return fail("timezone", "timezone", err)
	}
	spec := protocol.ScheduleSpec{Title: "Preview", Goal: "Preview", Kind: request.Kind, Cron: request.Cron, Timezone: request.Timezone, At: request.At, EveryMinutes: request.EveryMinutes, Notify: "changes", Misfire: "coalesce"}
	field := map[string]string{"cron": "cron", "once": "at", "interval": "every_minutes"}[request.Kind]
	if field == "" {
		field = "kind"
	}
	for range 5 {
		next, err := Next(spec, at)
		if err != nil {
			return fail(field, "rule", err)
		}
		if next == nil {
			break
		}
		result.Times = append(result.Times, *next)
		at = *next
	}
	return result
}
