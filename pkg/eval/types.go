// Package eval runs the production Go Agent with injected fixture/live services.
// It has no Runtime, Control, product storage or provider credential dependency.
package eval

import (
	"encoding/json"
	"github.com/jjyr/pot/pkg/core"
)

const Schema uint32 = 2

var Revision string

type Scenario struct {
	ID                string             `json:"id"`
	Profile           core.Profile       `json:"profile"`
	Protocol          string             `json:"protocol"`
	Turns             []string           `json:"turns"`
	Responses         []ScriptedResponse `json:"responses"`
	Fixtures          map[string]string  `json:"fixtures"`
	ExpectedFiles     map[string]string  `json:"expected_files"`
	ExpectedFinalText *string            `json:"expected_final_text"`
	TurnAssertions    []OutputAssertion  `json:"turn_assertions,omitempty"`
	DeadlineMS        uint64             `json:"deadline_ms"`
}
type ScriptedResponse struct {
	Stage        string            `json:"stage"`
	Frames       []json.RawMessage `json:"frames"`
	BodyContains []string          `json:"body_contains,omitempty"`
}
type OutputAssertion struct {
	Kind     string          `json:"kind"`
	Expected json.RawMessage `json:"expected"`
}

func (a OutputAssertion) Accepts(text string) bool {
	if a.Kind == "text" {
		var expected string
		return json.Unmarshal(a.Expected, &expected) == nil && expected == text
	}
	if a.Kind != "json" {
		return false
	}
	left, err := core.CanonicalJSON(json.RawMessage(text))
	right, e := core.CanonicalJSON(a.Expected)
	return err == nil && e == nil && string(left) == string(right)
}

type Manifest struct {
	SchemaVersion   uint32            `json:"schema_version"`
	RunID           string            `json:"run_id"`
	ScenarioID      string            `json:"scenario_id"`
	ScenarioHash    string            `json:"scenario_hash"`
	Mode            string            `json:"mode"`
	Executor        string            `json:"executor"`
	BuildVersion    string            `json:"build_version"`
	RuntimeRevision *string           `json:"runtime_revision"`
	EffectiveSpec   core.ResolvedSpec `json:"effective_spec"`
	DeadlineMS      uint64            `json:"deadline_ms"`
	Transport       json.RawMessage   `json:"transport,omitempty"`
	ExecutionTarget string            `json:"execution_target"`
}
type Payload struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}
type Event struct {
	SchemaVersion uint32  `json:"schema_version"`
	RunID         string  `json:"run_id"`
	Seq           uint64  `json:"seq"`
	ElapsedMS     uint64  `json:"elapsed_ms"`
	TurnID        string  `json:"turn_id"`
	Payload       Payload `json:"payload"`
}
type Report struct {
	SchemaVersion       uint32            `json:"schema_version"`
	RunID               string            `json:"run_id"`
	ScenarioID          string            `json:"scenario_id"`
	ScenarioHash        string            `json:"scenario_hash"`
	Mode                string            `json:"mode"`
	Success             bool              `json:"success"`
	Errors              []string          `json:"errors"`
	Usage               Totals            `json:"usage"`
	StageUsage          map[string]Totals `json:"stage_usage"`
	TurnUsage           map[string]Totals `json:"turn_usage"`
	TurnAnswers         map[string]string `json:"turn_answers"`
	RequestCount        int               `json:"request_count"`
	ToolCount           int               `json:"tool_count"`
	CompactionCount     int               `json:"compaction_count"`
	MeasurementComplete bool              `json:"measurement_complete"`
	TransportHash       *string           `json:"transport_hash"`
}
type UsageReport struct {
	MeasurementID    string  `json:"measurement_id"`
	SourceEventID    string  `json:"source_event_id"`
	Scope            string  `json:"scope"`
	Source           string  `json:"source"`
	Counter          string  `json:"counter"`
	Complete         bool    `json:"complete"`
	InputTokens      *uint64 `json:"input_tokens"`
	OutputTokens     *uint64 `json:"output_tokens"`
	CacheReadTokens  *uint64 `json:"cache_read_tokens"`
	CacheWriteTokens *uint64 `json:"cache_write_tokens"`
	ReasoningTokens  *uint64 `json:"reasoning_tokens"`
	BaselineInput    *uint64 `json:"baseline_input"`
	BaselineOutput   *uint64 `json:"baseline_output"`
}
type Totals struct {
	InputTokens      *uint64 `json:"input_tokens"`
	OutputTokens     *uint64 `json:"output_tokens"`
	CacheReadTokens  *uint64 `json:"cache_read_tokens"`
	CacheWriteTokens *uint64 `json:"cache_write_tokens"`
	ReasoningTokens  *uint64 `json:"reasoning_tokens"`
	Measurements     int     `json:"measurements"`
}
